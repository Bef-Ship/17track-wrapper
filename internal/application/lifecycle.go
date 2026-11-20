package application

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
	"github.com/SolaTyolo/17track-wrapper/pkg/track17"
)

const (
	jobPageSize  = 200
	sdkBatchSize = 40
	// registerStuckAfter: overlapping a pending asynq retry is harmless (17TRACK
	// "already registered" counts as success).
	registerStuckAfter = time.Hour
)

// Update is emitted when a package's events or status / subscription / carrier / return
// state changed.
type Update struct {
	Package domain.Package
	// Parsed is nil when the change carried no tracking detail (stop / cancel / retrack).
	Parsed *track17.ParsedTrack
	// NewEvents are the events first stored by this change.
	NewEvents []domain.TrackingEvent
	// Pushed is true when the change came from a 17TRACK webhook (vs poll / job).
	Pushed bool
}

// Observer receives package updates (e.g. the shipment projection).
type Observer interface {
	OnTrackingUpdate(ctx context.Context, u Update) error
}

// AddObserver registers o; call during wiring only.
func (s *Service) AddObserver(o Observer) {
	if o != nil {
		s.observers = append(s.observers, o)
	}
}

// notify fans u out to every observer. Package state is already committed, so failures
// are logged; the returned error lets webhook callers nack for a provider retry.
func (s *Service) notify(ctx context.Context, u Update) error {
	var first error
	for _, o := range s.observers {
		if err := o.OnTrackingUpdate(ctx, u); err != nil {
			blog.Svc("tracking").Error("tracking update observer failed",
				"tracking_number", u.Package.TrackingNumber, "err", err.Error())
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// Refresh pulls current detail from 17TRACK for the current generation of number and
// applies it, restoring the package from the archive first when needed.
func (s *Service) Refresh(ctx context.Context, number string) (domain.Package, error) {
	pkg, err := s.lookup(ctx, domain.PackageRef{Number: number})
	if err != nil {
		return domain.Package{}, err
	}
	client, err := s.client()
	if err != nil || client == nil {
		return domain.Package{}, domain.Errorf(domain.ErrNotConfigured, domain.CodeInternal, "17TRACK client not configured")
	}
	res, err := client.GetTrackInfo(ctx, []sdk.TrackNumber{trackRef(*pkg)})
	if err != nil {
		b := upstreamError(err)
		return domain.Package{}, domain.Errorf(track17.WrapRetryable(err), b.Code, "%s", b.Message)
	}
	if res != nil && len(res.Rejected) > 0 && len(res.Accepted) == 0 {
		b := rejectBody(res.Rejected[0])
		return domain.Package{}, domain.Errorf(domain.ErrConflict, b.Code, "%s", b.Message)
	}
	if res == nil || len(res.Accepted) == 0 {
		return *pkg, nil
	}
	if pkg.IsArchived {
		restored, err := s.Restore(ctx, pkg.PackageID)
		if err != nil {
			return domain.Package{}, fmt.Errorf("restore: %w", err)
		}
		pkg = &restored
	}
	if pkg, err = s.applyDetail(ctx, *pkg, &res.Accepted[0]); err != nil {
		return domain.Package{}, err
	}
	return *pkg, nil
}

// SyncActive polls 17TRACK for TRACKING packages with no data for syncStaleAfter
// (webhook backstop; recently heard packages are skipped to save quota).
func (s *Service) SyncActive(ctx context.Context) (int, error) {
	client, err := s.client()
	if err != nil || client == nil {
		return 0, nil
	}
	since := time.Now().UTC().Add(-s.syncStaleAfter)
	list := func(ctx context.Context, afterID int64) ([]domain.Package, error) {
		return s.repo.ListTrackingNotHeardSince(ctx, since, afterID, jobPageSize)
	}
	n := 0
	err = s.eachBatch(ctx, list, func(items []domain.Package) error {
		refs := make([]sdk.TrackNumber, len(items))
		for i, it := range items {
			refs[i] = trackRef(it)
		}
		res, err := client.GetTrackInfo(ctx, refs)
		if err != nil {
			// Permanent errors skip this batch; retryable (429/5xx) also skip —
			// rate limiter + next cron will catch up. Do not abort the whole job.
			blog.Svc("tracking").Warn("sync: GetTrackInfo failed",
				"retryable", track17.IsRetryable(err), "err", err.Error())
			return nil
		}
		if res == nil {
			return nil
		}
		details := make(map[string]*sdk.TrackDetail, len(res.Accepted))
		for i := range res.Accepted {
			details[res.Accepted[i].Number] = &res.Accepted[i]
		}
		for i := range items {
			d, ok := details[items[i].TrackingNumber]
			if !ok {
				continue
			}
			if _, err := s.applyDetail(ctx, items[i], d); err != nil {
				blog.Svc("tracking").Warn("sync: apply failed",
					"tracking_number", items[i].TrackingNumber, "err", err.Error())
				continue
			}
			n++
		}
		return nil
	})
	return n, err
}

// StopStale stops TRACKING packages delivered > 7d ago or idle > 30d.
func (s *Service) StopStale(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	n := 0
	err := s.eachBatch(ctx, s.bySubscription(domain.SubTracking), func(items []domain.Package) error {
		stale := make([]domain.Package, 0)
		for _, it := range items {
			if track17.ShouldStopTracking(now, it.Status, it.LatestEventTime, it.DeliveredAt) {
				stale = append(stale, it)
			}
		}
		if len(stale) == 0 {
			return nil
		}
		stopped := make([]domain.Package, 0, len(stale))
		for i := range stale {
			pkg, applied, err := s.update(ctx, byID(stale[i].PackageID),
				func(p *domain.Package) bool {
					return p.SubscriptionStatus == domain.SubTracking &&
						track17.ShouldStopTracking(now, p.Status, p.LatestEventTime, p.DeliveredAt)
				},
				func(p *domain.Package) { p.SubscriptionStatus = domain.SubStopped })
			if err != nil {
				return err
			}
			if !applied {
				continue
			}
			stopped = append(stopped, *pkg)
			_ = s.notify(ctx, Update{Package: *pkg})
			n++
		}
		s.stopUpstream(ctx, stopped)
		return nil
	})
	return n, err
}

// RetrackStopped restarts STOPPED, undelivered, unarchived packages once.
func (s *Service) RetrackStopped(ctx context.Context) (int, error) {
	client, err := s.client()
	if err != nil || client == nil {
		return 0, nil
	}
	n := 0
	err = s.eachBatch(ctx, s.bySubscription(domain.SubStopped), func(items []domain.Package) error {
		todo := make([]domain.Package, 0, len(items))
		for _, it := range items {
			if it.IsRetracked || it.IsDelivered() || it.IsArchived {
				continue
			}
			todo = append(todo, it)
		}
		if len(todo) == 0 {
			return nil
		}
		refs := make([]sdk.TrackNumber, len(todo))
		for i, it := range todo {
			refs[i] = trackRef(it)
		}
		res, err := client.Retrack(ctx, refs)
		if err != nil {
			blog.Svc("tracking").Warn("retrack failed",
				"retryable", track17.IsRetryable(err), "err", err.Error())
			return nil
		}
		if res == nil {
			return nil
		}
		ok := make(map[string]bool, len(res.Accepted))
		for _, a := range res.Accepted {
			ok[a.Number] = true
		}
		for i := range todo {
			if !ok[todo[i].TrackingNumber] {
				continue
			}
			pkg, applied, err := s.update(ctx, byID(todo[i].PackageID),
				func(p *domain.Package) bool { return p.SubscriptionStatus == domain.SubStopped && !p.IsArchived },
				func(p *domain.Package) {
					p.IsRetracked = true
					p.SubscriptionStatus = domain.SubTracking
				})
			if err != nil {
				return err
			}
			if !applied {
				continue
			}
			_ = s.notify(ctx, Update{Package: *pkg})
			n++
		}
		return nil
	})
	return n, err
}

// RetryRegistering re-submits packages left in REGISTERING for registerStuckAfter
// (queue retries exhausted or the job was lost). Returns how many left REGISTERING.
func (s *Service) RetryRegistering(ctx context.Context) (int, error) {
	if _, err := s.client(); err != nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-registerStuckAfter)
	n := 0
	err := s.eachBatch(ctx, s.bySubscription(domain.SubRegistering), func(items []domain.Package) error {
		for i := range items {
			pkg := items[i]
			if pkg.UpdatedAt.After(cutoff) {
				continue
			}
			if res := s.finishRegister(ctx, &pkg, false); res.SubscriptionStatus != domain.SubRegistering {
				n++
			}
		}
		return nil
	})
	return n, err
}

type pageLister func(ctx context.Context, afterID int64) ([]domain.Package, error)

func (s *Service) bySubscription(status string) pageLister {
	return func(ctx context.Context, afterID int64) ([]domain.Package, error) {
		return s.repo.ListPackagesBySubscriptionStatus(ctx, status, afterID, jobPageSize)
	}
}

func (s *Service) eachBatch(ctx context.Context, list pageLister, fn func([]domain.Package) error) error {
	var after int64
	for {
		page, err := list(ctx, after)
		if err != nil {
			return err
		}
		for i := 0; i < len(page); i += sdkBatchSize {
			end := min(i+sdkBatchSize, len(page))
			if err := fn(page[i:end]); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if len(page) < jobPageSize {
			return nil
		}
		after = page[len(page)-1].PackageID
	}
}

// applyDetail ingests a polled 17TRACK detail into one generation and notifies observers
// when something changed.
func (s *Service) applyDetail(ctx context.Context, pkg domain.Package, d *sdk.TrackDetail) (*domain.Package, error) {
	parsed := track17.ParseDetail(d)
	batch := batchFromParsed(pkg.TrackingNumber, parsed)
	now := time.Now().UTC()
	res, err := s.ingest(ctx, byID(pkg.PackageID), &batch, ingestOpts{
		after: func(p *domain.Package) {
			applyReturnToPackage(p, batch.Events, now)
			applyResolvedCarrier(p, parsed.Carrier)
			p.LastCallbackAt = &now
		},
		publish: func(r ingestResult) Update {
			return Update{Package: *r.Package, Parsed: &parsed, NewEvents: r.Inserted}
		},
	})
	if err != nil {
		return nil, err
	}
	return res.Package, nil
}

// stopUpstream stops the 17TRACK subscription for each package (one package per number).
func (s *Service) stopUpstream(ctx context.Context, pkgs []domain.Package) {
	client, err := s.client()
	if err != nil || client == nil {
		return
	}
	refs := make([]sdk.TrackNumber, 0, len(pkgs))
	for _, p := range pkgs {
		refs = append(refs, trackRef(p))
	}
	for i := 0; i < len(refs); i += sdkBatchSize {
		_, err := client.StopTrack(ctx, refs[i:min(i+sdkBatchSize, len(refs))])
		if err != nil && track17.IsRetryable(err) {
			blog.Svc("tracking").Warn("stopTrack retryable", "err", err.Error())
		}
	}
}
