package publichttp

import (
	"net/http"

	"quack/internal/protocol"
	"quack/internal/sites"
	"quack/internal/statichttp"
)

type RouteKind string

const (
	RouteStatic RouteKind = "static"
)

type PublicRouteDecision struct {
	Site    string
	Version int64
	Kind    RouteKind
	Path    string
}

type Handler struct {
	static statichttp.Handler
}

func New(static statichttp.Handler) Handler {
	return Handler{static: static}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handlePublicRequest)
}

func (h Handler) handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	decision := h.decide(r)
	if decision.Site == "" {
		http.NotFound(w, r)
		return
	}

	switch decision.Kind {
	case RouteStatic:
		h.static.ServeSiteFile(w, r, statichttp.Request{
			Site:    decision.Site,
			URLPath: decision.Path,
		})
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) decide(r *http.Request) PublicRouteDecision {
	site := sites.NameFromHost(r.Host)
	if site == "" {
		return PublicRouteDecision{}
	}
	return PublicRouteDecision{
		Site: site,
		Kind: RouteStatic,
		Path: r.URL.Path,
	}
}
