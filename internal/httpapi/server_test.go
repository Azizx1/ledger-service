package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azizx1/ledger-service/internal/observability"
	"github.com/Azizx1/ledger-service/internal/service"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func TestIncrementAuthorizationReadsAuthorizationIDFromBody(t *testing.T) {
	t.Parallel()
	handler := newTestHandler()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/authorizations/increments",
		strings.NewReader(`{
			"request_id":"19c5d2b61ac27dd55d9c9daff5af446",
			"authorization_id":"19c5d2b61ac27dd55d9c9daff5af445",
			"increment_cents":500
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error_code":"authorization_not_found"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestIncrementAuthorizationOldPathIsNotRegistered(t *testing.T) {
	t.Parallel()
	handler := newTestHandler()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/authorizations/19c5d2b61ac27dd55d9c9daff5af445/increments",
		strings.NewReader(`{
			"request_id":"19c5d2b61ac27dd55d9c9daff5af446",
			"authorization_id":"19c5d2b61ac27dd55d9c9daff5af445",
			"increment_cents":500
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
	}
}

func newTestHandler() http.Handler {
	metrics := observability.NewMetrics()
	ledgerService := service.New(
		emptyLedger{},
		1,
		time.Hour,
		0,
		100_000,
		2*time.Second,
		100,
		slog.New(slog.DiscardHandler),
		nil,
	)
	return NewHandler(ledgerService, slog.New(slog.DiscardHandler), metrics, 10)
}

type emptyLedger struct{}

func (emptyLedger) CreateAccount(tb.Account) (tb.CreateAccountResult, error) {
	return tb.CreateAccountResult{}, nil
}

func (emptyLedger) CreateTransfer(tb.Transfer) (tb.CreateTransferResult, error) {
	return tb.CreateTransferResult{}, nil
}

func (emptyLedger) LookupAccount(tb.Uint128) (tb.Account, bool, error) {
	return tb.Account{}, false, nil
}

func (emptyLedger) LookupTransfer(tb.Uint128) (tb.Transfer, bool, error) {
	return tb.Transfer{}, false, nil
}
