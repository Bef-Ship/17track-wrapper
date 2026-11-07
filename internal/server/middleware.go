package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/metrics"
	"github.com/SolaTyolo/tracking-service/internal/reqid"
)

const requestIDHeader = "X-Request-ID"

// publicPaths skip API key auth: probes and Prometheus scraping.
var publicPaths = map[string]bool{"/health": true, "/ready": true, "/metrics": true}

// requireAPIKey accepts "Authorization: Bearer <key>" or "X-API-Key: <key>".
// An empty key disables auth.
func requireAPIKey(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	want := []byte(key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-API-Key")
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			got = bearer
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeErr(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// observe assigns a request id, then logs and records metrics per request.
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(requestIDHeader)
		if id == "" || len(id) > 64 {
			id = reqid.New()
		}
		w.Header().Set(requestIDHeader, id)
		r = r.WithContext(reqid.With(r.Context(), id))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		// r.Pattern is filled in by ServeMux; empty means no route matched.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		if publicPaths[r.URL.Path] && rec.status < 400 {
			return
		}
		slog.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", rec.status,
			"duration_ms", elapsed.Milliseconds(),
			"tenant_id", r.Header.Get(tenantHeader),
		)
	})
}
