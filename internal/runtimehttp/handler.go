package runtimehttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"quack/internal/domain"
	appruntime "quack/internal/runtime"
)

type Handler struct {
	runtime  appruntime.Service
	settings SettingsReader
	sockets  *socketManager
}

type SettingsReader interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
}

type Option func(*Handler)

func WithSettings(settings SettingsReader) Option {
	return func(h *Handler) {
		h.settings = settings
	}
}

func New(runtime appruntime.Service, opts ...Option) Handler {
	if runtime == nil {
		// Keep this nil-to-disabled fallback as the final safety net that prevents
		// public routing from executing user code when composition forgets to wire
		// a runtime service.
		runtime = appruntime.NewDisabledService()
	}
	h := Handler{runtime: runtime, sockets: newSocketManager()}
	for _, opt := range opts {
		opt(&h)
	}
	return h
}

func (h Handler) ActiveWebSockets(site string) int64 {
	if h.sockets == nil {
		return 0
	}
	return h.sockets.activeBySite(site)
}

func (h Handler) ActiveWebSocketsTotal() int64 {
	if h.sockets == nil {
		return 0
	}
	return h.sockets.activeTotal()
}

func (h Handler) ActiveWebSocketsBySite() map[string]int64 {
	if h.sockets == nil {
		return nil
	}
	return h.sockets.activeBySiteSnapshot()
}

func (h Handler) ServeHTTPRoute(w http.ResponseWriter, r *http.Request, req appruntime.InvocationRequest) {
	limits := req.Limits
	if limits.MaxRequestBytes <= 0 {
		limits.MaxRequestBytes = appruntime.DefaultMaxRequestBytes
	}
	req.Body = nil
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.MaxRequestBytes))
		if err != nil {
			http.Error(w, "runtime request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		req.Body = body
	}
	req.Headers = publicHeaders(r.Header)
	resp, err := h.runtime.InvokeHTTP(r.Context(), req)
	if err != nil {
		if errors.Is(err, appruntime.ErrDisabled) {
			http.Error(w, "runtime execution is disabled", http.StatusNotImplemented)
			return
		}
		switch {
		case errors.Is(err, appruntime.ErrCapabilityDenied):
			http.Error(w, "runtime capability denied", http.StatusForbidden)
		case errors.Is(err, appruntime.ErrMethodNotAllowed):
			http.Error(w, "runtime method is not allowed", http.StatusMethodNotAllowed)
		case errors.Is(err, appruntime.ErrRequestTooLarge):
			http.Error(w, "runtime request body is too large", http.StatusRequestEntityTooLarge)
		case errors.Is(err, appruntime.ErrResponseTooLarge):
			http.Error(w, "runtime response body is too large", http.StatusBadGateway)
		case errors.Is(err, appruntime.ErrTimeout):
			http.Error(w, "runtime execution timed out", http.StatusGatewayTimeout)
		case errors.Is(err, appruntime.ErrConcurrencyLimit):
			http.Error(w, "runtime concurrency limit reached", http.StatusTooManyRequests)
		case errors.Is(err, appruntime.ErrRouteNotFound):
			http.NotFound(w, r)
		case errors.Is(err, appruntime.ErrInvocationFailure):
			// TODO: Gate detailed runtime error responses behind a site.yml setting.
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
		}
		return
	}
	for key, values := range resp.Headers {
		if !responseHeaderAllowed(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

func publicHeaders(in http.Header) map[string][]string {
	out := map[string][]string{}
	for key, values := range in {
		if !requestHeaderAllowed(key) {
			continue
		}
		out[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	return out
}

func requestHeaderAllowed(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "cookie", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto", "x-real-ip":
		return false
	default:
		return true
	}
}

func responseHeaderAllowed(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return false
	default:
		return true
	}
}
