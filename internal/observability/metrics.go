package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	operations *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	http       *prometheus.CounterVec
	registry   *prometheus.Registry
}

func NewMetrics() *Metrics {
	metrics := &Metrics{
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_operations_total",
			Help: "Completed business operations by kind and outcome.",
		}, []string{"kind", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ledger_operation_duration_seconds",
			Help:    "End-to-end business operation latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind"}),
		http: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_http_requests_total",
			Help: "HTTP requests by method, route, and status.",
		}, []string{"method", "route", "status"}),
		registry: prometheus.NewRegistry(),
	}
	metrics.registry.MustRegister(metrics.operations, metrics.duration, metrics.http)
	return metrics
}

func (m *Metrics) ObserveOperation(kind, outcome string, elapsed time.Duration) {
	m.operations.WithLabelValues(kind, outcome).Inc()
	m.duration.WithLabelValues(kind).Observe(elapsed.Seconds())
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) HTTPMiddleware(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capture := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(capture, request)
		m.http.WithLabelValues(request.Method, route, strconv.Itoa(capture.status)).Inc()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
