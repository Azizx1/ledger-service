package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdmissionControllerRejectsWithoutWaiting(t *testing.T) {
	t.Parallel()

	controller := newAdmissionController(1)
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
