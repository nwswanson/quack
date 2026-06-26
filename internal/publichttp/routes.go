package publichttp

import (
	"log/slog"
	"net/http"
	"strings"

	"quack/internal/logbuffer"
	"quack/internal/policy"
	"quack/internal/protocol"
	"quack/internal/releases"
	appruntime "quack/internal/runtime"
	"quack/internal/runtimehttp"
	"quack/internal/sites"
	"quack/internal/statichttp"
)

type RouteKind string

const (
	RouteStatic    RouteKind = "static"
	RouteHTTP      RouteKind = "http"
	RouteWebSocket RouteKind = "websocket"
)

type PublicRouteDecision struct {
	Site                string
	SiteHost            string
	Version             int64
	Kind                RouteKind
	Path                string
	RoutePath           string
	StaticRoot          string
	StaticFile          string
	Methods             []string
	ResourceLimits      appruntime.ResourceLimits
	ExposeRuntimeErrors bool
	DeniedReason        string
	BlockedHost         bool
}

type Handler struct {
	static       statichttp.Handler
	runtime      runtimehttp.Handler
	routes       RouteReader
	hostResolver sites.HostResolver
}

type RouteReader interface {
	LookupRoute(r *http.Request, site string, path string) (PublicRouteDecision, bool, error)
}

type SiteInfo struct {
	Site    string `json:"site"`
	Version int64  `json:"version"`
}

type SiteInfoReader interface {
	CurrentSiteInfo(r *http.Request, site string) (SiteInfo, bool, error)
}

type Option func(*Handler)

func WithRoutes(routes RouteReader) Option {
	return func(h *Handler) {
		h.routes = routes
	}
}

func WithRuntime(runtime runtimehttp.Handler) Option {
	return func(h *Handler) {
		// Real execution is introduced by passing an explicitly constructed
		// runtimehttp.Handler from server.New. Do not make publichttp instantiate
		// an executor; this package should stay a transport router, not an
		// execution composition root.
		h.runtime = runtime
	}
}

func WithHostResolver(resolver sites.HostResolver) Option {
	return func(h *Handler) {
		h.hostResolver = resolver
	}
}

func New(static statichttp.Handler, opts ...Option) Handler {
	h := Handler{static: static, runtime: runtimehttp.New(nil)}
	for _, opt := range opts {
		opt(&h)
	}
	return h
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handlePublicRequest)
}

func (h Handler) handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	decision, err := h.decide(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "public route lookup failed", "host", r.Host, "path", r.URL.Path, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if decision.Site == "" {
		if decision.BlockedHost {
			writeUnconfiguredHost(w)
			return
		}
		http.NotFound(w, r)
		return
	}
	logbuffer.SetRequestSite(r.Context(), decision.Site, decision.Version, decision.Path)
	if decision.DeniedReason != "" {
		protocol.WriteError(w, http.StatusForbidden, decision.DeniedReason)
		return
	}
	if isQuackPath(r.URL.Path) {
		h.handleQuackRequest(w, r, decision.Site)
		return
	}

	switch decision.Kind {
	case RouteStatic:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.static.ServeSiteFile(w, r, statichttp.Request{
			Site:       decision.Site,
			URLPath:    decision.Path,
			RoutePath:  decision.RoutePath,
			StaticRoot: decision.StaticRoot,
			StaticFile: decision.StaticFile,
		})
	case RouteHTTP:
		if !methodAllowed(r.Method, decision.Methods) {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.runtime.ServeHTTPRoute(w, r, appruntime.InvocationRequest{
			Site: decision.Site, Version: decision.Version, Route: decision.Path, Method: r.Method, Query: r.URL.RawQuery, Limits: decision.ResourceLimits,
			ExposeRuntimeErrors: decision.ExposeRuntimeErrors,
		})
	case RouteWebSocket:
		h.runtime.ServeWebSocketRoute(w, r, appruntime.WebSocketInvocationRequest{
			Site: decision.Site, SiteHost: decision.SiteHost, Version: decision.Version, Route: decision.Path, Query: r.URL.RawQuery, Limits: decision.ResourceLimits,
		})
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) decide(r *http.Request) (PublicRouteDecision, error) {
	resolution, err := h.resolveHost(r)
	if err != nil {
		return PublicRouteDecision{}, err
	}
	switch resolution.Status {
	case sites.HostResolved:
	case sites.HostBlocked:
		return PublicRouteDecision{BlockedHost: true}, nil
	default:
		return PublicRouteDecision{}, nil
	}
	site := resolution.Site
	if isQuackPath(r.URL.Path) {
		return PublicRouteDecision{Site: site, SiteHost: resolution.Host}, nil
	}
	if h.routes != nil {
		decision, ok, err := h.routes.LookupRoute(r, site, r.URL.Path)
		if err != nil {
			return PublicRouteDecision{}, err
		}
		if ok {
			decision.SiteHost = resolution.Host
			return decision, nil
		}
	}
	return PublicRouteDecision{
		Site:     site,
		SiteHost: resolution.Host,
		Kind:     RouteStatic,
		Path:     r.URL.Path,
	}, nil
}

func (h Handler) resolveHost(r *http.Request) (sites.HostResolution, error) {
	if h.hostResolver != nil {
		return h.hostResolver.ResolveHost(r.Context(), r.Host)
	}
	site := sites.NameFromHost(r.Host)
	if site == "" {
		return sites.HostResolution{Status: sites.HostUnmatched, Host: sites.NormalizeHost(r.Host)}, nil
	}
	return sites.HostResolution{Status: sites.HostResolved, Site: site, Host: sites.NormalizeHost(r.Host)}, nil
}

func (h Handler) handleQuackRequest(w http.ResponseWriter, r *http.Request, site string) {
	switch r.URL.Path {
	case "/__quack/info":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		infoReader, ok := h.routes.(SiteInfoReader)
		if !ok {
			http.NotFound(w, r)
			return
		}
		info, ok, err := infoReader.CurrentSiteInfo(r, site)
		if err != nil {
			slog.ErrorContext(r.Context(), "public quack info lookup failed", "host", r.Host, "site", site, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		protocol.WriteJSON(w, http.StatusOK, info)
	default:
		http.NotFound(w, r)
	}
}

func isQuackPath(path string) bool {
	return path == "/__quack" || strings.HasPrefix(path, "/__quack/")
}

func writeUnconfiguredHost(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusMisdirectedRequest)
	_, _ = w.Write([]byte("<!doctype html><title>Unconfigured host</title><h1>Unconfigured host</h1>"))
}

type ReleaseRouteReader struct {
	Releases releases.Service
	Policies policy.Loader
}

func (r ReleaseRouteReader) LookupRoute(req *http.Request, site string, urlPath string) (PublicRouteDecision, bool, error) {
	decision, ok, err := r.Releases.LookupRoute(req.Context(), site, urlPath)
	if err != nil || !ok {
		return PublicRouteDecision{}, ok, err
	}
	if decision.Kind == releases.RouteHTTP || decision.Kind == releases.RouteWebSocket {
		if r.Policies == nil {
			// Keep nil policy loader as deny-by-default. A missing policy
			// dependency must never be interpreted as permission to execute dynamic
			// code.
			reason := "dynamic HTTP routes are disabled by administrator policy"
			if decision.Kind == releases.RouteWebSocket {
				reason = "dynamic WebSocket routes are disabled by administrator policy"
			}
			return PublicRouteDecision{
				Site: decision.Site, Version: decision.Version, Kind: RouteKind(decision.Kind), Path: decision.Path, RoutePath: decision.RoutePath, StaticRoot: decision.StaticRoot, StaticFile: decision.StaticFile, Methods: append([]string(nil), decision.Methods...), ResourceLimits: decision.ResourceLimits, ExposeRuntimeErrors: decision.ExposeRuntimeErrors, DeniedReason: reason,
			}, true, nil
		}
		// This is the route-level gate before runtimehttp. The runtime service
		// repeats capability evaluation immediately before invoking the executor so
		// cached route decisions cannot outlive a policy change.
		var allowed bool
		var reason string
		var err error
		if decision.Kind == releases.RouteWebSocket {
			allowed, reason, err = policy.RuntimeWebSocketAllowed(req.Context(), r.Policies, site)
		} else {
			allowed, reason, err = policy.RuntimeHTTPAllowed(req.Context(), r.Policies, site)
		}
		if err != nil {
			return PublicRouteDecision{}, false, err
		}
		if !allowed {
			return PublicRouteDecision{
				Site: decision.Site, Version: decision.Version, Kind: RouteKind(decision.Kind), Path: decision.Path, RoutePath: decision.RoutePath, StaticRoot: decision.StaticRoot, StaticFile: decision.StaticFile, Methods: append([]string(nil), decision.Methods...), ResourceLimits: decision.ResourceLimits, ExposeRuntimeErrors: decision.ExposeRuntimeErrors, DeniedReason: reason,
			}, true, nil
		}
	}
	return PublicRouteDecision{
		Site: decision.Site, Version: decision.Version, Kind: RouteKind(decision.Kind), Path: decision.Path, RoutePath: decision.RoutePath, StaticRoot: decision.StaticRoot, StaticFile: decision.StaticFile, Methods: append([]string(nil), decision.Methods...), ResourceLimits: decision.ResourceLimits, ExposeRuntimeErrors: decision.ExposeRuntimeErrors,
	}, true, nil
}

func (r ReleaseRouteReader) CurrentSiteInfo(req *http.Request, site string) (SiteInfo, bool, error) {
	if r.Releases == nil {
		return SiteInfo{}, false, nil
	}
	decision, ok, err := r.Releases.LookupRoute(req.Context(), site, "/")
	if err != nil || !ok || decision.Version == 0 {
		return SiteInfo{}, false, err
	}
	return SiteInfo{Site: site, Version: decision.Version}, true, nil
}

func methodAllowed(method string, methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if strings.EqualFold(method, candidate) {
			return true
		}
	}
	return false
}
