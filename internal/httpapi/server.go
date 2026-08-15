package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/Azizx1/ledger-service/internal/domain"
	"github.com/Azizx1/ledger-service/internal/observability"
	"github.com/Azizx1/ledger-service/internal/service"
)

const maxRequestBytes = 1 << 20

type Server struct {
	service *service.Service
	logger  *slog.Logger
}

func NewHandler(ledgerService *service.Service, logger *slog.Logger, metrics *observability.Metrics, maxConcurrentRequests int) http.Handler {
	server := &Server{service: ledgerService, logger: logger}
	admission := newAdmissionController(maxConcurrentRequests)
	mux := http.NewServeMux()

	server.handle(mux, metrics, admission, "create_account", "POST /v1/accounts", server.createAccount)
	server.handle(mux, metrics, admission, "get_balance", "GET /v1/accounts/{account_id}", server.balance)
	server.handle(mux, metrics, admission, "topup", "POST /v1/topups", server.topUp)
	server.handle(mux, metrics, admission, "withdrawal", "POST /v1/withdrawals", server.withdraw)
	server.handle(mux, metrics, admission, "card_allocation", "POST /v1/card-allocations", server.allocateToCard)
	server.handle(mux, metrics, admission, "card_return", "POST /v1/card-returns", server.returnFromCard)
	server.handle(mux, metrics, admission, "authorization", "POST /v1/authorizations", server.authorize)
	server.handle(mux, metrics, admission, "authorization_increment", "POST /v1/authorizations/{authorization_id}/increments", server.increment)
	mux.Handle("GET /health/live", metrics.HTTPMiddleware("liveness", http.HandlerFunc(server.live)))
	mux.Handle("GET /health/ready", metrics.HTTPMiddleware("readiness", http.HandlerFunc(server.ready)))
	mux.Handle("GET /metrics", metrics.Handler())

	return recoverPanics(logger, mux)
}

func (s *Server) handle(mux *http.ServeMux, metrics *observability.Metrics, admission *admissionController, route, pattern string, handler http.HandlerFunc) {
	mux.Handle(pattern, metrics.HTTPMiddleware(route, admission.Wrap(handler)))
}

func (s *Server) createAccount(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreateAccountRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.CreateAccount(input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) balance(writer http.ResponseWriter, request *http.Request) {
	response, err := s.service.Balance(request.PathValue("account_id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) topUp(writer http.ResponseWriter, request *http.Request) {
	var input domain.TopUpRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.TopUp(input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) withdraw(writer http.ResponseWriter, request *http.Request) {
	var input domain.WithdrawalRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.Withdraw(input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) allocateToCard(writer http.ResponseWriter, request *http.Request) {
	var input domain.CardAllocationRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.AllocateToCard(input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) returnFromCard(writer http.ResponseWriter, request *http.Request) {
	var input domain.CardReturnRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.ReturnFromCard(input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) authorize(writer http.ResponseWriter, request *http.Request) {
	var input domain.AuthorizationRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	response, status, err := s.service.Authorize(request.Context(), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) increment(writer http.ResponseWriter, request *http.Request) {
	var input domain.IncrementAuthorizationRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, err)
		return
	}
	authorizationID := request.PathValue("authorization_id")
	if input.AuthorizationID != "" && input.AuthorizationID != authorizationID {
		writeError(writer, fmt.Errorf("%w: authorization_id in path and body must match", domain.ErrInvalidRequest))
		return
	}
	input.AuthorizationID = authorizationID
	response, status, err := s.service.IncrementAuthorization(request.Context(), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, status, response)
}

func (s *Server) live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	contentType := request.Header.Get("Content-Type")
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			return fmt.Errorf("%w: Content-Type must be application/json", domain.ErrInvalidRequest)
		}
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %v", domain.ErrInvalidRequest, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: request body must contain one JSON value", domain.ErrInvalidRequest)
	}
	return nil
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "dependency_unavailable"
	message := "a required dependency is unavailable; retry the same request"

	switch {
	case errors.Is(err, domain.ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, domain.ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "idempotency_conflict", err.Error()
	case errors.Is(err, domain.ErrAccountNotFound):
		status, code, message = http.StatusNotFound, "account_not_found", err.Error()
	case errors.Is(err, domain.ErrAuthorizationNotFound):
		status, code, message = http.StatusNotFound, "authorization_not_found", err.Error()
	case errors.Is(err, context.Canceled):
		status, code, message = 499, "request_canceled", "the request was canceled"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusGatewayTimeout, "deadline_exceeded", "the request deadline elapsed"
	}
	writeJSON(writer, status, domain.ErrorResponse{Status: "error", ErrorCode: code, Message: message})
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic serving request", "panic", recovered, "path", request.URL.Path)
				writeJSON(writer, http.StatusInternalServerError, domain.ErrorResponse{
					Status: "error", ErrorCode: "internal_error", Message: "an internal error occurred",
				})
			}
		}()
		next.ServeHTTP(writer, request)
	})
}
