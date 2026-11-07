package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/olap"
	"github.com/SolaTyolo/tracking-service/internal/service"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

// fakeStore implements just enough of store.Store for HTTP-level tests.
// Tracking number "MISSING" is unknown and "BOOM" fails with an internal error.
type fakeStore struct {
	store.Store
	pkgs []domain.Package
	got  store.PackageFilter
}

func (f *fakeStore) ListPackages(_ context.Context, filter store.PackageFilter) ([]domain.Package, error) {
	f.got = filter
	if len(f.pkgs) > filter.Limit {
		return f.pkgs[:filter.Limit], nil
	}
	return f.pkgs, nil
}

func (f *fakeStore) WithTx(_ context.Context, fn func(store.Store) error) error { return fn(f) }

func (f *fakeStore) GetPackageForUpdate(_ context.Context, tenantID, tn string) (*domain.Package, error) {
	switch tn {
	case "MISSING":
		return nil, store.ErrNotFound
	case "BOOM":
		return nil, errors.New("dial tcp 10.0.0.5:5432: connection refused")
	}
	return &domain.Package{PackageID: 1, TenantID: tenantID, TrackingNumber: tn}, nil
}

func (f *fakeStore) InsertEvents(context.Context, []domain.TrackingEvent) ([]domain.TrackingEvent, error) {
	return nil, nil
}

func (f *fakeStore) UpdatePackage(context.Context, *domain.Package) error { return nil }

func newTestServer(st store.Store, apiKey string) http.Handler {
	svc := service.NewTracking(st, olap.Noop{}, nil, 90, 0)
	return New(svc, Options{OLAPName: "noop", APIKey: apiKey}).Handler()
}

func do(h http.Handler, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListPackages(t *testing.T) {
	st := &fakeStore{pkgs: []domain.Package{{PackageID: 9}, {PackageID: 7}, {PackageID: 4}}}
	h := newTestServer(st, "")

	rec := do(h, http.MethodGet, "/v1/packages?status=in_transit,delivered&destination_country=US&limit=2&cursor=10", "",
		tenantHeader, "acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if st.got.TenantID != "acme" || len(st.got.Statuses) != 2 || st.got.DestinationCountry != "US" ||
		st.got.BeforeID != 10 || st.got.Limit != 2 {
		t.Fatalf("filter=%+v", st.got)
	}
	var resp struct {
		Items      []domain.Package `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 2 || resp.NextCursor != "7" {
		t.Fatalf("resp=%+v", resp)
	}

	if rec := do(h, http.MethodGet, "/v1/packages", ""); rec.Code != http.StatusOK || st.got.TenantID != domain.DefaultTenant {
		t.Fatalf("default tenant: code=%d tenant=%q", rec.Code, st.got.TenantID)
	}
	if rec := do(h, http.MethodGet, "/v1/packages?status=flying", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status code=%d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/v1/packages", "", tenantHeader, "bad tenant!"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad tenant code=%d", rec.Code)
	}
}

func TestAnalytics(t *testing.T) {
	h := newTestServer(&fakeStore{}, "")
	if rec := do(h, http.MethodGet, "/v1/analytics/events", ""); rec.Code != http.StatusConflict {
		t.Fatalf("olap disabled code=%d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/v1/analytics/stats?group_by=day,stage", ""); rec.Code != http.StatusConflict {
		t.Fatalf("olap disabled code=%d", rec.Code)
	}
	for _, target := range []string{
		"/v1/analytics/stats",
		"/v1/analytics/stats?group_by=nope",
		"/v1/analytics/stats?group_by=day,stage,country,carrier_code",
		"/v1/analytics/events?from=" + time.Now().UTC().Format(time.RFC3339) + "&to=2020-01-01T00:00:00Z",
	} {
		if rec := do(h, http.MethodGet, target, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code=%d body=%s", target, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestAPIKey(t *testing.T) {
	h := newTestServer(&fakeStore{}, "s3cret")
	cases := []struct {
		name    string
		target  string
		headers []string
		want    int
	}{
		{"missing key", "/v1/packages", nil, http.StatusUnauthorized},
		{"wrong key", "/v1/packages", []string{"X-API-Key", "nope"}, http.StatusUnauthorized},
		{"x-api-key", "/v1/packages", []string{"X-API-Key", "s3cret"}, http.StatusOK},
		{"bearer", "/v1/packages", []string{"Authorization", "Bearer s3cret"}, http.StatusOK},
		{"health is public", "/health", nil, http.StatusOK},
		{"metrics is public", "/metrics", nil, http.StatusOK},
	}
	for _, c := range cases {
		if rec := do(h, http.MethodGet, c.target, "", c.headers...); rec.Code != c.want {
			t.Errorf("%s: code=%d want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestIngestPerBatchResults(t *testing.T) {
	h := newTestServer(&fakeStore{}, "")
	body := `{"batches": [{"tracking_number": "OK1"}, {"tracking_number": "MISSING"}, {"tracking_number": "BOOM"}]}`
	rec := do(h, http.MethodPost, "/v1/tracking-events", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("internal error leaked: %s", rec.Body)
	}
	var resp struct {
		Ingested int            `json:"ingested"`
		Failed   int            `json:"failed"`
		Results  []ingestResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range resp.Results {
		got = append(got, r.TrackingNumber+":"+r.Status)
	}
	if resp.Ingested != 1 || resp.Failed != 2 || strings.Join(got, ",") != "OK1:ok,MISSING:not_found,BOOM:error" {
		t.Fatalf("resp=%+v", resp)
	}

	if rec := do(h, http.MethodPost, "/v1/tracking-events", `{"tracking_number": "MISSING"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("single not found code=%d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/v1/tracking-events", `{"tracking_number": "OK1"}`); rec.Code != http.StatusOK {
		t.Fatalf("single ok code=%d", rec.Code)
	}
}

func TestRequestID(t *testing.T) {
	h := newTestServer(&fakeStore{}, "")
	if rec := do(h, http.MethodGet, "/health", "", requestIDHeader, "abc"); rec.Header().Get(requestIDHeader) != "abc" {
		t.Fatalf("request id not echoed: %q", rec.Header().Get(requestIDHeader))
	}
	if rec := do(h, http.MethodGet, "/health", ""); len(rec.Header().Get(requestIDHeader)) != 16 {
		t.Fatalf("request id not generated: %q", rec.Header().Get(requestIDHeader))
	}
}
