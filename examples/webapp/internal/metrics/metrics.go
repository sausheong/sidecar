package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, path, and status code.",
		},
		[]string{"method", "path", "status"},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	TasksTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "tasks_total",
			Help: "Current number of tasks in the store.",
		},
	)

	once sync.Once
)

// Register registers all metrics with the default Prometheus registry.
// Safe to call multiple times (e.g. in tests).
func Register() {
	once.Do(func() {
		prometheus.MustRegister(RequestsTotal, RequestDuration, TasksTotal)
	})
}
