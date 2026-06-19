package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

type adminHostRouter struct {
	adminHost string
	admin     http.Handler
	site      http.Handler
}

func (r adminHostRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if isAdminPath(req.URL.Path) {
		if r.adminHost != "" && !r.isAdminHost(req.Host) {
			http.NotFound(w, req)
			return
		}
		r.admin.ServeHTTP(w, req)
		return
	}
	if r.isAdminHost(req.Host) {
		r.admin.ServeHTTP(w, req)
		return
	}
	r.site.ServeHTTP(w, req)
}

func (r adminHostRouter) isAdminHost(host string) bool {
	return r.adminHost != "" && normalizeAdminHost(host) == r.adminHost
}

func isAdminPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func normalizeAdminHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			value = parsed.Host
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, ".")
}
