package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "flowproxy_requests_total",
		Help: "Total proxied requests",
	}, []string{"method", "route", "status_code"})

	RateLimitedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "flowproxy_rate_limited_total",
		Help: "Total rate-limited requests",
	}, []string{"client_id", "route"})

	UpstreamLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "flowproxy_upstream_latency_seconds",
		Help:    "Upstream response latency",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"route"})

	RedisErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "flowproxy_redis_errors_total",
		Help: "Redis operation errors",
	}, []string{"operation"})

	ActiveConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "flowproxy_active_connections",
		Help: "Current in-flight requests",
	})

	ConfigReloads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "flowproxy_config_reload_total",
		Help: "Config reload attempts",
	}, []string{"status"})

	DroppedLogs = promauto.NewCounter(prometheus.CounterOpts{
		Name: "flowproxy_dropped_logs_total",
		Help: "Log entries dropped due to full buffer",
	})
)

// Metrics records request metrics: count, latency histogram, active connections.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ActiveConnections.Inc()
		defer ActiveConnections.Dec()

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		route := normalizeRoute(r.URL.Path)
		status := strconv.Itoa(rw.status)

		RequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		UpstreamLatency.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

// normalizeRoute takes first two path segments to avoid high cardinality.
// /api/search/v2/foo => /api/search
func normalizeRoute(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) >= 2 {
		return "/" + parts[0] + "/" + parts[1]
	}
	return "/" + parts[0]
}
