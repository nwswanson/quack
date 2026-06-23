package releases

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"quack/internal/domain"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
)

type RouteKind string

const (
	RouteStatic    RouteKind = "static"
	RouteHTTP      RouteKind = "http"
	RouteWebSocket RouteKind = "websocket"
)

type RouteDecision struct {
	Site                string
	Version             int64
	Kind                RouteKind
	Path                string
	RoutePath           string
	StaticRoot          string
	StaticFile          string
	Methods             []string
	ResourceLimits      appruntime.ResourceLimits
	ExposeRuntimeErrors bool
}

type RouteSource interface {
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
}

type RuntimeRouteSource interface {
	ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error)
}

func (s service) LookupRoute(ctx context.Context, site string, urlPath string) (RouteDecision, bool, error) {
	if s.routes == nil {
		return RouteDecision{Site: site, Kind: RouteStatic, Path: urlPath}, true, nil
	}
	manifests, err := s.routes.ListCurrentSiteManifests(ctx)
	if err != nil {
		return RouteDecision{}, false, err
	}
	for _, current := range manifests {
		if current.Site != site {
			continue
		}
		routes := routesFromSettings(current.Settings)
		if s.runtimeRoutes != nil {
			// This intentionally reads metadata, not executable bundles. Keep
			// release route lookup as a routing decision only; the runtime service
			// loads and validates the bundle immediately before invocation.
			runtimeRoutes, err := s.runtimeRoutes.ListCurrentRuntimeRoutes(ctx)
			if err != nil {
				return RouteDecision{}, false, err
			}
			routes = append(routes, routesFromRuntimeMetadata(site, current.SiteSHA, current.Version, runtimeRoutes)...)
		}
		route := chooseRoute(urlPath, routes)
		return RouteDecision{Site: site, Version: current.Version, Kind: route.Kind, Path: urlPath, RoutePath: route.RoutePath, StaticRoot: route.StaticRoot, StaticFile: route.StaticFile, Methods: append([]string(nil), route.Methods...), ResourceLimits: route.ResourceLimits, ExposeRuntimeErrors: route.ExposeRuntimeErrors}, true, nil
	}
	return RouteDecision{Site: site, Kind: RouteStatic, Path: urlPath}, true, nil
}

func routesFromSettings(settings map[string]string) []RouteDecision {
	var declared []manifest.Route
	if raw := strings.TrimSpace(settings[appsettings.SettingRoutes]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &declared)
	}
	out := make([]RouteDecision, 0, len(declared))
	for _, route := range declared {
		kind := RouteKind(route.Kind)
		if kind == "" {
			kind = RouteStatic
		}
		out = append(out, RouteDecision{Kind: kind, Path: cleanRoutePath(route.Path), StaticRoot: route.Root, StaticFile: route.File})
	}
	return out
}

func routesFromRuntimeMetadata(site string, siteSHA string, version int64, routes []appruntime.RouteMetadata) []RouteDecision {
	out := make([]RouteDecision, 0, len(routes))
	for _, route := range routes {
		if route.Site != "" && route.Site != site {
			continue
		}
		if route.SiteSHA != siteSHA || route.Version != version {
			continue
		}
		kind := RouteKind(route.RouteKind)
		if kind != RouteHTTP && kind != RouteWebSocket {
			continue
		}
		out = append(out, RouteDecision{
			Site:                site,
			Version:             version,
			Kind:                kind,
			Path:                cleanRoutePath(route.RoutePath),
			Methods:             append([]string(nil), route.Methods...),
			ResourceLimits:      route.ResourceLimits,
			ExposeRuntimeErrors: route.ExposeErrors,
		})
	}
	return out
}

func chooseRoute(urlPath string, routes []RouteDecision) RouteDecision {
	if len(routes) == 0 {
		return RouteDecision{Kind: RouteStatic, Path: urlPath}
	}
	clean := cleanRoutePath(urlPath)
	var best RouteDecision
	for _, route := range routes {
		if !routeMatchesDecision(clean, route) {
			continue
		}
		if best.Path == "" || len(route.Path) > len(best.Path) || samePathRuntimeMetadataTie(route, best) {
			best = route
		}
	}
	if best.Path == "" {
		return RouteDecision{Kind: RouteStatic, Path: urlPath}
	}
	best.RoutePath = best.Path
	best.Path = urlPath
	return best
}

func samePathRuntimeMetadataTie(candidate RouteDecision, current RouteDecision) bool {
	return len(candidate.Path) == len(current.Path) && candidate.Version != 0 && current.Version == 0
}

func routeMatchesDecision(urlPath string, route RouteDecision) bool {
	if route.Kind == RouteStatic && route.StaticFile != "" {
		return urlPath == route.Path
	}
	return routeMatches(urlPath, route.Path)
}

func routeMatches(urlPath string, routePath string) bool {
	if routePath == "/" {
		return true
	}
	return urlPath == routePath || strings.HasPrefix(urlPath, strings.TrimRight(routePath, "/")+"/")
}

func cleanRoutePath(value string) string {
	clean := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if clean == "." {
		return "/"
	}
	return clean
}
