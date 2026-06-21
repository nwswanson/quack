package runtimehttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHandlerCapsRequestBodyBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime)

	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("toolarge"))
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: http.MethodPost,
		Limits: appruntime.ResourceLimits{MaxRequestBytes: 3},
	})

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if runtime.called {
		t.Fatal("runtime called for oversized body")
	}
}

func TestHandlerCopiesOnlyPublicHeaders(t *testing.T) {
	runtime := &recordingRuntime{resp: appruntime.InvocationResponse{StatusCode: http.StatusOK, Headers: map[string][]string{
		"Connection":   {"close"},
		"Content-Type": {"text/plain"},
	}, Body: []byte("ok")}}
	handler := New(runtime)

	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("hello"))
	req.Header.Set("X-Test", "visible")
	req.Header.Set("Authorization", "secret")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodPost})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := runtime.req.Headers["X-Test"]; len(got) != 1 || got[0] != "visible" {
		t.Fatalf("headers = %+v, want X-Test visible", runtime.req.Headers)
	}
	if _, ok := runtime.req.Headers["Authorization"]; ok {
		t.Fatalf("headers = %+v, authorization should be stripped", runtime.req.Headers)
	}
	if rec.Header().Get("Connection") != "" {
		t.Fatalf("response connection header = %q, want stripped", rec.Header().Get("Connection"))
	}
}

func TestHandlerMapsStructuredRuntimeErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"denied":         {err: appruntime.ErrCapabilityDenied, want: http.StatusForbidden},
		"method":         {err: appruntime.ErrMethodNotAllowed, want: http.StatusMethodNotAllowed},
		"response large": {err: appruntime.ErrResponseTooLarge, want: http.StatusBadGateway},
		"timeout":        {err: appruntime.ErrTimeout, want: http.StatusGatewayTimeout},
		"concurrency":    {err: appruntime.ErrConcurrencyLimit, want: http.StatusTooManyRequests},
		"route missing":  {err: appruntime.ErrRouteNotFound, want: http.StatusNotFound},
		"generic":        {err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			handler := New(&recordingRuntime{err: tc.err})
			req := httptest.NewRequest(http.MethodGet, "/api", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestHandlerReturnsInvocationFailureDetails(t *testing.T) {
	handler := New(&recordingRuntime{err: fmt.Errorf("%w:\nTraceback: kaboom", appruntime.ErrInvocationFailure)})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Traceback: kaboom") {
		t.Fatalf("body = %q, want invocation failure details", rec.Body.String())
	}
}

type recordingRuntime struct {
	called bool
	req    appruntime.InvocationRequest
	resp   appruntime.InvocationResponse
	err    error
}

func (r *recordingRuntime) InvokeHTTP(ctx context.Context, req appruntime.InvocationRequest) (appruntime.InvocationResponse, error) {
	r.called = true
	r.req = req
	return r.resp, r.err
}
