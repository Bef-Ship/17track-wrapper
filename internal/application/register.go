package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/pkg/track17"
)

// pendingRegister is a package stored as REGISTERING that still needs the 17TRACK call.
type pendingRegister struct {
	pkg *domain.Package
	out *domain.ItemResult
}

// Register stores (or reuses) a package generation per item, then subscribes all new
// ones with 17TRACK in one batch. Like 17TRACK, a number whose current generation is
// not finished is rejected with CodeAlreadyRegistered.
func (s *Service) Register(ctx context.Context, in domain.RegisterInput) (domain.BatchResult, error) {
	if err := checkBatch(len(in.Items)); err != nil {
		return domain.BatchResult{}, err
	}
	source := sourceOrAPI(in.Source)
	results := make([]domain.ItemResult, len(in.Items))
	var pending []pendingRegister
	for i, item := range in.Items {
		pkg, out := s.prepareRegister(ctx, source, item)
		results[i] = out
		if pkg != nil {
			pending = append(pending, pendingRegister{pkg: pkg, out: &results[i]})
		}
	}
	s.registerUpstream(ctx, pending, true)
	return domain.SplitResults(results), nil
}

func checkBatch(n int) error {
	if n == 0 {
		return domain.ErrRequired("items")
	}
	if n > domain.MaxBatch {
		return domain.ErrTooMany()
	}
	return nil
}

// prepareRegister resolves one item to a REGISTERING package that needs the 17TRACK call,
// or to a final result (already registered, invalid, …) with a nil package.
func (s *Service) prepareRegister(ctx context.Context, source string, item domain.RegisterItemInput) (*domain.Package, domain.ItemResult) {
	params := item.RegisterItem
	params.Number = strings.TrimSpace(params.Number)
	params.Tag = strings.TrimSpace(params.Tag)
	tn := params.Number
	ref := strings.TrimSpace(item.ExternalRef)
	out := domain.ItemResult{Number: tn, Carrier: params.Carrier}
	if tn == "" {
		out.Reject(domain.ErrRequired("number"))
		return nil, out
	}
	if params.Carrier < 0 {
		out.Reject(domain.ErrInvalidValue("carrier"))
		return nil, out
	}

	existing, err := s.repo.GetPackageByTrackingNumber(ctx, tn)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createGeneration(ctx, nil, newPackage(source, ref, params), out)
	}
	if err != nil {
		out.Reject(err)
		return nil, out
	}
	out.PackageID = existing.PackageID
	out.Carrier = existing.Carrier
	out.SubscriptionStatus = existing.SubscriptionStatus

	if item.NewGeneration || isRecycled(existing, ref) {
		return s.createGeneration(ctx, existing, newPackage(source, ref, params), out)
	}

	// Local-only failures (auto-detect rejected / never synced) can be retried with a carrier.
	if canRetryLocalRegister(existing) {
		if params.Carrier <= 0 {
			out.RejectWith(domain.ErrorBody{
				Code:    domain.CodeCarrierUndetected,
				Message: firstNonEmpty(existing.SubStatusDesc, "Carrier cannot be detected."),
			})
			return nil, out
		}
		pkg, applied, err := s.update(ctx, byID(existing.PackageID), canRetryLocalRegister, func(p *domain.Package) {
			fillFromRegister(p, source, ref, params)
			p.SubscriptionStatus = domain.SubRegistering
			p.SubStatusDesc = ""
		})
		if err != nil {
			out.Reject(err)
			return nil, out
		}
		out.SubscriptionStatus = pkg.SubscriptionStatus
		if !applied {
			out.RejectWith(errAlreadyRegistered(tn, ""))
			return nil, out
		}
		out.Carrier = pkg.Carrier
		return pkg, out
	}
	if existing.SubscriptionStatus == domain.SubCancelled {
		out.RejectWith(errAlreadyRegistered(tn, "it is CANCELLED; pass new_generation to track it again"))
		return nil, out
	}

	// Refresh external_ref / source when re-registering from shipment.
	needsRef := func(p *domain.Package) bool { return ref != "" && p.ExternalRef == "" }
	needsSource := func(p *domain.Package) bool {
		return source == domain.SourceShipment && p.Source == domain.SourceAPI
	}
	if needsRef(existing) || needsSource(existing) {
		_, _, _ = s.update(ctx, byID(existing.PackageID),
			func(p *domain.Package) bool { return needsRef(p) || needsSource(p) },
			func(p *domain.Package) {
				if needsRef(p) {
					p.ExternalRef = ref
				}
				if needsSource(p) {
					p.Source = domain.SourceShipment
				}
			})
	}
	out.RejectWith(errAlreadyRegistered(tn, ""))
	return nil, out
}

func errAlreadyRegistered(tn, hint string) domain.ErrorBody {
	msg := fmt.Sprintf("The tracking number %s has been registered, don't need to repeat registration.", tn)
	if hint != "" {
		msg = fmt.Sprintf("The tracking number %s has been registered, %s.", tn, hint)
	}
	return domain.ErrorBody{Code: domain.CodeAlreadyRegistered, Message: msg}
}

// isRecycled reports whether registering ref against the current generation means the
// carrier reused the number: that shipment is over and it is archived or belonged to
// another external_ref.
func isRecycled(cur *domain.Package, ref string) bool {
	if !cur.IsFinished() {
		return false
	}
	return cur.IsArchived || (ref != "" && cur.ExternalRef != "" && ref != cur.ExternalRef)
}

func newPackage(source, ref string, params sdk.RegisterItem) *domain.Package {
	pkg := &domain.Package{
		TrackingNumber:        params.Number,
		Status:                domain.StatusRegistered,
		SubscriptionStatus:    domain.SubRegistering,
		ReturnDetectSupported: true,
	}
	fillFromRegister(pkg, source, ref, params)
	return pkg
}

func fillFromRegister(p *domain.Package, source, ref string, params sdk.RegisterItem) {
	p.Carrier = params.Carrier
	p.RegisterParams = params
	if ref != "" {
		p.ExternalRef = ref
	}
	if source == domain.SourceShipment || p.Source == "" {
		p.Source = source
	}
	if params.Tag != "" {
		tag := params.Tag
		p.Tag = &tag
	}
}

// createGeneration inserts pkg as the current generation of its number. With prev set,
// prev's 17TRACK registration is deleted first (otherwise 17TRACK answers "already
// registered" and keeps pushing the old shipment), then prev is superseded in the same
// transaction as the insert.
func (s *Service) createGeneration(ctx context.Context, prev, pkg *domain.Package, out domain.ItemResult) (*domain.Package, domain.ItemResult) {
	if prev != nil {
		if err := s.releaseUpstream(ctx, *prev); err != nil {
			out.RejectWith(upstreamError(err))
			return nil, out
		}
	}
	err := s.repo.WithTx(ctx, func(tx domain.Repository) error {
		if prev != nil {
			old, err := tx.GetPackageByIDForUpdate(ctx, prev.PackageID)
			if err != nil {
				return err
			}
			if old.SupersededAt != nil {
				return domain.ErrConflict
			}
			now := time.Now().UTC()
			old.SupersededAt = &now
			if old.IsActive() {
				old.SubscriptionStatus = domain.SubStopped
			}
			if err := tx.UpdatePackage(ctx, old); err != nil {
				return err
			}
		}
		return tx.CreatePackage(ctx, pkg)
	})
	if errors.Is(err, domain.ErrConflict) {
		// A concurrent register of the same number won; report its generation.
		if cur, err := s.repo.GetPackageByTrackingNumber(ctx, pkg.TrackingNumber); err == nil {
			out.PackageID, out.Carrier, out.SubscriptionStatus = cur.PackageID, cur.Carrier, cur.SubscriptionStatus
		}
		out.RejectWith(errAlreadyRegistered(pkg.TrackingNumber, ""))
		return nil, out
	}
	if err != nil {
		out.Reject(err)
		return nil, out
	}
	out.PackageID, out.Carrier, out.SubscriptionStatus = pkg.PackageID, pkg.Carrier, domain.SubRegistering
	return pkg, out
}

// releaseUpstream deletes prev's 17TRACK registration; packages that never reached
// 17TRACK need nothing.
func (s *Service) releaseUpstream(ctx context.Context, prev domain.Package) error {
	if canRetryLocalRegister(&prev) {
		return nil
	}
	client, err := s.client()
	if err != nil {
		return err
	}
	res, err := client.DeleteTrack(ctx, []sdk.TrackNumber{trackRef(prev)})
	if err != nil {
		return err
	}
	if res != nil && len(res.Rejected) > 0 {
		if r := res.Rejected[0]; r.Error != nil && r.Error.Code != sdk.ErrCodeNotRegistered {
			return domain.Errorf(domain.ErrConflict, r.Error.Code, "release previous generation: %s", rejectMessage(r))
		}
	}
	return nil
}

// registerUpstream calls 17TRACK /register for pending packages and records each outcome.
// allowEnqueue=false is the worker path: retryable errors keep REGISTERING for asynq.
func (s *Service) registerUpstream(ctx context.Context, pending []pendingRegister, allowEnqueue bool) {
	if len(pending) == 0 {
		return
	}
	client, err := s.client()
	if err != nil {
		for _, p := range pending {
			s.failRegister(ctx, p, domain.SubClosed, "", domain.Body(err))
		}
		return
	}
	for i := 0; i < len(pending); i += sdkBatchSize {
		s.registerChunk(ctx, client, pending[i:min(i+sdkBatchSize, len(pending))], allowEnqueue)
	}
}

func (s *Service) registerChunk(ctx context.Context, client track17.Client, chunk []pendingRegister, allowEnqueue bool) {
	items := make([]sdk.RegisterItem, len(chunk))
	for i, p := range chunk {
		items[i] = registerItem(*p.pkg)
	}
	res, err := client.Register(ctx, items)
	if err != nil {
		retryable := track17.IsRetryable(err)
		for _, p := range chunk {
			switch {
			case retryable && !allowEnqueue:
				p.out.SubscriptionStatus = domain.SubRegistering
				p.out.RejectWith(upstreamError(err))
			case retryable && s.enqueueRegister(ctx, *p.pkg):
				// Accepted locally; the register queue finishes the 17TRACK call.
				p.out.SubscriptionStatus = domain.SubRegistering
			default:
				s.failRegister(ctx, p, domain.SubClosed, "", upstreamError(err))
			}
		}
		return
	}
	accepted := map[string]sdk.RegisterAccepted{}
	rejected := map[string]sdk.RejectedItem{}
	if res != nil {
		for _, a := range res.Accepted {
			accepted[numberKey(a.Number)] = a
		}
		for _, r := range res.Rejected {
			rejected[numberKey(r.Number)] = r
		}
	}
	for _, p := range chunk {
		key := numberKey(p.pkg.TrackingNumber)
		resolved := 0
		if rej, ok := rejected[key]; ok {
			if !alreadyRegistered(rej) {
				body := rejectBody(rej)
				// Auto-detect (carrier=0) often fails for ambiguous numbers (e.g. 052…) — keep a
				// retryable local state so the caller can pick a concrete carrier and re-submit.
				if needsCarrierRejection(rej) || (p.pkg.Carrier == 0 && !isInvalidNumberRejection(rej)) {
					s.failRegister(ctx, p, domain.SubNeedsCarrier, body.Message, body)
				} else {
					s.failRegister(ctx, p, domain.SubClosed, body.Message, body)
				}
				continue
			}
			resolved = rej.Carrier
		} else if acc, ok := accepted[key]; ok {
			resolved = acc.Carrier
		} else {
			// No verdict for this number: stays REGISTERING for RetryRegistering.
			p.out.SubscriptionStatus = domain.SubRegistering
			continue
		}
		if err := s.saveRegisterOutcome(ctx, p.pkg, func(pk *domain.Package) {
			applyResolvedCarrier(pk, resolved)
			pk.SubscriptionStatus = domain.SubTracking
		}); err != nil {
			p.out.Reject(err)
			continue
		}
		p.out.SubscriptionStatus, p.out.Carrier = p.pkg.SubscriptionStatus, p.pkg.Carrier
	}
}

func (s *Service) failRegister(ctx context.Context, p pendingRegister, status, desc string, body domain.ErrorBody) {
	p.out.SubscriptionStatus = status
	p.out.RejectWith(body)
	err := s.saveRegisterOutcome(ctx, p.pkg, func(pk *domain.Package) {
		pk.SubscriptionStatus = status
		if desc != "" {
			pk.SubStatusDesc = desc
		}
	})
	if err != nil {
		return
	}
	p.out.SubscriptionStatus = p.pkg.SubscriptionStatus
	if p.pkg.SubscriptionStatus == domain.SubTracking {
		// A webhook confirmed the registration meanwhile.
		p.out.Error = nil
	}
}

// saveRegisterOutcome applies a register result under row lock, only while the package
// is still REGISTERING: a webhook may already have moved it to TRACKING meanwhile.
// pkg is refreshed to the stored row.
func (s *Service) saveRegisterOutcome(ctx context.Context, pkg *domain.Package, apply func(*domain.Package)) error {
	fresh, _, err := s.update(ctx, byID(pkg.PackageID),
		func(p *domain.Package) bool { return p.SubscriptionStatus == domain.SubRegistering },
		apply)
	if err != nil {
		return err
	}
	*pkg = *fresh
	return nil
}

// finishRegister calls 17TRACK for one package already stored as REGISTERING.
func (s *Service) finishRegister(ctx context.Context, pkg *domain.Package, allowEnqueue bool) domain.ItemResult {
	out := domain.ItemResult{
		Number:             pkg.TrackingNumber,
		Carrier:            pkg.Carrier,
		PackageID:          pkg.PackageID,
		SubscriptionStatus: domain.SubRegistering,
	}
	s.registerUpstream(ctx, []pendingRegister{{pkg: pkg, out: &out}}, allowEnqueue)
	return out
}

func (s *Service) enqueueRegister(ctx context.Context, pkg domain.Package) bool {
	if s.registerQ == nil {
		return false
	}
	job := domain.RegisterJob{PackageID: pkg.PackageID, Item: registerItem(pkg)}
	return s.registerQ.EnqueueRegister(ctx, job) == nil
}

// ProcessRegisterJob retries a deferred 17TRACK register (worker).
// Returns a retryable error when asynq should retry the task.
func (s *Service) ProcessRegisterJob(ctx context.Context, job domain.RegisterJob) error {
	var (
		pkg *domain.Package
		err error
	)
	if job.PackageID > 0 {
		pkg, err = s.repo.GetPackageByID(ctx, job.PackageID)
	} else {
		pkg, err = s.repo.GetPackageByTrackingNumber(ctx, strings.TrimSpace(job.Item.Number))
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if pkg.SubscriptionStatus != domain.SubRegistering || pkg.SupersededAt != nil {
		return nil
	}
	if res := s.finishRegister(ctx, pkg, false); res.SubscriptionStatus == domain.SubRegistering {
		msg := "no result from 17TRACK"
		if res.Error != nil {
			msg = res.Error.Message
		}
		return track17.WrapRetryable(fmt.Errorf("%s", msg))
	}
	return nil
}

// Cancel stops SDK polling for the current generation of each number (17TRACK /stoptrack).
func (s *Service) Cancel(ctx context.Context, in domain.CancelInput) (domain.BatchResult, error) {
	if err := checkBatch(len(in.Items)); err != nil {
		return domain.BatchResult{}, err
	}
	results := make([]domain.ItemResult, len(in.Items))
	for i, item := range in.Items {
		results[i] = s.cancelOne(ctx, item)
	}
	return domain.SplitResults(results), nil
}

func (s *Service) cancelOne(ctx context.Context, item sdk.TrackNumber) domain.ItemResult {
	tn := strings.TrimSpace(item.Number)
	out := domain.ItemResult{Number: tn, Carrier: item.Carrier}
	if tn == "" {
		out.Reject(domain.ErrRequired("number"))
		return out
	}
	var prev string
	now := time.Now().UTC()
	pkg, applied, err := s.update(ctx, byNumber(tn),
		func(p *domain.Package) bool {
			prev = p.SubscriptionStatus
			return prev == domain.SubTracking || prev == domain.SubStopped
		},
		func(p *domain.Package) {
			p.SubscriptionStatus = domain.SubCancelled
			p.CancelledAt = &now
		})
	if err != nil {
		out.Reject(notRegistered(tn, err))
		return out
	}
	out.PackageID, out.Carrier, out.SubscriptionStatus = pkg.PackageID, pkg.Carrier, pkg.SubscriptionStatus
	if !applied {
		if prev != domain.SubCancelled {
			out.RejectWith(domain.ErrorBody{
				Code:    domain.CodeStopNotAllowed,
				Message: "Only tracking numbers that are being tracked can be stopped.",
			})
		}
		return out
	}
	if prev == domain.SubTracking {
		s.stopUpstream(ctx, []domain.Package{*pkg})
	}
	_ = s.notify(ctx, Update{Package: *pkg})
	return out
}

// ChangeCarrier updates the 17TRACK carrier of current generations (17TRACK /changecarrier).
func (s *Service) ChangeCarrier(ctx context.Context, in domain.ChangeCarrierInput) (domain.BatchResult, error) {
	if err := checkBatch(len(in.Items)); err != nil {
		return domain.BatchResult{}, err
	}
	results := make([]domain.ItemResult, len(in.Items))
	for i, item := range in.Items {
		results[i] = s.changeCarrierOne(ctx, item)
	}
	return domain.SplitResults(results), nil
}

func (s *Service) changeCarrierOne(ctx context.Context, item domain.ChangeCarrierItemInput) domain.ItemResult {
	tn := strings.TrimSpace(item.Number)
	out := domain.ItemResult{Number: tn, Carrier: item.CarrierNew}
	if tn == "" {
		out.Reject(domain.ErrRequired("number"))
		return out
	}
	if item.CarrierNew <= 0 {
		out.Reject(domain.ErrInvalidValue("carrier_new"))
		return out
	}

	pkg, err := s.repo.GetPackageByTrackingNumber(ctx, tn)
	if err != nil {
		out.Reject(notRegistered(tn, err))
		return out
	}
	out.PackageID = pkg.PackageID
	out.SubscriptionStatus = pkg.SubscriptionStatus

	// Never synced to 17TRACK (auto-detect failed) → re-register with the concrete carrier.
	if canRetryLocalRegister(pkg) {
		locked, applied, err := s.update(ctx, byID(pkg.PackageID), canRetryLocalRegister, func(p *domain.Package) {
			p.Carrier = item.CarrierNew
			p.RegisterParams.Carrier = item.CarrierNew
			if item.FinalCarrierNew > 0 {
				p.RegisterParams.FinalCarrier = item.FinalCarrierNew
			}
			p.SubscriptionStatus = domain.SubRegistering
			p.SubStatusDesc = ""
		})
		if err != nil {
			out.Reject(err)
			return out
		}
		if !applied {
			out.SubscriptionStatus = locked.SubscriptionStatus
			out.Reject(errState("subscription changed concurrently; retry"))
			return out
		}
		reg := s.finishRegister(ctx, locked, true)
		if reg.SubscriptionStatus == domain.SubTracking {
			_ = s.notify(ctx, Update{Package: *locked})
		}
		return reg
	}

	if pkg.SubscriptionStatus != domain.SubTracking {
		out.Reject(errState("cannot change carrier while subscription is " + pkg.SubscriptionStatus))
		return out
	}
	if pkg.Carrier == item.CarrierNew && item.FinalCarrierNew == 0 {
		return out
	}

	client, err := s.client()
	if err != nil {
		out.Reject(err)
		return out
	}
	req := item
	req.Number = tn
	if req.CarrierOld == 0 {
		req.CarrierOld = pkg.Carrier
	}
	if req.FinalCarrierNew > 0 && req.FinalCarrierOld == 0 {
		req.FinalCarrierOld = pkg.RegisterParams.FinalCarrier
	}
	res, err := client.ChangeCarrier(ctx, []sdk.ChangeCarrierItem{req})
	if err != nil {
		out.RejectWith(upstreamError(err))
		return out
	}
	if res != nil && len(res.Rejected) > 0 {
		out.RejectWith(rejectBody(res.Rejected[0]))
		return out
	}

	pkg, _, err = s.update(ctx, byID(pkg.PackageID), nil, func(p *domain.Package) {
		p.Carrier = item.CarrierNew
		p.RegisterParams.Carrier = item.CarrierNew
		if item.FinalCarrierNew > 0 {
			p.RegisterParams.FinalCarrier = item.FinalCarrierNew
		}
	})
	if err != nil {
		out.Reject(err)
		return out
	}
	_ = s.notify(ctx, Update{Package: *pkg})
	out.SubscriptionStatus = domain.SubTracking
	return out
}

func errState(msg string) error {
	return domain.Errorf(domain.ErrConflict, domain.CodeStateConflict, "%s", msg)
}

// notRegistered turns a plain ErrNotFound for tn into 17TRACK's "not registered".
func notRegistered(tn string, err error) error {
	var ce *domain.CodedError
	if errors.Is(err, domain.ErrNotFound) && !errors.As(err, &ce) {
		return domain.ErrNotRegistered(tn)
	}
	return err
}

// upstreamError maps a 17TRACK call failure for API callers. Item-level 17TRACK codes
// (-18019xxx) pass through; request-level ones (bad key, quota, …) concern our 17TRACK
// account, not the caller, so they surface as an internal error.
func upstreamError(err error) domain.ErrorBody {
	var ce *domain.CodedError
	if errors.As(err, &ce) {
		return ce.ErrorBody
	}
	var api *sdk.APIError
	if errors.As(err, &api) && isItemCode(api.Code) {
		return domain.ErrorBody{Code: api.Code, Message: firstNonEmpty(api.Message, err.Error())}
	}
	var item *sdk.ItemError
	if errors.As(err, &item) && item.Code != 0 {
		return domain.ErrorBody{Code: item.Code, Message: firstNonEmpty(item.Message, err.Error())}
	}
	return domain.ErrorBody{Code: domain.CodeInternal, Message: "17TRACK unavailable: " + err.Error()}
}

func isItemCode(code int) bool { return code <= -18019000 && code > -18020000 }

func rejectBody(r sdk.RejectedItem) domain.ErrorBody {
	if r.Error != nil && r.Error.Code != 0 {
		return domain.ErrorBody{Code: r.Error.Code, Message: rejectMessage(r)}
	}
	return domain.ErrorBody{Code: domain.CodeDataInvalid, Message: rejectMessage(r)}
}

// registerItem is the 17TRACK /register body for pkg: the stored request with the
// package's current number and carrier.
func registerItem(pkg domain.Package) sdk.RegisterItem {
	item := pkg.RegisterParams
	item.Number = pkg.TrackingNumber
	item.Carrier = pkg.Carrier
	return item
}

func trackRef(pkg domain.Package) sdk.TrackNumber {
	return sdk.TrackNumber{Number: pkg.TrackingNumber, Carrier: pkg.Carrier}
}

func numberKey(n string) string { return strings.ToUpper(strings.TrimSpace(n)) }

func rejectMessage(r sdk.RejectedItem) string {
	if r.Error != nil && r.Error.Message != "" {
		return r.Error.Message
	}
	return "sdk rejected"
}

func sourceOrAPI(source string) string {
	if strings.TrimSpace(source) == domain.SourceShipment {
		return domain.SourceShipment
	}
	return domain.SourceAPI
}

func alreadyRegistered(r sdk.RejectedItem) bool {
	if r.Error == nil {
		return false
	}
	return r.Error.Code == domain.CodeAlreadyRegistered ||
		strings.Contains(strings.ToLower(r.Error.Message), "already")
}

// canRetryLocalRegister reports packages that exist only locally (never accepted by 17TRACK).
func canRetryLocalRegister(pkg *domain.Package) bool {
	if pkg == nil {
		return false
	}
	switch pkg.SubscriptionStatus {
	case domain.SubNeedsCarrier, domain.SubClosed:
		return true
	default:
		return false
	}
}

func needsCarrierRejection(r sdk.RejectedItem) bool {
	if r.Error == nil {
		return false
	}
	if r.Error.Code == domain.CodeCarrierUndetected {
		return true
	}
	msg := strings.ToLower(r.Error.Message)
	if !strings.Contains(msg, "carrier") {
		return false
	}
	return strings.Contains(msg, "unrecognized") ||
		strings.Contains(msg, "not recognized") ||
		strings.Contains(msg, "cannot identify") ||
		strings.Contains(msg, "cannot detect") ||
		strings.Contains(msg, "ambiguous") ||
		strings.Contains(msg, "multiple") ||
		strings.Contains(msg, "specify")
}

func isInvalidNumberRejection(r sdk.RejectedItem) bool {
	if r.Error == nil {
		return false
	}
	if r.Error.Code == sdk.ErrCodeInvalidNumberFormat {
		return true
	}
	msg := strings.ToLower(r.Error.Message)
	return strings.Contains(msg, "invalid number") ||
		strings.Contains(msg, "number format") ||
		strings.Contains(msg, "illegal number")
}
