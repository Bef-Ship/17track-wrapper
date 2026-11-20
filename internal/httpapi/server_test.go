package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaTyolo/17track-wrapper/api"
	"github.com/SolaTyolo/17track-wrapper/internal/domain"
)

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) (int, string, []domain.ErrorBody) {
	t.Helper()
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Errors []domain.ErrorBody `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return body.Code, body.Message, body.Data.Errors
}

func TestHandlerRequiresServiceToken(t *testing.T) {
	h := New(nil, ServiceTokens{"backend": "sek", "gateway": "gw"}).Handler()
	cases := []struct {
		name, path   string
		header, auth string
		want         int
	}{
		{"healthz is open", "/healthz", "", "", http.StatusOK},
		{"readyz is open", "/readyz", "", "", http.StatusOK},
		{"openapi is open", "/openapi.yaml", "", "", http.StatusOK},
		{"missing token", "/v1/packages", "", "", http.StatusUnauthorized},
		{"wrong token", "/v1/packages", "nope", "", http.StatusUnauthorized},
		{"wrong bearer", "/v1/packages", "", "Bearer nope", http.StatusUnauthorized},
		{"17token header", "/v1/unknown", "sek", "", http.StatusNotFound},
		{"bearer", "/v1/unknown", "", "Bearer gw", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			if c.header != "" {
				req.Header.Set(TokenHeader, c.header)
			}
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
			if c.want == http.StatusUnauthorized {
				if code, _, errs := decodeEnvelope(t, rec); code != domain.CodeInvalidToken || len(errs) != 1 {
					t.Fatalf("body = %s", rec.Body.String())
				}
			}
		})
	}
}

func TestParseServiceTokens(t *testing.T) {
	got, err := ParseServiceTokens(" backend:a , gateway:b,c ")
	if err != nil || len(got) != 3 || got["backend"] != "a" || got["gateway"] != "b" || got["default"] != "c" {
		t.Fatalf("tokens = %v %v", got, err)
	}
	for _, bad := range []string{"backend:", ":tok", "a:1,a:2"} {
		if _, err := ParseServiceTokens(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
	if got, err := ParseServiceTokens(""); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v %v", got, err)
	}
}

func TestReadyzReportsDependencyFailure(t *testing.T) {
	h := New(nil, nil).WithReadiness(func(context.Context) error { return errors.New("db down") }).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBusinessErrorsUse17TrackEnvelope(t *testing.T) {
	h := New(nil, nil).Handler()
	cases := []struct {
		name, method, path, body string
		code                     int
	}{
		{"bad paging", http.MethodGet, "/v1/packages?limit=abc", "", domain.CodeFieldInvalid},
		{"malformed json", http.MethodPost, "/v1/packages/register", `{"items":`, domain.CodeDataInvalid},
		{"unknown field", http.MethodPost, "/v1/packages/cancel", `{"number":"TN1"}`, domain.CodeDataInvalid},
		{"restore without id", http.MethodPost, "/v1/packages/restore", `{}`, domain.CodeFieldRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (business errors are HTTP 200 like 17TRACK)", rec.Code)
			}
			code, msg, errs := decodeEnvelope(t, rec)
			if code != c.code || msg == "" || len(errs) != 1 || errs[0].Code != c.code {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestErrorStatus(t *testing.T) {
	cases := map[error]int{
		domain.ErrRequired("number"):  http.StatusOK,
		domain.ErrNotRegistered("TN"): http.StatusOK,
		errors.New("boom"):            http.StatusInternalServerError,
		domain.Errorf(domain.ErrNotConfigured, domain.CodeArchiveUnavailable, "x"): http.StatusServiceUnavailable,
	}
	for err, want := range cases {
		rec := httptest.NewRecorder()
		fail(rec, err)
		if rec.Code != want {
			t.Fatalf("%v: status = %d, want %d", err, rec.Code, want)
		}
	}
}

func TestRegisterBodyUses17TrackFields(t *testing.T) {
	decode := func(body string) (domain.RegisterInput, bool) {
		var in domain.RegisterInput
		ok := decodeJSON(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &in)
		return in, ok
	}
	in, ok := decode(`{"source":"shipment","items":[{"number":"TN1","carrier":100002,"lang":"en",
		"order_no":"SO-1","destination_country":"US","external_ref":"r1","new_generation":true}]}`)
	if !ok {
		t.Fatal("decode failed")
	}
	it := in.Items[0]
	if in.Source != "shipment" || it.Number != "TN1" || it.Carrier != 100002 || it.Lang != "en" ||
		it.OrderNo != "SO-1" || it.DestinationCountry != "US" || it.ExternalRef != "r1" || !it.NewGeneration {
		t.Fatalf("decoded = %+v", in)
	}
	if _, ok := decode(`{"items":[{"tracking_no":"TN1"}]}`); ok {
		t.Fatal("legacy tracking_no should be rejected")
	}
}

func TestEveryRouteIsInOpenAPI(t *testing.T) {
	spec := string(api.OpenAPI)
	for _, rt := range New(nil, nil).routeTable() {
		_, path, _ := strings.Cut(rt.pattern, " ")
		if !strings.Contains(spec, "\n  "+path+":\n") {
			t.Errorf("%s missing from api/openapi.yaml", rt.pattern)
		}
	}
}

func TestHandlerOpenWithoutToken(t *testing.T) {
	rec := httptest.NewRecorder()
	New(nil, nil).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
