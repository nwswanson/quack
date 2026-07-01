package secret

import "net/http"

type Middleware func(http.Handler) http.Handler

func RegisterRoutes(mux *http.ServeMux, h *Handler, middleware Middleware) {
	wrap := func(next http.Handler) http.Handler {
		if middleware == nil {
			return next
		}
		return middleware(next)
	}
	mux.Handle("/admin2/secrets", wrap(http.HandlerFunc(h.Page)))
	mux.Handle("/admin2/secrets/unlock", wrap(http.HandlerFunc(h.Unlock)))
}
