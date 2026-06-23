package devserver

import (
	"context"
	"strings"

	"quack/internal/sites"
)

type HostMatchMode string

const (
	HostMatchAny  HostMatchMode = "any"
	HostMatchSite HostMatchMode = "site"
)

type HostResolver struct {
	Site         string
	Mode         HostMatchMode
	AllowedHosts []string
}

func (r HostResolver) ResolveHost(ctx context.Context, host string) (sites.HostResolution, error) {
	normalized := sites.NormalizeHost(host)
	if len(r.AllowedHosts) > 0 && !sites.HostAllowed(normalized, r.AllowedHosts) {
		return sites.HostResolution{Status: sites.HostBlocked, Site: r.Site, Host: normalized, Reason: "host is not allowed in dev mode"}, nil
	}
	switch r.Mode {
	case HostMatchSite:
		site := sites.NameFromHost(normalized)
		if site == "" {
			return sites.HostResolution{Status: sites.HostUnmatched, Host: normalized}, nil
		}
		return sites.HostResolution{Status: sites.HostResolved, Site: site, Host: normalized}, nil
	default:
		return sites.HostResolution{Status: sites.HostResolved, Site: strings.TrimSpace(r.Site), Host: normalized}, nil
	}
}
