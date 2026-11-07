// Package metrics holds the Prometheus collectors exposed on /metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tracking_http_requests_total",
		Help: "HTTP requests by route pattern and status code.",
	}, []string{"method", "route", "code"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tracking_http_request_duration_seconds",
		Help:    "HTTP request latency by route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	EventsIngested = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tracking_events_inserted_total",
		Help: "Tracking events newly inserted into PostgreSQL (duplicates excluded).",
	})

	OLAPInsertFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tracking_olap_insert_failures_total",
		Help: "Failed OLAP writes; those events are missing from analytics.",
	})

	PackagesArchived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tracking_packages_archived_total",
		Help: "Packages moved to cold storage.",
	})
)
