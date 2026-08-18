package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	operations          *prometheus.CounterVec
	duration            *prometheus.HistogramVec
	http                *prometheus.CounterVec
	admissionInFlight   prometheus.Gauge
	admissionRejected   *prometheus.CounterVec
	ledgerCalls         *prometheus.CounterVec
	ledgerCallDuration  *prometheus.HistogramVec
	ledgerCallsInFlight prometheus.Gauge
	registry            *prometheus.Registry
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
		admissionInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ledger_admission_in_flight",
			Help: "Requests currently holding a ledger-route admission slot.",
		}),
		admissionRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_admission_rejected_total",
			Help: "Requests rejected before execution by reason.",
		}, []string{"reason"}),
		ledgerCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_dependency_calls_total",
			Help: "TigerBeetle SDK calls by operation and transport outcome.",
		}, []string{"operation", "outcome"}),
		ledgerCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ledger_dependency_call_duration_seconds",
			Help:    "TigerBeetle SDK call duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		ledgerCallsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ledger_dependency_calls_in_flight",
			Help: "TigerBeetle SDK calls currently waiting for a response.",
		}),
		registry: prometheus.NewRegistry(),
	}
	metrics.registry.MustRegister(
		metrics.operations,
		metrics.duration,
		metrics.http,
		metrics.admissionInFlight,
		metrics.admissionRejected,
		metrics.ledgerCalls,
		metrics.ledgerCallDuration,
		metrics.ledgerCallsInFlight,
	)
	return metrics
}

func (m *Metrics) BeginAdmission() {
	m.admissionInFlight.Inc()
}

func (m *Metrics) EndAdmission() {
	m.admissionInFlight.Dec()
}

func (m *Metrics) ObserveAdmissionRejection(reason string) {
	m.admissionRejected.WithLabelValues(reason).Inc()
}

func (m *Metrics) ObserveLedgerCall(kind, outcome string, elapsed time.Duration) {
	m.ledgerCalls.WithLabelValues(kind, outcome).Inc()
	m.ledgerCallDuration.WithLabelValues(kind).Observe(elapsed.Seconds())
}

func (m *Metrics) SetLedgerCallsInFlight(inFlight int) {
	m.ledgerCallsInFlight.Set(float64(inFlight))
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
