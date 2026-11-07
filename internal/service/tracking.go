package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/archive"
	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/metrics"
	"github.com/SolaTyolo/tracking-service/internal/olap"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

var (
	// ErrArchiveDisabled is returned by archive operations when ARCHIVE_ENABLED=false.
	ErrArchiveDisabled = errors.New("archive disabled")
	// ErrJobRunning is returned when another instance holds the archive job lock.
	ErrJobRunning = errors.New("archive job already running")
	// ErrInvalid wraps caller input errors.
	ErrInvalid = errors.New("invalid input")
)

const (
	archiveLockName = "tracking-service:archive"
	olapTimeout     = 5 * time.Second
)

// Tracking is the core package↔events application service.
// cold is nil when archiving is disabled; olapReader is nil when OLAP reads are unavailable.
type Tracking struct {
	store        store.Store
	olap         olap.Writer
	olapReader   olap.Reader
	cold         archive.ColdStore
	retention    time.Duration
	archiveBatch int
}

func NewTracking(st store.Store, olapWriter olap.Writer, cold archive.ColdStore, retentionDays, archiveBatch int) *Tracking {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	if archiveBatch <= 0 {
		archiveBatch = 200
	}
	if olapWriter == nil {
		olapWriter = olap.Noop{}
	}
	reader, _ := olapWriter.(olap.Reader)
	return &Tracking{
		store:        st,
		olap:         olapWriter,
		olapReader:   reader,
		cold:         cold,
		retention:    time.Duration(retentionDays) * 24 * time.Hour,
		archiveBatch: archiveBatch,
	}
}

// RegisterPackage creates or returns an existing package for a tracking number.
func (t *Tracking) RegisterPackage(ctx context.Context, pkg *domain.Package) (*domain.Package, error) {
	if pkg.TrackingNumber == "" || pkg.CarrierCode == "" {
		return nil, fmt.Errorf("%w: tracking_number and carrier_code required", ErrInvalid)
	}
	if pkg.TenantID == "" {
		pkg.TenantID = domain.DefaultTenant
	}
	if err := t.store.CreatePackage(ctx, pkg); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			existing, getErr := t.store.GetPackageByTrackingNumber(ctx, pkg.TenantID, pkg.TrackingNumber)
			if getErr != nil {
				return nil, getErr
			}
			slog.InfoContext(ctx, "package already registered",
				"tracking_number", pkg.TrackingNumber, "package_id", existing.PackageID)
			return existing, nil
		}
		return nil, err
	}
	slog.InfoContext(ctx, "package registered",
		"tenant_id", pkg.TenantID,
		"package_id", pkg.PackageID,
		"tracking_number", pkg.TrackingNumber,
		"carrier_code", pkg.CarrierCode,
		"provider", pkg.ProviderName,
		"external_ref", pkg.ExternalRef,
	)
	return pkg, nil
}

// IngestBatch applies a normalized event batch in one transaction holding the
// package row lock: unarchive if needed, idempotent event insert, package
// update (skipped for stale payloads), then OLAP sync of the new events.
func (t *Tracking) IngestBatch(ctx context.Context, batch domain.EventBatch) error {
	if batch.TenantID == "" {
		batch.TenantID = domain.DefaultTenant
	}
	var (
		pkg      *domain.Package
		inserted []domain.TrackingEvent
		stale    bool
	)
	err := t.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		pkg, err = tx.GetPackageForUpdate(ctx, batch.TenantID, batch.TrackingNumber)
		if err != nil {
			return err
		}
		changed := pkg.IsArchived
		if pkg.IsArchived {
			if err := t.unarchive(ctx, tx, pkg); err != nil {
				return fmt.Errorf("unarchive: %w", err)
			}
		}
		for i := range batch.Events {
			batch.Events[i].PackageID = pkg.PackageID
			batch.Events[i].TrackingNumber = pkg.TrackingNumber
		}
		if inserted, err = tx.InsertEvents(ctx, batch.Events); err != nil {
			return err
		}
		// Events are idempotent and always kept; only package state is
		// protected from payloads older than the last applied one.
		stale = pkg.LatestUpdateAt != nil && !batch.UpdateTime.IsZero() && batch.UpdateTime.Before(*pkg.LatestUpdateAt)
		if !stale {
			applyBatch(pkg, batch)
			changed = true
		}
		if !changed {
			return nil
		}
		return tx.UpdatePackage(ctx, pkg)
	})
	if err != nil {
		if store.IsNotFound(err) {
			slog.WarnContext(ctx, "ingest skipped: unknown tracking_number",
				"tenant_id", batch.TenantID, "tracking_number", batch.TrackingNumber)
			return fmt.Errorf("%w: tracking_number %s", store.ErrNotFound, batch.TrackingNumber)
		}
		return err
	}
	metrics.EventsIngested.Add(float64(len(inserted)))
	slog.InfoContext(ctx, "events ingested",
		"tenant_id", batch.TenantID,
		"package_id", pkg.PackageID,
		"tracking_number", pkg.TrackingNumber,
		"received", len(batch.Events),
		"inserted", len(inserted),
		"stale", stale,
		"status", pkg.Status,
		"provider", batch.ProviderName,
	)
	t.syncOLAP(ctx, pkg, inserted)
	return nil
}

// syncOLAP writes newly inserted events to the OLAP store. PG is already
// committed, so failures are only logged and counted.
func (t *Tracking) syncOLAP(ctx context.Context, pkg *domain.Package, events []domain.TrackingEvent) {
	if len(events) == 0 || t.olap == nil {
		return
	}
	rows := make([]olap.Event, 0, len(events))
	for _, ev := range events {
		rows = append(rows, olap.FromDomain(pkg, ev))
	}
	// Detached from the request so a client disconnect does not drop the write.
	octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), olapTimeout)
	defer cancel()
	if err := t.olap.InsertEvents(octx, rows); err != nil {
		metrics.OLAPInsertFailures.Inc()
		slog.ErrorContext(ctx, "olap insert failed",
			"err", err, "olap", t.olap.Name(), "package_id", pkg.PackageID, "count", len(rows))
	}
}

// applyBatch merges a tracking update into the package. Fields absent from
// the batch keep their current value.
func applyBatch(pkg *domain.Package, batch domain.EventBatch) {
	if batch.CarrierCode != "" {
		pkg.CarrierCode = batch.CarrierCode
	}
	if batch.ProviderName != "" {
		pkg.ProviderName = batch.ProviderName
	}
	if batch.Status != "" {
		pkg.Status = batch.Status
		pkg.SubStatus = batch.SubStatus
		pkg.SubStatusDesc = batch.SubStatusDesc
	}
	pkg.ShippingDetail.Merge(batch.ShippingDetail)

	// Explicit milestones win; staged events only fill stages not yet known.
	merged := domain.Milestones{}
	for _, ev := range batch.Events {
		if ev.Stage == "" {
			continue
		}
		if cur, ok := merged[ev.Stage]; !ok || ev.EventTime.Before(cur) {
			merged[ev.Stage] = ev.EventTime
		}
	}
	for stage, at := range pkg.Milestones {
		merged[stage] = at
	}
	for stage, at := range batch.Milestones {
		merged[stage] = at
	}
	if len(merged) > 0 {
		pkg.Milestones = merged
	}
	if at, ok := pkg.Milestones[domain.StagePickedUp]; ok {
		pkg.PickedUpAt = &at
	}
	if at, ok := pkg.Milestones[domain.StageDelivered]; ok {
		pkg.DeliveredAt = &at
		if batch.Status == "" && pkg.Status != domain.StatusDelivered {
			pkg.Status = domain.StatusDelivered
			pkg.SubStatus = ""
			pkg.SubStatusDesc = ""
		}
	}

	for _, ev := range batch.Events {
		if pkg.LatestEventTime == nil || ev.EventTime.After(*pkg.LatestEventTime) {
			et := ev.EventTime
			pkg.LatestEventTime = &et
			pkg.LatestEventDesc = ev.Description
			pkg.LatestEventLocation = ev.Location
		}
	}

	// Archiving keys off delivered_at, so a delivered package without a
	// delivered milestone falls back to its latest event / update / current time.
	if pkg.Status == domain.StatusDelivered && pkg.DeliveredAt == nil {
		at := time.Now().UTC()
		if pkg.LatestEventTime != nil {
			at = *pkg.LatestEventTime
		} else if !batch.UpdateTime.IsZero() {
			at = batch.UpdateTime
		}
		pkg.DeliveredAt = &at
	}

	// Server time must not become latest_update_at, or later payloads carrying
	// the carrier's (earlier) clock would all be treated as stale.
	if !batch.UpdateTime.IsZero() {
		ut := batch.UpdateTime
		pkg.LatestUpdateAt = &ut
	}
}

// GetPackageEvents returns events from PG or cold archive based on is_archived.
func (t *Tracking) GetPackageEvents(ctx context.Context, tenantID, trackingNumber string) (*domain.Package, []domain.TrackingEvent, error) {
	pkg, err := t.store.GetPackageByTrackingNumber(ctx, tenantID, trackingNumber)
	if err != nil {
		return nil, nil, err
	}
	if !pkg.IsArchived {
		events, err := t.store.ListEventsByPackage(ctx, pkg.PackageID)
		return pkg, events, err
	}
	if t.cold == nil || pkg.ArchiveKey == "" {
		return pkg, nil, fmt.Errorf("package archived but cold store unavailable")
	}
	snap, err := t.cold.Get(ctx, pkg.ArchiveKey)
	if err != nil {
		return nil, nil, err
	}
	slog.InfoContext(ctx, "served events from cold archive",
		"tracking_number", trackingNumber, "archive_key", pkg.ArchiveKey, "backend", t.cold.Name())
	return pkg, snap.Events, nil
}

// ListPackages returns one page of a tenant's packages, newest first.
func (t *Tracking) ListPackages(ctx context.Context, f store.PackageFilter) ([]domain.Package, error) {
	return t.store.ListPackages(ctx, f)
}

// QueryOLAPEvents lists denormalized events from the OLAP store.
func (t *Tracking) QueryOLAPEvents(ctx context.Context, q olap.EventQuery) ([]olap.Event, error) {
	if t.olapReader == nil {
		return nil, olap.ErrDisabled
	}
	return t.olapReader.QueryEvents(ctx, q)
}

// OLAPStats aggregates events from the OLAP store.
func (t *Tracking) OLAPStats(ctx context.Context, q olap.StatsQuery) ([]olap.StatsRow, error) {
	if t.olapReader == nil {
		return nil, olap.ErrDisabled
	}
	return t.olapReader.Stats(ctx, q)
}

// ArchiveDuePackages moves events of packages delivered before the retention
// cutoff to cold storage, in batches of archiveBatch until none are left.
// Only one instance runs it at a time (ErrJobRunning otherwise).
func (t *Tracking) ArchiveDuePackages(ctx context.Context) (int, error) {
	if t.cold == nil {
		return 0, ErrArchiveDisabled
	}
	release, ok, err := t.store.TryLock(ctx, archiveLockName)
	if err != nil {
		return 0, fmt.Errorf("archive lock: %w", err)
	}
	if !ok {
		return 0, ErrJobRunning
	}
	defer release()

	cutoff := time.Now().UTC().Add(-t.retention)
	total, failed := 0, 0
	for {
		pkgs, err := t.store.ListPackagesDueArchive(ctx, cutoff, t.archiveBatch)
		if err != nil {
			return total, err
		}
		archived := 0
		for _, pkg := range pkgs {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			done, err := t.archiveOne(ctx, pkg, cutoff)
			if err != nil {
				failed++
				slog.ErrorContext(ctx, "archive package failed",
					"package_id", pkg.PackageID, "tracking_number", pkg.TrackingNumber, "err", err)
				continue
			}
			if done {
				archived++
			}
		}
		metrics.PackagesArchived.Add(float64(archived))
		total += archived
		// Failed packages stay due and come back next round; stop once a round makes no progress.
		if len(pkgs) < t.archiveBatch || archived == 0 {
			break
		}
	}
	slog.InfoContext(ctx, "archive job finished", "archived", total, "failed", failed, "cutoff", cutoff)
	return total, nil
}

// archiveOne archives one package under its row lock, so a concurrent ingest
// either finishes first (and its events are in the snapshot) or waits and
// then unarchives. It reports false when the package is no longer due.
func (t *Tracking) archiveOne(ctx context.Context, due domain.Package, cutoff time.Time) (bool, error) {
	done := false
	err := t.store.WithTx(ctx, func(tx store.Store) error {
		pkg, err := tx.GetPackageForUpdate(ctx, due.TenantID, due.TrackingNumber)
		if err != nil {
			return err
		}
		if pkg.IsArchived || pkg.DeliveredAt == nil || !pkg.DeliveredAt.Before(cutoff) {
			return nil
		}
		events, err := tx.ListEventsByPackage(ctx, pkg.PackageID)
		if err != nil {
			return err
		}
		key := archive.KeyForPackage(*pkg)
		if err := t.cold.Put(ctx, key, domain.ArchiveSnapshot{
			Package:    *pkg,
			Events:     events,
			ArchivedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		pkg.IsArchived = true
		pkg.ArchiveKey = key
		if err := tx.UpdatePackage(ctx, pkg); err != nil {
			return err
		}
		if err := tx.DeleteEventsByPackage(ctx, pkg.PackageID); err != nil {
			return err
		}
		done = true
		slog.InfoContext(ctx, "package archived",
			"tenant_id", pkg.TenantID,
			"package_id", pkg.PackageID,
			"tracking_number", pkg.TrackingNumber,
			"events", len(events),
			"archive_key", key,
			"backend", t.cold.Name(),
		)
		return nil
	})
	return done, err
}

// unarchive restores archived events into tx and clears the archive flags on
// pkg; the caller persists pkg.
func (t *Tracking) unarchive(ctx context.Context, tx store.Store, pkg *domain.Package) error {
	if t.cold == nil || pkg.ArchiveKey == "" {
		return fmt.Errorf("cannot unarchive: missing cold key")
	}
	snap, err := t.cold.Get(ctx, pkg.ArchiveKey)
	if err != nil {
		return err
	}
	for i := range snap.Events {
		snap.Events[i].PackageID = pkg.PackageID
	}
	if _, err := tx.InsertEvents(ctx, snap.Events); err != nil {
		return err
	}
	pkg.IsArchived = false
	pkg.ArchiveKey = ""
	slog.InfoContext(ctx, "package unarchived",
		"package_id", pkg.PackageID,
		"tracking_number", pkg.TrackingNumber,
		"restored_events", len(snap.Events),
	)
	return nil
}

// Ping checks the hot store.
func (t *Tracking) Ping(ctx context.Context) error {
	return t.store.Ping(ctx)
}
