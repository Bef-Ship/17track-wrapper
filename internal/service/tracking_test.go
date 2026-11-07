package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/olap"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

type memStore struct {
	mu       sync.Mutex
	pkgs     map[string]*domain.Package
	byID     map[int64]*domain.Package
	events   map[int64][]domain.TrackingEvent
	nextID   int64
	nextEvID int64
	lock     sync.Mutex // TryLock
	txMu     sync.Mutex // WithTx serializes transactions; no rollback
}

func newMemStore() *memStore {
	return &memStore{
		pkgs:   make(map[string]*domain.Package),
		byID:   make(map[int64]*domain.Package),
		events: make(map[int64][]domain.TrackingEvent),
	}
}

func (m *memStore) Ping(context.Context) error { return nil }

func (m *memStore) WithTx(_ context.Context, fn func(store.Store) error) error {
	m.txMu.Lock()
	defer m.txMu.Unlock()
	return fn(m)
}

func (m *memStore) GetPackageForUpdate(ctx context.Context, tenantID, tn string) (*domain.Package, error) {
	return m.GetPackageByTrackingNumber(ctx, tenantID, tn)
}

func (m *memStore) CreatePackage(_ context.Context, pkg *domain.Package) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pkgs[memKey(pkg.TenantID, pkg.TrackingNumber)]; ok {
		return store.ErrAlreadyExists
	}
	m.nextID++
	cp := *pkg
	cp.PackageID = m.nextID
	cp.CreatedAt = time.Now().UTC()
	cp.UpdatedAt = cp.CreatedAt
	if cp.Status == "" {
		cp.Status = domain.StatusRegistered
	}
	m.pkgs[memKey(cp.TenantID, cp.TrackingNumber)] = &cp
	m.byID[cp.PackageID] = &cp
	*pkg = cp
	return nil
}

func memKey(tenantID, tn string) string { return tenantID + "/" + tn }

func (m *memStore) GetPackageByTrackingNumber(_ context.Context, tenantID, tn string) (*domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pkgs[memKey(tenantID, tn)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memStore) UpdatePackage(_ context.Context, pkg *domain.Package) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[pkg.PackageID]; !ok {
		return store.ErrNotFound
	}
	cp := *pkg
	cp.UpdatedAt = time.Now().UTC()
	m.byID[pkg.PackageID] = &cp
	m.pkgs[memKey(pkg.TenantID, pkg.TrackingNumber)] = &cp
	return nil
}

func (m *memStore) ListPackages(_ context.Context, f store.PackageFilter) ([]domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Package
	for id := m.nextID; id > 0 && len(out) < f.Limit; id-- {
		p, ok := m.byID[id]
		if ok && p.TenantID == f.TenantID && (f.BeforeID == 0 || id < f.BeforeID) {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (m *memStore) TryLock(context.Context, string) (func(), bool, error) {
	if !m.lock.TryLock() {
		return nil, false, nil
	}
	return m.lock.Unlock, true, nil
}

func (m *memStore) ListPackagesDueArchive(_ context.Context, before time.Time, limit int) ([]domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Package
	for _, p := range m.pkgs {
		if p.DeliveredAt != nil && p.DeliveredAt.Before(before) && !p.IsArchived {
			out = append(out, *p)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *memStore) InsertEvents(_ context.Context, events []domain.TrackingEvent) ([]domain.TrackingEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var inserted []domain.TrackingEvent
	for _, ev := range events {
		dup := false
		for _, existing := range m.events[ev.PackageID] {
			if existing.EventFingerprint == ev.EventFingerprint {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		m.nextEvID++
		ev.EventID = m.nextEvID
		ev.CreatedAt = time.Now().UTC()
		m.events[ev.PackageID] = append(m.events[ev.PackageID], ev)
		inserted = append(inserted, ev)
	}
	return inserted, nil
}

func (m *memStore) ListEventsByPackage(_ context.Context, packageID int64) ([]domain.TrackingEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.TrackingEvent(nil), m.events[packageID]...), nil
}

func (m *memStore) DeleteEventsByPackage(_ context.Context, packageID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.events, packageID)
	return nil
}

type countingOLAP struct {
	mu    sync.Mutex
	count int
}

func (c *countingOLAP) Name() string                       { return "counting" }
func (c *countingOLAP) EnsureSchema(context.Context) error { return nil }
func (c *countingOLAP) Close() error                       { return nil }
func (c *countingOLAP) InsertEvents(_ context.Context, events []olap.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count += len(events)
	return nil
}

type memCold struct {
	mu    sync.Mutex
	snaps map[string]domain.ArchiveSnapshot
}

func newMemCold() *memCold {
	return &memCold{snaps: make(map[string]domain.ArchiveSnapshot)}
}

func (c *memCold) Name() string { return "mem" }

func (c *memCold) Put(_ context.Context, key string, snap domain.ArchiveSnapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snaps[key] = snap
	return nil
}

func (c *memCold) Get(_ context.Context, key string) (*domain.ArchiveSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.snaps[key]
	if !ok {
		return nil, errors.New("not found: " + key)
	}
	return &snap, nil
}

func TestIngestIdempotentAndOLAP(t *testing.T) {
	st := newMemStore()
	olapCounter := &countingOLAP{}
	svc := NewTracking(st, olapCounter, newMemCold(), 90, 0)

	ctx := context.Background()
	pkg := &domain.Package{TrackingNumber: "TN1", CarrierCode: "ups", ProviderName: "manual"}
	if _, err := svc.RegisterPackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	fp := domain.Fingerprint("TN1", now, "SZ", "picked up", "PU")
	batch := domain.EventBatch{
		TrackingNumber: "TN1",
		CarrierCode:    "ups",
		ProviderName:   "manual",
		UpdateTime:     now,
		Events: []domain.TrackingEvent{{
			TrackingNumber:   "TN1",
			EventTime:        now,
			Location:         "SZ",
			Description:      "picked up",
			StatusCode:       "PU",
			EventFingerprint: fp,
			RawJSON:          json.RawMessage(`{"k":1}`),
		}},
	}
	if err := svc.IngestBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	_, events, err := svc.GetPackageEvents(ctx, domain.DefaultTenant, "TN1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event after idempotent ingest, got %d", len(events))
	}
	olapCounter.mu.Lock()
	n := olapCounter.count
	olapCounter.mu.Unlock()
	if n != 1 {
		// only first ingest inserts rows; duplicate fingerprint skips OLAP write
		t.Fatalf("olap count=%d want 1", n)
	}
}

func TestArchiveAndUnarchive(t *testing.T) {
	st := newMemStore()
	svc := NewTracking(st, olap.Noop{}, newMemCold(), 90, 0)
	ctx := context.Background()

	delivered := time.Now().UTC().Add(-100 * 24 * time.Hour)
	pkg := &domain.Package{
		TrackingNumber: "TN2",
		CarrierCode:    "dhl",
		ProviderName:   "manual",
		DeliveredAt:    &delivered,
		Status:         domain.StatusDelivered,
	}
	if _, err := svc.RegisterPackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_ = svc.IngestBatch(ctx, domain.EventBatch{
		TrackingNumber: "TN2",
		ProviderName:   "manual",
		UpdateTime:     now,
		Events: []domain.TrackingEvent{{
			TrackingNumber:   "TN2",
			EventTime:        now.Add(-time.Hour),
			Description:      "delivered",
			EventFingerprint: domain.Fingerprint("TN2", now.Add(-time.Hour), "", "delivered", ""),
		}},
	})

	n, err := svc.ArchiveDuePackages(ctx)
	if err != nil || n != 1 {
		t.Fatalf("archive n=%d err=%v", n, err)
	}
	got, events, err := svc.GetPackageEvents(ctx, domain.DefaultTenant, "TN2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsArchived || len(events) != 1 {
		t.Fatalf("archived=%v events=%d", got.IsArchived, len(events))
	}

	// new webhook should unarchive
	later := now.Add(time.Minute)
	if err := svc.IngestBatch(ctx, domain.EventBatch{
		TrackingNumber: "TN2",
		ProviderName:   "manual",
		UpdateTime:     later,
		Events: []domain.TrackingEvent{{
			TrackingNumber:   "TN2",
			EventTime:        later,
			Description:      "exception reopen",
			EventFingerprint: domain.Fingerprint("TN2", later, "", "exception reopen", ""),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	got, events, err = svc.GetPackageEvents(ctx, domain.DefaultTenant, "TN2")
	if err != nil {
		t.Fatal(err)
	}
	if got.IsArchived {
		t.Fatal("expected unarchived")
	}
	if len(events) != 2 {
		t.Fatalf("want 2 hot events, got %d", len(events))
	}
}

func ev(tn string, at time.Time, desc string) domain.TrackingEvent {
	return domain.TrackingEvent{
		TrackingNumber: tn, EventTime: at, Description: desc,
		EventFingerprint: domain.Fingerprint(tn, at, "", desc, ""),
	}
}

func TestStalePayloadKeepsEventsAndOLAPGetsOnlyNew(t *testing.T) {
	st := newMemStore()
	counter := &countingOLAP{}
	svc := NewTracking(st, counter, nil, 90, 0)
	ctx := context.Background()
	if _, err := svc.RegisterPackage(ctx, &domain.Package{TrackingNumber: "S1", CarrierCode: "ups"}); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	a, b := ev("S1", t0, "a"), ev("S1", t0.Add(time.Hour), "b")

	if err := svc.IngestBatch(ctx, domain.EventBatch{
		TrackingNumber: "S1", UpdateTime: t0.Add(2 * time.Hour), Status: domain.StatusInTransit,
		Events: []domain.TrackingEvent{a},
	}); err != nil {
		t.Fatal(err)
	}
	// Older payload replaying a plus a new event b: b is stored, status untouched.
	if err := svc.IngestBatch(ctx, domain.EventBatch{
		TrackingNumber: "S1", UpdateTime: t0.Add(time.Hour), Status: domain.StatusException,
		Events: []domain.TrackingEvent{a, b},
	}); err != nil {
		t.Fatal(err)
	}
	pkg, events, err := svc.GetPackageEvents(ctx, domain.DefaultTenant, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("stale payload must still store new events, got %d", len(events))
	}
	if pkg.Status != domain.StatusInTransit {
		t.Fatalf("stale payload changed status to %s", pkg.Status)
	}
	if counter.count != 2 {
		t.Fatalf("olap rows=%d want 2 (replayed event a must not be re-sent)", counter.count)
	}
}

func TestMissingUpdateTimeDoesNotBlockLaterPayloads(t *testing.T) {
	svc := NewTracking(newMemStore(), olap.Noop{}, nil, 90, 0)
	ctx := context.Background()
	if _, err := svc.RegisterPackage(ctx, &domain.Package{TrackingNumber: "U1", CarrierCode: "ups"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestBatch(ctx, domain.EventBatch{TrackingNumber: "U1", Status: domain.StatusInTransit}); err != nil {
		t.Fatal(err)
	}
	// Carrier clock an hour behind the server must still apply.
	if err := svc.IngestBatch(ctx, domain.EventBatch{
		TrackingNumber: "U1", UpdateTime: time.Now().UTC().Add(-time.Hour), Status: domain.StatusOutForDelivery,
	}); err != nil {
		t.Fatal(err)
	}
	pkg, _, _ := svc.GetPackageEvents(ctx, domain.DefaultTenant, "U1")
	if pkg.Status != domain.StatusOutForDelivery {
		t.Fatalf("status=%s, payload wrongly treated as stale", pkg.Status)
	}
}

func TestArchiveSkipsPackageChangedSinceListing(t *testing.T) {
	st := newMemStore()
	svc := NewTracking(st, olap.Noop{}, newMemCold(), 90, 0)
	ctx := context.Background()
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	pkg := &domain.Package{TrackingNumber: "R1", CarrierCode: "ups", DeliveredAt: &old}
	if _, err := svc.RegisterPackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	listed := *pkg
	recent := time.Now().UTC()
	pkg.DeliveredAt = &recent
	if err := st.UpdatePackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	done, err := svc.archiveOne(ctx, listed, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil || done {
		t.Fatalf("done=%v err=%v; package re-read under lock is no longer due", done, err)
	}
}

func TestArchiveDisabled(t *testing.T) {
	svc := NewTracking(newMemStore(), olap.Noop{}, nil, 90, 0)
	if _, err := svc.ArchiveDuePackages(context.Background()); !errors.Is(err, ErrArchiveDisabled) {
		t.Fatalf("err=%v want ErrArchiveDisabled", err)
	}
}
