package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"sidecar-demo/internal/metrics"
)

// statusRecorder wraps ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logger writes one JSON log line per request to out.
// Lines with status >= 500 carry level "ERROR", triggering Sidecar's logs adapter.
func Logger(out io.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			entry := map[string]any{
				"level":   levelFor(rec.status),
				"time":    time.Now().UTC().Format(time.RFC3339),
				"method":  r.Method,
				"path":    r.URL.Path,
				"status":  rec.status,
				"latency": time.Since(start).String(),
			}
			_ = json.NewEncoder(out).Encode(entry)
		})
	}
}

// Metrics records request count and duration into Prometheus.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		metrics.RequestsTotal.WithLabelValues(
			r.Method, r.URL.Path, fmt.Sprintf("%d", rec.status),
		).Inc()
		metrics.RequestDuration.WithLabelValues(r.Method, r.URL.Path).
			Observe(time.Since(start).Seconds())
	})
}

func levelFor(status int) string {
	switch {
	case status >= 500:
		return "ERROR"
	case status >= 400:
		return "WARN"
	default:
		return "INFO"
	}
}
