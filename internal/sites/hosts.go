package sites

import (
	"context"
	"net"
	"strings"

	"quack/internal/domain"
)

type HostResolutionStatus string

const (
	HostUnmatched HostResolutionStatus = "unmatched"
	HostResolved  HostResolutionStatus = "resolved"
	HostBlocked   HostResolutionStatus = "blocked"
)

type HostResolution struct {
	Status HostResolutionStatus
	Site   string
	Host   string
	Reason string
}

type HostResolver interface {
	ResolveHost(ctx context.Context, host string) (HostResolution, error)
}

type ServerSettingsReader interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
}

type SettingsHostResolver struct {
	Settings ServerSettingsReader
}

func (r SettingsHostResolver) ResolveHost(ctx context.Context, host string) (HostResolution, error) {
	normalized := NormalizeHost(host)
	site := NameFromHost(normalized)
	if site == "" {
		return HostResolution{Status: HostUnmatched, Host: normalized}, nil
	}
	settings := domain.ServerSettings{}
	if r.Settings != nil {
		var err error
		settings, err = r.Settings.GetServerSettings(ctx)
		if err != nil {
			return HostResolution{}, err
		}
	}
	if !HostAllowed(normalized, settings.AllowedHosts) {
		return HostResolution{
			Status: HostBlocked,
			Site:   site,
			Host:   normalized,
			Reason: "host is not configured for this server",
		}, nil
	}
	return HostResolution{Status: HostResolved, Site: site, Host: normalized}, nil
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	} else if strings.Count(host, ":") == 1 {
		host = strings.Split(host, ":")[0]
	}
	return strings.Trim(host, ".")
}

func HostAllowed(host string, allowedHosts []string) bool {
	host = NormalizeHost(host)
	if host == "" || len(allowedHosts) == 0 {
		return false
	}
	for _, allowed := range allowedHosts {
		allowed = strings.Trim(strings.ToLower(strings.TrimSpace(allowed)), ".")
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*.")
			if host != suffix && strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}
