package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SolaTyolo/17track-wrapper/api"
	"github.com/SolaTyolo/17track-wrapper/internal/application"
	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
	"github.com/SolaTyolo/17track-wrapper/pkg/track17"
)

// Server exposes TrackingService over HTTP (17TRACK wrapper + package snapshots).
// Request fields, response envelope ({code, message, data}) and error codes follow
// 17TRACK v2; the contract is api/openapi.yaml.
type Server struct {
	svc    *application.Service
	mux    *http.ServeMux
	tokens ServiceTokens
	ready  func(context.Context) error
}

// New builds the API. With tokens set, every route except the public ones requires a
// service token (17token header or Bearer).
func New(svc *application.Service, tokens ServiceTokens) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux(), tokens: tokens}
	for _, rt := range s.routeTable() {
		s.mux.HandleFunc(rt.pattern, rt.handler)
	}
	return s
}

// WithReadiness sets the dependency check behind /readyz (e.g. a DB ping).
func (s *Server) WithReadiness(check func(context.Context) error) *Server {
	s.ready = check
	return s
}

var publicPaths = map[string]bool{"/healthz": true, "/readyz": true, "/openapi.yaml": true}

func (s *Server) Handler() http.Handler {
	if len(s.tokens) == 0 {
		return s.mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			s.mux.ServeHTTP(w, r)
			return
		}
		name, ok := s.tokens.match(requestToken(r))
		if !ok {
			fail(w, domain.Errorf(nil, domain.CodeInvalidToken, "Invalid access security key."))
			return
		}
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, name)))
	})
}

type route struct {
	pattern string
	handler http.HandlerFunc
}

func (s *Server) routeTable() []route {
	return []route{
		{"GET /healthz", s.healthz},
		{"GET /readyz", s.readyz},
		{"GET /openapi.yaml", s.openapi},

		{"POST /v1/packages/register", s.register},
		{"POST /v1/packages/cancel", s.cancel},
		{"POST /v1/packages/refresh", s.refresh},
		{"POST /v1/packages/change-carrier", s.changeCarrier},
		{"POST /v1/packages/restore", s.restore},
		{"GET /v1/packages", s.listPackages},
		{"GET /v1/packages/events", s.getPackageEvents},
		{"GET /v1/packages/history", s.getHistory},
		{"GET /v1/packages/route", s.getRoute},
		{"GET /v1/carriers", s.listCarriers},
		// Sync fallback for the gateway; normally pushes arrive on the event bus (worker role).
		{"POST /v1/webhooks/17track", s.applyWebhook},
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(api.OpenAPI)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in domain.RegisterInput
	if !decodeJSON(w, r, &in) {
		return
	}
	respond(w)(s.svc.Register(r.Context(), in))
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var in domain.CancelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	respond(w)(s.svc.Cancel(r.Context(), in))
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Number string `json:"number"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	respond(w)(s.svc.Refresh(r.Context(), body.Number))
}

func (s *Server) changeCarrier(w http.ResponseWriter, r *http.Request) {
	var in domain.ChangeCarrierInput
	if !decodeJSON(w, r, &in) {
		return
	}
	respond(w)(s.svc.ChangeCarrier(r.Context(), in))
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PackageID int64 `json:"package_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.PackageID <= 0 {
		fail(w, domain.ErrRequired("package_id"))
		return
	}
	respond(w)(s.svc.Restore(r.Context(), body.PackageID))
}

func (s *Server) listPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.ListPackagesFilter{
		ExternalRef:        q.Get("external_ref"),
		OriginCountry:      q.Get("origin_country"),
		DestinationCountry: q.Get("destination_country"),
		SubscriptionStatus: q.Get("subscription_status"),
		Tag:                q.Get("tag"),
		Source:             q.Get("source"),
		IncludeSuperseded:  q.Get("include_superseded") == "true",
	}
	if v := q.Get("status"); v != "" {
		f.Statuses = strings.Split(v, ",")
	}
	limit, err1 := queryInt(q, "limit")
	offset, err2 := queryInt(q, "offset")
	beforeID, err3 := queryInt(q, "before_id")
	if err := errors.Join(err1, err2, err3); err != nil {
		fail(w, err)
		return
	}
	if q.Has("carrier") {
		carrier, err := queryInt(q, "carrier")
		if err != nil {
			fail(w, err)
			return
		}
		c := int(carrier)
		f.Carrier = &c
	}
	f.Limit, f.Offset, f.BeforeID = int(limit), int(offset), beforeID
	respond(w)(s.svc.ListPackages(r.Context(), f))
}

// packageRef reads ?package_id= (one generation), else ?number= with optional
// ?external_ref= (that order's generation) or alone (current generation).
func packageRef(q url.Values) (domain.PackageRef, error) {
	id, err := queryInt(q, "package_id")
	if err != nil {
		return domain.PackageRef{}, err
	}
	return domain.PackageRef{Number: q.Get("number"), PackageID: id, ExternalRef: q.Get("external_ref")}, nil
}

func (s *Server) getPackageEvents(w http.ResponseWriter, r *http.Request) {
	ref, err := packageRef(r.URL.Query())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w)(s.svc.GetPackageEvents(r.Context(), ref))
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	respond(w)(s.svc.History(r.Context(), q.Get("number"), q.Get("events") == "true"))
}

func (s *Server) getRoute(w http.ResponseWriter, r *http.Request) {
	ref, err := packageRef(r.URL.Query())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w)(s.svc.GetRoute(r.Context(), ref))
}

type carrierItem struct {
	Key        int    `json:"key"`
	Name       string `json:"name"`
	NameZhCN   string `json:"name_zh_cn"`
	CountryISO string `json:"country_iso"`
}

func (s *Server) listCarriers(w http.ResponseWriter, _ *http.Request) {
	list, err := track17.ListCarriers()
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]carrierItem, 0, len(list))
	for _, c := range list {
		out = append(out, carrierItem{Key: c.Key, Name: c.Name, NameZhCN: c.NameZhCN, CountryISO: c.CountryISO})
	}
	ok(w, out)
}

// applyWebhook answers non-2xx on any failure so the gateway retries the push.
func (s *Server) applyWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil {
		err = s.svc.ApplyWebhookBody(r.Context(), raw)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrInvalid) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errorEnvelope(errorBody(err)))
		return
	}
	ok(w, nil)
}

// queryInt parses an optional non-negative integer query param (absent = 0).
func queryInt(q url.Values, name string) (int64, error) {
	v := q.Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, domain.ErrInvalidValue(name)
	}
	return n, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		fail(w, domain.Errorf(domain.ErrInvalid, domain.CodeDataInvalid, "The submitted data is invalid. (%v)", err))
		return false
	}
	return true
}

// envelope is the 17TRACK response body. code 0 = success; otherwise message and
// data.errors carry the error (17TRACK request-level error shape).
type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type errorsData struct {
	Errors []domain.ErrorBody `json:"errors"`
}

func errorEnvelope(b domain.ErrorBody) envelope {
	return envelope{Code: b.Code, Message: b.Message, Data: errorsData{Errors: []domain.ErrorBody{b}}}
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{Code: domain.CodeOK, Data: data})
}

// respond writes (data, err) from a service call.
func respond(w http.ResponseWriter) func(any, error) {
	return func(v any, err error) {
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, v)
	}
}

// fail writes err as a 17TRACK error. Like 17TRACK, business errors are HTTP 200 with
// code != 0; only auth (401) and server-side failures (5xx, safe to retry) use
// other statuses.
func fail(w http.ResponseWriter, err error) {
	b := errorBody(err)
	writeJSON(w, httpStatus(b.Code), errorEnvelope(b))
}

func errorBody(err error) domain.ErrorBody {
	b := domain.Body(err)
	if b.Code == domain.CodeInternal {
		blog.Svc("tracking").Error("http request failed", "err", err.Error())
	}
	return b
}

func httpStatus(code int) int {
	switch code {
	case domain.CodeInvalidToken:
		return http.StatusUnauthorized
	case domain.CodeInternal:
		return http.StatusInternalServerError
	case domain.CodeArchiveUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
