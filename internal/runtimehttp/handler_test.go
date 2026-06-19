package runtimehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	appruntime "quack/internal/runtime"
)

func TestHandlerReturnsDisabledWhenRuntimeIsNotConfigured(t *testing.T) {
	handler := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}
