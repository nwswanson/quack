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
		// Phase 12 TODO: keep this nil-to-disabled fallback even after adding a
		// real executor. It is the final safety net that prevents public routing
		// from accidentally executing user code when composition forgets to wire a
		// runtime service.
		runtime = appruntime.NewDisabledService()
	}
	return Handler{runtime: runtime}
}

func (h Handler) ServeHTTPRoute(w http.ResponseWriter, r *http.Request, req appruntime.InvocationRequest) {
	// Phase 12 TODO: read and cap the request body here before passing it to the
	// runtime service. The cap should come from RouteMetadata.ResourceLimits and
	// must return a deterministic 413-style failure without invoking the executor.
	//
	// Phase 12 TODO: copy only the headers user code should see. Today this passes
	// r.Header as-is because execution is disabled; real execution needs an allow
	// or deny list for hop-by-hop, auth, and internal proxy headers.
	resp, err := h.runtime.InvokeHTTP(r.Context(), req)
	if err != nil {
		if errors.Is(err, appruntime.ErrDisabled) {
			http.Error(w, "runtime execution is disabled", http.StatusNotImplemented)
			return
		}
		// Phase 12 TODO: map structured runtime errors to stable HTTP responses:
		// timeout, denied capability, oversized response, startup failure, user
		// panic/error, and executor unavailable should not all collapse to 500.
		http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
		return
	}
	// Phase 12 TODO: enforce response limits before writing headers. If the
	// executor streams responses later, this adapter must enforce the byte cap
	// while streaming instead of buffering unbounded data in memory.
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
