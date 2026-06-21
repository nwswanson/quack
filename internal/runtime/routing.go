package runtime

import (
	"context"
	"strings"
)

func (s *service) lookupRoute(ctx context.Context, req InvocationRequest) (RouteMetadata, error) {
	routes, err := s.repo.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		return RouteMetadata{}, err
	}
	var best RouteMetadata
	for _, route := range routes {
		if httpRouteMatches(route, req) && (best.RoutePath == "" || len(route.RoutePath) > len(best.RoutePath)) {
			best = route
		}
	}
	if best.RoutePath == "" {
		return RouteMetadata{}, ErrRouteNotFound
	}
	return best, nil
}

func (s *service) lookupWebSocketRoute(ctx context.Context, req WebSocketInvocationRequest) (RouteMetadata, error) {
	routes, err := s.repo.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		return RouteMetadata{}, err
	}
	var best RouteMetadata
	for _, route := range routes {
		if websocketRouteMatches(route, req) && (best.RoutePath == "" || len(route.RoutePath) > len(best.RoutePath)) {
			best = route
		}
	}
	if best.RoutePath == "" {
		return RouteMetadata{}, ErrRouteNotFound
	}
	return best, nil
}
func httpRouteMatches(route RouteMetadata, req InvocationRequest) bool {
	return route.Site == req.Site && route.Version == req.Version && route.RouteKind == RouteHTTP && routeMatches(req.Route, route.RoutePath)
}
func websocketRouteMatches(route RouteMetadata, req WebSocketInvocationRequest) bool {
	return route.Site == req.Site && route.Version == req.Version && route.RouteKind == RouteWebSocket && routeMatches(req.Route, route.RoutePath)
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
func routeMatches(urlPath, routePath string) bool {
	cleanRoute := cleanRoutePath(routePath)
	return cleanRoute == "/" || urlPath == cleanRoute || strings.HasPrefix(urlPath, cleanRoute+"/")
}
func pathUnderRoute(urlPath, routePath string) string {
	cleanRoute := cleanRoutePath(routePath)
	if cleanRoute == "/" {
		return nonEmptyPath(urlPath)
	}
	if urlPath == cleanRoute {
		return "/"
	}
	return nonEmptyPath(strings.TrimPrefix(urlPath, cleanRoute))
}
func cleanRoutePath(routePath string) string {
	if route := strings.TrimRight(routePath, "/"); route != "" {
		return route
	}
	return "/"
}
func nonEmptyPath(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
