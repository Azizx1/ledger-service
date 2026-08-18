package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azizx1/ledger-service/internal/observability"
)

func TestAdmissionControllerRejectsWithoutWaiting(t *testing.T) {
	t.Parallel()

	controller := newAdmissionController(1, func() bool { return true }, observability.NewMetrics())
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := controller.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	}))

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	}()
	<-entered

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", second.Code)
	}
	if second.Header().Get("Retry-After") != "1" {
		t.Fatalf("expected Retry-After header, got %q", second.Header().Get("Retry-After"))
	}

	close(release)
	<-firstDone
}

func TestAdmissionControllerRejectsWhenLedgerIsStalled(t *testing.T) {
	t.Parallel()
	controller := newAdmissionController(1, func() bool { return false }, observability.NewMetrics())
	called := false
	handler := controller.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
	if called {
		t.Fatal("stalled request reached the operation handler")
	}
	if !strings.Contains(response.Body.String(), `"error_code":"ledger_stalled"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}
