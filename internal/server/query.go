package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/olap"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

const tenantHeader = "X-Tenant-ID"

var tenantPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// tenantFrom reads the tenant id header (default tenant when absent);
// it writes a 400 and returns false when the value is malformed.
func tenantFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := strings.TrimSpace(r.Header.Get(tenantHeader))
	if v == "" {
		return domain.DefaultTenant, true
	}
	if !tenantPattern.MatchString(v) {
		writeErr(w, http.StatusBadRequest, tenantHeader+" must match "+tenantPattern.String())
		return "", false
	}
	return v, true
}

// GET /v1/packages
func (s *Server) handleListPackages(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFrom(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.PackageFilter{
		TenantID:           tenantID,
		CarrierCode:        q.Get("carrier_code"),
		ProviderName:       q.Get("provider_name"),
		ExternalRef:        q.Get("external_ref"),
		OriginCountry:      q.Get("origin_country"),
		DestinationCountry: q.Get("destination_country"),
	}
	var err error
	if v := q.Get("status"); v != "" {
		f.Statuses = strings.Split(v, ",")
		for _, st := range f.Statuses {
			if !domain.IsValidStatus(st) {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("status: unknown value %q", st))
				return
			}
		}
	}
	if v := q.Get("is_archived"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "is_archived: "+err.Error())
			return
		}
		f.IsArchived = &b
	}
	if f.CreatedFrom, err = timeParam(q, "created_from"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.CreatedTo, err = timeParam(q, "created_to"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.BeforeID, err = int64Param(q, "cursor"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.Limit, err = limitParam(q, "limit", 50, 200); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	pkgs, err := s.svc.ListPackages(r.Context(), f)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if pkgs == nil {
		pkgs = []domain.Package{}
	}
	resp := map[string]any{"items": pkgs}
	if len(pkgs) == f.Limit {
		resp["next_cursor"] = strconv.FormatInt(pkgs[len(pkgs)-1].PackageID, 10)
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /v1/analytics/events
func (s *Server) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter, ok := eventFilterFrom(w, r, 7*24*time.Hour)
	if !ok {
		return
	}
	limit, err := limitParam(q, "limit", 100, 1000)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := int64Param(q, "offset")
	if err != nil || offset > 10000 {
		writeErr(w, http.StatusBadRequest, "offset must be an integer in [0, 10000]")
		return
	}
	events, err := s.svc.QueryOLAPEvents(r.Context(), olap.EventQuery{
		EventFilter: filter, Limit: limit, Offset: int(offset),
	})
	if err != nil {
		writeOLAPErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "from": filter.From, "to": filter.To})
}

// GET /v1/analytics/stats
func (s *Server) handleAnalyticsStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter, ok := eventFilterFrom(w, r, 30*24*time.Hour)
	if !ok {
		return
	}
	groupBy := strings.Split(q.Get("group_by"), ",")
	if q.Get("group_by") == "" || len(groupBy) > 3 {
		writeErr(w, http.StatusBadRequest, "group_by requires 1-3 of: "+strings.Join(olap.StatsDimensions, ","))
		return
	}
	for _, dim := range groupBy {
		if !slices.Contains(olap.StatsDimensions, dim) {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("group_by: unknown dimension %q", dim))
			return
		}
	}
	limit, err := limitParam(q, "limit", 1000, 10000)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.svc.OLAPStats(r.Context(), olap.StatsQuery{
		EventFilter: filter, GroupBy: groupBy, Limit: limit,
	})
	if err != nil {
		writeOLAPErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "from": filter.From, "to": filter.To})
}

// eventFilterFrom parses tenant, from/to (default: the last defaultSpan) and equality filters.
func eventFilterFrom(w http.ResponseWriter, r *http.Request, defaultSpan time.Duration) (olap.EventFilter, bool) {
	tenantID, ok := tenantFrom(w, r)
	if !ok {
		return olap.EventFilter{}, false
	}
	q := r.URL.Query()
	from, err := timeParam(q, "from")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return olap.EventFilter{}, false
	}
	to, err := timeParam(q, "to")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return olap.EventFilter{}, false
	}
	f := olap.EventFilter{
		TenantID:           tenantID,
		To:                 time.Now().UTC(),
		TrackingNumber:     q.Get("tracking_number"),
		CarrierCode:        q.Get("carrier_code"),
		EventCarrierCode:   q.Get("event_carrier_code"),
		ProviderName:       q.Get("provider_name"),
		PackageStatus:      q.Get("package_status"),
		Stage:              q.Get("stage"),
		SubStatus:          q.Get("sub_status"),
		OriginCountry:      q.Get("origin_country"),
		DestinationCountry: q.Get("destination_country"),
		Country:            q.Get("country"),
	}
	if to != nil {
		f.To = *to
	}
	f.From = f.To.Add(-defaultSpan)
	if from != nil {
		f.From = *from
	}
	if !f.From.Before(f.To) {
		writeErr(w, http.StatusBadRequest, "from must be before to")
		return olap.EventFilter{}, false
	}
	return f, true
}

func writeOLAPErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, olap.ErrDisabled) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeInternal(w, r, err)
}

func timeParam(q url.Values, key string) (*time.Time, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("%s: expect RFC3339: %w", key, err)
	}
	t = t.UTC()
	return &t, nil
}

func int64Param(q url.Values, key string) (int64, error) {
	v := q.Get(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: expect a non-negative integer", key)
	}
	return n, nil
}

func limitParam(q url.Values, key string, def, max int) (int, error) {
	n, err := int64Param(q, key)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return def, nil
	}
	if n > int64(max) {
		return 0, fmt.Errorf("%s: at most %d", key, max)
	}
	return int(n), nil
}
