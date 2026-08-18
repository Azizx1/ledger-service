package httpapi

import (
	"net/http"

	"github.com/Azizx1/ledger-service/internal/domain"
	"github.com/Azizx1/ledger-service/internal/observability"
)

type admissionController struct {
	slots       chan struct{}
	ledgerReady func() bool
	metrics     *observability.Metrics
}

func newAdmissionController(limit int, ledgerReady func() bool, metrics *observability.Metrics) *admissionController {
	return &admissionController{slots: make(chan struct{}, limit), ledgerReady: ledgerReady, metrics: metrics}
}

func (a *admissionController) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !a.ledgerReady() {
			a.metrics.ObserveAdmissionRejection("ledger_stalled")
			writer.Header().Set("Retry-After", "1")
			writeJSON(writer, http.StatusServiceUnavailable, domain.ErrorResponse{
				Status:    "error",
				ErrorCode: "ledger_stalled",
				Message:   "the ledger is not responding; retry the same request",
			})
			return
		}
		select {
		case a.slots <- struct{}{}:
			a.metrics.BeginAdmission()
			defer func() {
				<-a.slots
				a.metrics.EndAdmission()
			}()
			next.ServeHTTP(writer, request)
		default:
			a.metrics.ObserveAdmissionRejection("capacity")
			writer.Header().Set("Retry-After", "1")
			writeJSON(writer, http.StatusServiceUnavailable, domain.ErrorResponse{
				Status:    "error",
				ErrorCode: "overloaded",
				Message:   "too many operations are in flight; retry the same request",
			})
		}
	})
}
