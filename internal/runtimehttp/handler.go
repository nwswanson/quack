package runtimehttp

import (
	"errors"
	"net/http"

	appruntime "quack/internal/runtime"
)

type Handler struct {
	runtime appruntime.Service
}

func New(runtime appruntime.Service) Handler {
	if runtime == nil {
		runtime = appruntime.NewDisabledService()
	}
	return Handler{runtime: runtime}
}

func (h Handler) ServeHTTPRoute(w http.ResponseWriter, r *http.Request, req appruntime.InvocationRequest) {
	resp, err := h.runtime.InvokeHTTP(r.Context(), req)
	if err != nil {
		if errors.Is(err, appruntime.ErrDisabled) {
			http.Error(w, "runtime execution is disabled", http.StatusNotImplemented)
			return
		}
		http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
		return
	}
	for key, values := range resp.Headers {
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
