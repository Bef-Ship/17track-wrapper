package application

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"testing"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/internal/infrastructure"
	"github.com/SolaTyolo/17track-wrapper/pkg/track17"
)

type memRepo struct {
	pkgs      map[int64]*domain.Package
	events    map[int64][]domain.TrackingEvent
	next      int64
	nextEvent int64
}

func newMemRepo() *memRepo {
	return &memRepo{pkgs: map[int64]*domain.Package{}, events: map[int64][]domain.TrackingEvent{}}
}

// WithTx rolls back package and event writes when fn fails.
func (r *memRepo) WithTx(_ context.Context, fn func(domain.Repository) error) error {
	pkgs := make(map[int64]domain.Package, len(r.pkgs))
	for id, p := range r.pkgs {
		pkgs[id] = *p
	}
	events := maps.Clone(r.events)
	if err := fn(r); err != nil {
		r.pkgs = map[int64]*domain.Package{}
		for id, p := range pkgs {
			r.pkgs[id] = &p
		}
		r.events = events
		return err
	}
	return nil
}

func (r *memRepo) current(tn string) *domain.Package {
	for _, p := range r.pkgs {
		if p.TrackingNumber == tn && p.SupersededAt == nil {
			return p
		}
	}
	return nil
}

func (r *memRepo) CreatePackage(_ context.Context, pkg *domain.Package) error {
	if r.current(pkg.TrackingNumber) != nil {
		return domain.ErrConflict
	}
	r.next++
	pkg.PackageID = r.next
	pkg.CreatedAt = time.Now()
	pkg.UpdatedAt = pkg.CreatedAt
	cp := *pkg
	r.pkgs[pkg.PackageID] = &cp
	return nil
}

func (r *memRepo) GetPackageByID(_ context.Context, id int64) (*domain.Package, error) {
	p, ok := r.pkgs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *memRepo) GetPackageByIDForUpdate(ctx context.Context, id int64) (*domain.Package, error) {
	return r.GetPackageByID(ctx, id)
}

func (r *memRepo) GetPackageByTrackingNumber(_ context.Context, tn string) (*domain.Package, error) {
	p := r.current(tn)
	if p == nil {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *memRepo) GetPackageForUpdate(ctx context.Context, tn string) (*domain.Package, error) {
	return r.GetPackageByTrackingNumber(ctx, tn)
}

func (r *memRepo) UpdatePackage(_ context.Context, pkg *domain.Package) error {
	if _, ok := r.pkgs[pkg.PackageID]; !ok {
		return domain.ErrNotFound
	}
	pkg.UpdatedAt = time.Now()
	cp := *pkg
	r.pkgs[pkg.PackageID] = &cp
	return nil
}

func (r *memRepo) ListPackages(context.Context, domain.ListPackagesFilter) ([]domain.Package, error) {
	return nil, nil
}

func (r *memRepo) ListPackageGenerations(_ context.Context, tn string) ([]domain.Package, error) {
	out := r.page(0, len(r.pkgs), func(p *domain.Package) bool { return p.TrackingNumber == tn })
	sort.Slice(out, func(i, j int) bool { return out[i].PackageID > out[j].PackageID })
	return out, nil
}

func (r *memRepo) ListPackagesDueArchive(_ context.Context, before time.Time, limit int) ([]domain.Package, error) {
	return r.page(0, limit, func(p *domain.Package) bool {
		return !p.IsArchived && !p.IsActive() && p.UpdatedAt.Before(before)
	}), nil
}

func (r *memRepo) page(afterID int64, limit int, keep func(*domain.Package) bool) []domain.Package {
	ids := make([]int64, 0)
	for id, p := range r.pkgs {
		if id > afterID && keep(p) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]domain.Package, 0, len(ids))
	for _, id := range ids {
		out = append(out, *r.pkgs[id])
	}
	return out
}

func (r *memRepo) ListPackagesBySubscriptionStatus(_ context.Context, status string, afterID int64, limit int) ([]domain.Package, error) {
	return r.page(afterID, limit, func(p *domain.Package) bool {
		return p.SubscriptionStatus == status && p.SupersededAt == nil
	}), nil
}

func (r *memRepo) ListTrackingNotHeardSince(_ context.Context, since time.Time, afterID int64, limit int) ([]domain.Package, error) {
	return r.page(afterID, limit, func(p *domain.Package) bool {
		return p.SubscriptionStatus == domain.SubTracking && p.SupersededAt == nil &&
			(p.LastCallbackAt == nil || p.LastCallbackAt.Before(since))
	}), nil
}

func (r *memRepo) InsertEvents(_ context.Context, events []domain.TrackingEvent) ([]domain.TrackingEvent, error) {
	var inserted []domain.TrackingEvent
	for _, ev := range events {
		dup := false
		for _, have := range r.events[ev.PackageID] {
			dup = dup || have.EventFingerprint == ev.EventFingerprint
		}
		if dup {
			continue
		}
		r.nextEvent++
		ev.EventID = r.nextEvent
		r.events[ev.PackageID] = append(r.events[ev.PackageID], ev)
		inserted = append(inserted, ev)
	}
	return inserted, nil
}

func (r *memRepo) ListEventsByPackage(_ context.Context, id int64) ([]domain.TrackingEvent, error) {
	out := append([]domain.TrackingEvent(nil), r.events[id]...)
	sort.Slice(out, func(i, j int) bool { return out[i].EventTime.Before(out[j].EventTime) })
	return out, nil
}

func (r *memRepo) DeleteEventsByPackage(_ context.Context, id int64) error {
	delete(r.events, id)
	return nil
}

func (r *memRepo) TryLock(context.Context, string) (func(), bool, error) {
	return func() {}, true, nil
}

type fakeSDK struct {
	registerRejected *sdk.RejectedItem
	registerCalls    [][]sdk.RegisterItem
	stopped          []string
	retracked        []string
	deleted          []sdk.TrackNumber
	polled           []string
	changed          []sdk.ChangeCarrierItem
	// onRetrack runs mid-call, e.g. to simulate a webhook landing during the request.
	onRetrack func()
}

func (f *fakeSDK) Token() string { return "t" }

func (f *fakeSDK) Register(_ context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error) {
	f.registerCalls = append(f.registerCalls, items)
	out := &sdk.AcceptedRejected[sdk.RegisterAccepted]{}
	if f.registerRejected != nil {
		out.Rejected = []sdk.RejectedItem{*f.registerRejected}
		return out, nil
	}
	for _, it := range items {
		out.Accepted = append(out.Accepted, sdk.RegisterAccepted{Number: it.Number, Carrier: it.Carrier})
	}
	return out, nil
}

func (f *fakeSDK) StopTrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	for _, it := range items {
		f.stopped = append(f.stopped, it.Number)
	}
	return &sdk.AcceptedRejected[sdk.SimpleAccepted]{}, nil
}

func (f *fakeSDK) Retrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	if f.onRetrack != nil {
		f.onRetrack()
	}
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	for _, it := range items {
		f.retracked = append(f.retracked, it.Number)
		out.Accepted = append(out.Accepted, sdk.SimpleAccepted(it))
	}
	return out, nil
}

func (f *fakeSDK) DeleteTrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	f.deleted = append(f.deleted, items...)
	return &sdk.AcceptedRejected[sdk.SimpleAccepted]{}, nil
}

func (f *fakeSDK) ChangeCarrier(_ context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	for _, it := range items {
		f.changed = append(f.changed, it)
		out.Accepted = append(out.Accepted, sdk.SimpleAccepted{Number: it.Number, Carrier: it.CarrierNew})
	}
	return out, nil
}

func (f *fakeSDK) GetTrackInfo(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error) {
	out := &sdk.AcceptedRejected[sdk.TrackDetail]{}
	for _, it := range items {
		f.polled = append(f.polled, it.Number)
		out.Accepted = append(out.Accepted, sdk.TrackDetail{Number: it.Number, Carrier: it.Carrier})
	}
	return out, nil
}

type recObserver struct{ updates []Update }

func (o *recObserver) OnTrackingUpdate(_ context.Context, u Update) error {
	o.updates = append(o.updates, u)
	return nil
}

func newTestService(t *testing.T) (*Service, *memRepo, *fakeSDK, *recObserver) {
	t.Helper()
	repo, fs, obs := newMemRepo(), &fakeSDK{}, &recObserver{}
	svc := NewService(repo, fs, nil, nil, Options{})
	svc.AddObserver(obs)
	return svc, repo, fs, obs
}

func item(tn string, carrier int) domain.RegisterItemInput {
	return domain.RegisterItemInput{RegisterItem: sdk.RegisterItem{Number: tn, Carrier: carrier}}
}

func registerOne(t *testing.T, svc *Service, tn string) int64 {
	t.Helper()
	return registerFrom(t, svc, tn, domain.SourceShipment)
}

func registerFrom(t *testing.T, svc *Service, tn, source string) int64 {
	t.Helper()
	res, err := svc.Register(context.Background(), domain.RegisterInput{
		Source: source,
		Items:  []domain.RegisterItemInput{item(tn, 100002)},
	})
	r := only(t, res, err)
	if r.Error != nil && r.Error.Code == domain.CodeAlreadyRegistered {
		return r.PackageID
	}
	if r.Error != nil || r.SubscriptionStatus != domain.SubTracking {
		t.Fatalf("register %s: %+v", tn, r)
	}
	return r.PackageID
}

func TestRegisterAlreadyRegisteredIsSuccess(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	fs.registerRejected = &sdk.RejectedItem{Number: "TN1", Error: &sdk.ErrorBody{Code: domain.CodeAlreadyRegistered, Message: "has been registered"}}
	id := registerOne(t, svc, "TN1")
	if got := repo.pkgs[id]; got.Source != domain.SourceShipment || got.SubscriptionStatus != domain.SubTracking {
		t.Fatalf("pkg = %+v", got)
	}
}

func TestApplyWebhookStoppedMarksPackageAndNotifies(t *testing.T) {
	svc, repo, _, obs := newTestService(t)
	id := registerOne(t, svc, "TN1")

	raw, _ := json.Marshal(map[string]any{"number": "TN1", "track_info": map[string]any{
		"latest_status": map[string]any{"status": "Delivered"},
	}})
	if err := svc.ApplyWebhook(context.Background(), domain.WebhookApplyInput{
		TrackingNo: "TN1", Raw: raw, Event: domain.EventTrackingStopped, CallbackSeq: "s1",
	}); err != nil {
		t.Fatal(err)
	}
	if st := repo.pkgs[id].SubscriptionStatus; st != domain.SubStopped {
		t.Fatalf("status = %s", st)
	}
	if len(obs.updates) != 1 || !obs.updates[0].Pushed ||
		obs.updates[0].Package.SubscriptionStatus != domain.SubStopped {
		t.Fatalf("updates = %+v", obs.updates)
	}
}

func TestApplyWebhookUnknownNumberIsIgnored(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if err := svc.ApplyWebhook(context.Background(), domain.WebhookApplyInput{TrackingNo: "nope", Raw: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyWebhookUpdatesResolvedCarrier(t *testing.T) {
	svc, repo, _, _ := newTestService(t)
	res, err := svc.Register(context.Background(), domain.RegisterInput{
		Items: []domain.RegisterItemInput{item("TN-AUTO", 0)},
	})
	r := only(t, res, err)
	if r.Error != nil || r.SubscriptionStatus != domain.SubTracking {
		t.Fatalf("register auto: %+v", r)
	}
	id := r.PackageID
	if got := repo.pkgs[id].Carrier; got != 0 {
		t.Fatalf("carrier before webhook = %d", got)
	}

	raw, _ := json.Marshal(map[string]any{
		"number":  "TN-AUTO",
		"carrier": 100001,
		"track_info": map[string]any{
			"latest_status": map[string]any{"status": "InTransit", "sub_status": "InTransit_Other"},
		},
	})
	if err := svc.ApplyWebhook(context.Background(), domain.WebhookApplyInput{
		TrackingNo: "TN-AUTO", Raw: raw, Event: domain.EventTrackingUpdated, CallbackSeq: "c1",
	}); err != nil {
		t.Fatal(err)
	}
	if got := repo.pkgs[id].Carrier; got != 100001 {
		t.Fatalf("carrier = %d", got)
	}
}

func TestStopStaleAndRetrack(t *testing.T) {
	svc, repo, fs, obs := newTestService(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)

	idle := registerOne(t, svc, "IDLE")
	fresh := registerOne(t, svc, "FRESH")
	delivered := registerOne(t, svc, "DELIVERED")
	for id, at := range map[int64]time.Time{idle: old, fresh: recent, delivered: old} {
		p := repo.pkgs[id]
		p.LatestEventTime = &at
		if id == delivered {
			p.Status = domain.StatusDelivered
		}
	}

	n, err := svc.StopStale(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("StopStale = %d, %v", n, err)
	}
	if repo.pkgs[fresh].SubscriptionStatus != domain.SubTracking || repo.pkgs[idle].SubscriptionStatus != domain.SubStopped {
		t.Fatalf("pkgs = %+v %+v", repo.pkgs[fresh], repo.pkgs[idle])
	}
	if len(fs.stopped) != 2 || len(obs.updates) != 2 {
		t.Fatalf("stopped=%v updates=%d", fs.stopped, len(obs.updates))
	}

	n, err = svc.RetrackStopped(context.Background())
	if err != nil || n != 1 || len(fs.retracked) != 1 || fs.retracked[0] != "IDLE" {
		t.Fatalf("RetrackStopped = %d, %v, %v", n, err, fs.retracked)
	}
	if got := repo.pkgs[idle]; got.SubscriptionStatus != domain.SubTracking || !got.IsRetracked {
		t.Fatalf("idle = %+v", got)
	}

	repo.pkgs[idle].SubscriptionStatus = domain.SubStopped
	if n, _ := svc.RetrackStopped(context.Background()); n != 0 {
		t.Fatalf("second retrack = %d", n)
	}
}

func TestChangeCarrierSingleAndBatch(t *testing.T) {
	svc, repo, fs, obs := newTestService(t)
	id1 := registerOne(t, svc, "TN-CC-1")
	id2 := registerOne(t, svc, "TN-CC-2")

	res, err := svc.ChangeCarrier(context.Background(), domain.ChangeCarrierInput{
		Items: []domain.ChangeCarrierItemInput{
			{Number: "TN-CC-1", CarrierNew: 100003},
			{Number: "TN-CC-2", CarrierNew: 21051},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accepted) != 2 || len(res.Rejected) != 0 {
		t.Fatalf("results = %+v", res)
	}
	if len(fs.changed) != 2 || fs.changed[0].CarrierOld != 100002 {
		t.Fatalf("sdk calls = %+v", fs.changed)
	}
	if got := repo.pkgs[id1]; got.Carrier != 100003 || got.RegisterParams.Carrier != 100003 {
		t.Fatalf("pkg1 = %+v", got)
	}
	if got := repo.pkgs[id2]; got.Carrier != 21051 {
		t.Fatalf("pkg2 = %+v", got)
	}
	if len(obs.updates) < 2 {
		t.Fatalf("expected observer updates, got %d", len(obs.updates))
	}

	again, err := svc.ChangeCarrier(context.Background(), domain.ChangeCarrierInput{
		Items: []domain.ChangeCarrierItemInput{{Number: "TN-CC-1", CarrierNew: 100003}},
	})
	if r := only(t, again, err); r.Error != nil || len(fs.changed) != 2 {
		t.Fatalf("unchanged = %+v calls=%d", r, len(fs.changed))
	}

	bad, err := svc.ChangeCarrier(context.Background(), domain.ChangeCarrierInput{
		Items: []domain.ChangeCarrierItemInput{{Number: "NOPE", CarrierNew: 100003}},
	})
	if r := only(t, bad, err); r.Error == nil || r.Error.Code != domain.CodeNotRegistered {
		t.Fatalf("unknown number = %+v", r)
	}
}

func TestAutoRegisterCarrierRejectionBecomesNeedsCarrier(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	fs.registerRejected = &sdk.RejectedItem{
		Number: "05217983598650",
		Error:  &sdk.ErrorBody{Code: domain.CodeCarrierUndetected, Message: "carrier unrecognized"},
	}
	res, err := svc.Register(context.Background(), domain.RegisterInput{
		Items: []domain.RegisterItemInput{item("05217983598650", 0)},
	})
	r := only(t, res, err)
	if r.SubscriptionStatus != domain.SubNeedsCarrier || r.Error == nil || r.Error.Code != domain.CodeCarrierUndetected {
		t.Fatalf("result = %+v", r)
	}
	pkg := repo.pkgs[r.PackageID]
	if pkg.SubscriptionStatus != domain.SubNeedsCarrier || pkg.SubStatusDesc == "" {
		t.Fatalf("pkg = %+v", pkg)
	}

	// A concrete carrier re-registers locally (never synced to 17TRACK).
	fs.registerRejected = nil
	out, err := svc.ChangeCarrier(context.Background(), domain.ChangeCarrierInput{
		Items: []domain.ChangeCarrierItemInput{{Number: "05217983598650", CarrierNew: 100001}},
	})
	if o := only(t, out, err); o.Error != nil || o.SubscriptionStatus != domain.SubTracking {
		t.Fatalf("set carrier = %+v", o)
	}
	got := repo.pkgs[r.PackageID]
	if got.SubscriptionStatus != domain.SubTracking || got.Carrier != 100001 {
		t.Fatalf("after set = %+v", got)
	}
	if len(fs.changed) != 0 {
		t.Fatalf("expected re-register not changecarrier, got %+v", fs.changed)
	}
}

func TestSameTrackingNumberIsShared(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	shipID := registerFrom(t, svc, "TN1", domain.SourceShipment)
	apiID := registerFrom(t, svc, "TN1", domain.SourceAPI)
	if shipID != apiID {
		t.Fatalf("expected shared package, ship=%d api=%d", shipID, apiID)
	}
	ctx := context.Background()
	if _, err := svc.Refresh(ctx, "TN1"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	cancel := func() domain.ItemResult {
		res, err := svc.Cancel(ctx, domain.CancelInput{Items: []sdk.TrackNumber{{Number: "TN1"}}})
		return only(t, res, err)
	}
	if r := cancel(); r.Error != nil {
		t.Fatalf("cancel = %+v", r)
	}
	if r := cancel(); r.Error != nil || r.SubscriptionStatus != domain.SubCancelled {
		t.Fatalf("repeat cancel should be idempotent: %+v", r)
	}
	if repo.pkgs[shipID].SubscriptionStatus != domain.SubCancelled {
		t.Fatalf("cancel status=%s", repo.pkgs[shipID].SubscriptionStatus)
	}
	if len(fs.stopped) != 1 {
		t.Fatalf("expected upstream stop, stopped=%v", fs.stopped)
	}
}

type failingObserver struct{ fails int }

func (o *failingObserver) OnTrackingUpdate(context.Context, Update) error {
	if o.fails > 0 {
		o.fails--
		return errors.New("callback down")
	}
	return nil
}

func TestApplyWebhookDedupesAndRetriesAfterFailure(t *testing.T) {
	repo, fs := newMemRepo(), &fakeSDK{}
	svc := NewService(repo, fs, infrastructure.NewMemoryStore(), nil, Options{})
	id := registerOne(t, svc, "TN1")
	failing := &failingObserver{fails: 1}
	rec := &recObserver{}
	svc.AddObserver(failing)
	svc.AddObserver(rec)

	raw, _ := json.Marshal(map[string]any{"number": "TN1", "track_info": map[string]any{
		"latest_status": map[string]any{"status": "InTransit"},
	}})
	in := domain.WebhookApplyInput{TrackingNo: "TN1", Raw: raw, Event: domain.EventTrackingUpdated}
	ctx := context.Background()

	if err := svc.ApplyWebhook(ctx, in); err == nil {
		t.Fatal("expected observer error to nack the webhook")
	}
	if st := repo.pkgs[id].Status; st == domain.StatusInTransit {
		t.Fatal("failed delivery must roll the ingest back so the retry notifies again")
	}
	if err := svc.ApplyWebhook(ctx, in); err != nil {
		t.Fatalf("retry after release: %v", err)
	}
	if err := svc.ApplyWebhook(ctx, in); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if len(rec.updates) != 2 {
		t.Fatalf("updates = %d, want 2 (failed attempt + retry, duplicate dropped)", len(rec.updates))
	}
}

func TestSyncActiveSkipsRecentlyHeard(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	quiet := registerOne(t, svc, "QUIET")
	heard := registerOne(t, svc, "HEARD")
	recent := time.Now().Add(-time.Hour)
	repo.pkgs[heard].LastCallbackAt = &recent

	n, err := svc.SyncActive(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SyncActive = %d, %v", n, err)
	}
	if len(fs.polled) != 1 || fs.polled[0] != "QUIET" {
		t.Fatalf("polled = %v", fs.polled)
	}
	if repo.pkgs[quiet].LastCallbackAt == nil {
		t.Fatal("poll should stamp last_callback_at")
	}
}

func TestStopStaleContinuesWhenObserverFails(t *testing.T) {
	repo, fs := newMemRepo(), &fakeSDK{}
	svc := NewService(repo, fs, nil, nil, Options{})
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, tn := range []string{"A", "B"} {
		id := registerOne(t, svc, tn)
		repo.pkgs[id].LatestEventTime = &old
	}
	svc.AddObserver(&failingObserver{fails: 1})

	n, err := svc.StopStale(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("StopStale = %d, %v", n, err)
	}
}

func TestUntimedEventFingerprintIsStable(t *testing.T) {
	untimed := track17.ParsedTrack{Events: []track17.Event{{Description: "no time", Location: "Hub"}}}
	a := batchFromParsed("TN1", untimed).Events[0].EventFingerprint
	b := batchFromParsed("TN1", untimed).Events[0].EventFingerprint
	if a != b {
		t.Fatalf("untimed fingerprint changed between deliveries: %s vs %s", a, b)
	}

	timed := track17.ParsedTrack{Events: []track17.Event{{TimeUTC: "2026-11-01T10:00:00Z", Description: "d", Location: "L", Stage: "PickedUp"}}}
	got := batchFromParsed("TN1", timed).Events[0].EventFingerprint
	want := domain.Fingerprint("TN1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "L", "d", "PickedUp")
	if got != want {
		t.Fatalf("timed fingerprint changed format: %s, want %s", got, want)
	}
}

func TestReturnTypeFollowsLatestHitWithoutTouchingStatus(t *testing.T) {
	pkg := &domain.Package{Status: domain.StatusInTransit, ReturnDetectSupported: true}
	events := func(sub string) []domain.TrackingEvent {
		return []domain.TrackingEvent{{EventFingerprint: "fp-" + sub, SubStatus: sub}}
	}

	applyReturnToPackage(pkg, events(domain.SubStatusExceptionReturning), time.Now())
	if !pkg.HasReturn || *pkg.ReturnType != domain.ReturnRTS || *pkg.ReturnCarrierEventID != "fp-"+domain.SubStatusExceptionReturning {
		t.Fatalf("first hit = %+v", pkg)
	}
	if pkg.Status != domain.StatusInTransit {
		t.Fatalf("status overwritten: %s", pkg.Status)
	}

	applyReturnToPackage(pkg, events(domain.SubStatusExceptionReturned), time.Now())
	if *pkg.ReturnType != domain.ReturnWarehouse {
		t.Fatalf("return type not updated: %s", *pkg.ReturnType)
	}

	applyReturnToPackage(pkg, nil, time.Now())
	if !pkg.ReturnDetectSupported || *pkg.ReturnType != domain.ReturnWarehouse {
		t.Fatalf("event-less batch reset return state: %+v", pkg)
	}
}

func TestRetrackKeepsConcurrentWebhookWrite(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	id := registerOne(t, svc, "TN-R")
	repo.pkgs[id].SubscriptionStatus = domain.SubStopped
	fs.onRetrack = func() { repo.pkgs[id].LatestEventDesc = "from webhook" }

	n, err := svc.RetrackStopped(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RetrackStopped = %d, %v", n, err)
	}
	got := repo.pkgs[id]
	if got.LatestEventDesc != "from webhook" {
		t.Fatalf("webhook write lost: %+v", got)
	}
	if got.SubscriptionStatus != domain.SubTracking || !got.IsRetracked {
		t.Fatalf("retrack not applied: %+v", got)
	}
}

func TestRetryRegisteringRecoversStuckPackages(t *testing.T) {
	svc, repo, _, _ := newTestService(t)
	stuck := registerOne(t, svc, "TN-STUCK")
	fresh := registerOne(t, svc, "TN-FRESH")
	repo.pkgs[stuck].SubscriptionStatus = domain.SubRegistering
	repo.pkgs[stuck].UpdatedAt = time.Now().Add(-2 * time.Hour)
	repo.pkgs[fresh].SubscriptionStatus = domain.SubRegistering
	repo.pkgs[fresh].UpdatedAt = time.Now()

	n, err := svc.RetryRegistering(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RetryRegistering = %d, %v", n, err)
	}
	if st := repo.pkgs[stuck].SubscriptionStatus; st != domain.SubTracking {
		t.Fatalf("stuck status = %s", st)
	}
	if st := repo.pkgs[fresh].SubscriptionStatus; st != domain.SubRegistering {
		t.Fatalf("fresh package retried too early: %s", st)
	}
}
