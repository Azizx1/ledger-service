package httpapi

import (
	"net/http"

	"github.com/abdulaziz/ledger-service/internal/domain"
)

type admissionController struct {
	slots chan struct{}
}

func newAdmissionController(limit int) *admissionController {
	return &admissionController{slots: make(chan struct{}, limit)}
}

func (a *admissionController) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
			next.ServeHTTP(writer, request)
		default:
			writer.Header().Set("Retry-After", "1")
			writeJSON(writer, http.StatusServiceUnavailable, domain.ErrorResponse{
				Status:    "error",
				ErrorCode: "overloaded",
				Message:   "too many operations are in flight; retry the same request",
			})
		}
	})
}
