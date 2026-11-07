package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/provider"
	"github.com/SolaTyolo/tracking-service/internal/reqid"
	"github.com/SolaTyolo/tracking-service/internal/service"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

const shutdownTimeout = 25 * time.Second

// Options configures a Server.
type Options struct {
	OLAPName  string
	OLAPOn    bool
	ArchiveOn bool
	APIKey    string // empty disables auth
}

// Server exposes package registration, event ingest/query, and admin jobs.
type Server struct {
	svc     *service.Tracking
	opts    Options
	handler http.Handler
}

func New(svc *service.Tracking, opts Options) *Server {
	s := &Server{svc: svc, opts: opts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /ready", s.handleReady)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("POST /v1/packages", s.handleRegisterPackage)
	mux.HandleFunc("GET /v1/packages", s.handleListPackages)
	mux.HandleFunc("GET /v1/packages/{trackingNumber}/events", s.handleGetEvents)
	mux.HandleFunc("POST /v1/tracking-events", s.handleTrackingEvents)
	mux.HandleFunc("GET /v1/analytics/events", s.handleAnalyticsEvents)
	mux.HandleFunc("GET /v1/analytics/stats", s.handleAnalyticsStats)
	mux.HandleFunc("POST /v1/admin/archive", s.handleArchiveJob)
	s.handler = observe(requireAPIKey(opts.APIKey, mux))
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

// Run serves until ctx is cancelled, then stops accepting connections and
// waits up to shutdownTimeout for in-flight requests.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: POST /v1/admin/archive runs a whole archive job synchronously.
	}
	slog.Info("tracking-service listening",
		"addr", addr,
		"olap_enabled", s.opts.OLAPOn,
		"olap", s.opts.OLAPName,
		"archive_enabled", s.opts.ArchiveOn,
		"auth_enabled", s.opts.APIKey != "",
	)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down http server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"olap_enabled":    s.opts.OLAPOn,
		"olap":            s.opts.OLAPName,
		"archive_enabled": s.opts.ArchiveOn,
	})
}

// handleReady is the readiness probe: it fails while PostgreSQL is unreachable.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.svc.Ping(ctx); err != nil {
		slog.WarnContext(r.Context(), "readiness check failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleRegisterPackage(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFrom(w, r)
	if !ok {
		return
	}
	var req struct {
		TrackingNumber string   `json:"tracking_number"`
		CarrierCode    string   `json:"carrier_code"`
		ProviderName   string   `json:"provider_name"`
		ExternalRef    string   `json:"external_ref"`
		LabelURL       string   `json:"label_url"`
		Weight         *float64 `json:"weight"`
		DimLength      *float64 `json:"dim_length"`
		DimWidth       *float64 `json:"dim_width"`
		DimHeight      *float64 `json:"dim_height"`
		domain.ShippingDetail
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pkg := &domain.Package{
		TenantID:       tenantID,
		ShippingDetail: req.ShippingDetail,
		TrackingNumber: req.TrackingNumber,
		CarrierCode:    req.CarrierCode,
		ProviderName:   req.ProviderName,
		ExternalRef:    req.ExternalRef,
		LabelURL:       req.LabelURL,
		Weight:         req.Weight,
		DimLength:      req.DimLength,
		DimWidth:       req.DimWidth,
		DimHeight:      req.DimHeight,
	}
	out, err := s.svc.RegisterPackage(r.Context(), pkg)
	if errors.Is(err, service.ErrInvalid) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFrom(w, r)
	if !ok {
		return
	}
	tn := r.PathValue("trackingNumber")
	pkg, events, err := s.svc.GetPackageEvents(r.Context(), tenantID, tn)
	if err != nil {
		if store.IsNotFound(err) {
			writeErr(w, http.StatusNotFound, "package not found")
			return
		}
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"package": pkg,
		"events":  events,
	})
}

type ingestResult struct {
	TrackingNumber string `json:"tracking_number"`
	Status         string `json:"status"` // ok, not_found, error
	Error          string `json:"error,omitempty"`
}

func (s *Server) handleTrackingEvents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFrom(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	batches, err := provider.Parse(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Each batch commits on its own; one failure does not stop the others.
	results := make([]ingestResult, 0, len(batches))
	failed, notFound := 0, 0
	for _, batch := range batches {
		batch.TenantID = tenantID
		res := ingestResult{TrackingNumber: batch.TrackingNumber, Status: "ok"}
		if err := s.svc.IngestBatch(r.Context(), batch); err != nil {
			failed++
			if store.IsNotFound(err) {
				notFound++
				res.Status, res.Error = "not_found", "package not registered"
			} else {
				slog.ErrorContext(r.Context(), "tracking events ingest failed",
					"tenant_id", tenantID, "tracking_number", batch.TrackingNumber, "err", err)
				res.Status, res.Error = "error", "internal error"
			}
		}
		results = append(results, res)
	}
	status := http.StatusOK
	switch {
	case failed == 0:
	case failed < len(batches):
		status = http.StatusMultiStatus
	case notFound == failed:
		status = http.StatusNotFound
	default:
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"ingested": len(batches) - failed,
		"failed":   failed,
		"results":  results,
	})
}

func (s *Server) handleArchiveJob(w http.ResponseWriter, r *http.Request) {
	n, err := s.svc.ArchiveDuePackages(r.Context())
	if errors.Is(err, service.ErrArchiveDisabled) || errors.Is(err, service.ErrJobRunning) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"archived": n,
		"at":       time.Now().UTC(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeInternal logs err and returns a generic 500 with the request id, so
// internals (SQL, storage keys, hosts) never reach the caller.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error":      "internal error",
		"request_id": reqid.From(r.Context()),
	})
}
