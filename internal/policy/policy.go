package policy

import (
	"context"
	"strconv"

	"quack/internal/domain"
	"quack/internal/manifest"
	appsettings "quack/internal/settings"
)

const (
	CapabilityDatabase = "database"
	// CapabilityRuntimeHTTP gates dynamic HTTP route declaration and invocation.
	//
	// Future runtime capabilities should use separate keys for network access,
	// secrets, writable temp storage, and database privileges. Do not let
	// "runtime.http" become a broad permission to do everything user code might
	// eventually request.
	CapabilityRuntimeHTTP      = "runtime.http"
	CapabilityRuntimeWebSocket = "runtime.websocket"
)

type CapabilityRequest struct {
	Key      string
	Required bool
	Value    string
}

type Evaluation struct {
	Allowed    bool
	Violations []Violation
}

type Violation struct {
	Key      string
	Reason   string
	Required bool
}

type Loader interface {
	LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error)
}

func RequestsFromManifest(siteManifest manifest.Manifest) []CapabilityRequest {
	var out []CapabilityRequest
	if siteManifest.Features.Database.Enabled {
		out = append(out, CapabilityRequest{
			Key:      CapabilityDatabase,
			Required: siteManifest.Features.Database.Required,
			Value:    "true",
		})
	}
	seenRuntimeHTTP := false
	seenRuntimeWebSocket := false
	for _, route := range siteManifest.Routes {
		switch route.Kind {
		case manifest.RouteHTTP:
			if !seenRuntimeHTTP {
				// This gates declaration and invocation of HTTP runtime routes.
				out = append(out, CapabilityRequest{Key: CapabilityRuntimeHTTP, Required: true, Value: "true"})
				seenRuntimeHTTP = true
			}
		case manifest.RouteWebSocket:
			if !seenRuntimeWebSocket {
				out = append(out, CapabilityRequest{Key: CapabilityRuntimeWebSocket, Required: true, Value: "true"})
				seenRuntimeWebSocket = true
			}
		}
	}
	return out
}

func DatabaseAllowed(ctx context.Context, loader Loader, actor domain.AdminUser, site string) (bool, string, error) {
	policies, err := loader.LoadPolicies(ctx, ScopesFor(actor, site))
	if err != nil {
		return false, "", err
	}
	return DatabaseAllowedByRecords(policies)
}

func ScopesFor(actor domain.AdminUser, site string) []domain.PolicyScope {
	scopes := []domain.PolicyScope{{Type: domain.ScopeSystem, ID: ""}}
	if actor.ID > 0 {
		scopes = append(scopes, domain.PolicyScope{Type: domain.ScopeUser, ID: strconv.FormatInt(actor.ID, 10)})
	}
	if site != "" {
		scopes = append(scopes, domain.PolicyScope{Type: domain.ScopeSite, ID: site})
	}
	return scopes
}

func DatabaseAllowedByRecords(policies []domain.PolicyRecord) (bool, string, error) {
	return capabilityAllowedByRecords(policies, appsettings.SettingDatabaseFeature, "database is disabled by administrator policy")
}

func RuntimeHTTPAllowed(ctx context.Context, loader Loader, site string) (bool, string, error) {
	// Invocation-time checks may eventually need actor/request context in addition
	// to site. For public unauthenticated requests, keep the current system+site
	// scopes unless a specific user identity model is introduced.
	policies, err := loader.LoadPolicies(ctx, ScopesFor(domain.AdminUser{}, site))
	if err != nil {
		return false, "", err
	}
	return RuntimeHTTPAllowedByRecords(policies)
}

func RuntimeHTTPAllowedByRecords(policies []domain.PolicyRecord) (bool, string, error) {
	return capabilityAllowedByRecords(policies, appsettings.SettingRuntimeHTTPFeature, "dynamic HTTP routes are disabled by administrator policy")
}

func RuntimeWebSocketAllowed(ctx context.Context, loader Loader, site string) (bool, string, error) {
	policies, err := loader.LoadPolicies(ctx, ScopesFor(domain.AdminUser{}, site))
	if err != nil {
		return false, "", err
	}
	return RuntimeWebSocketAllowedByRecords(policies)
}

func RuntimeWebSocketAllowedByRecords(policies []domain.PolicyRecord) (bool, string, error) {
	return capabilityAllowedByRecords(policies, appsettings.SettingRuntimeWebSocketFeature, "dynamic WebSocket routes are disabled by administrator policy")
}

func Evaluate(policies []domain.PolicyRecord, requests []CapabilityRequest) Evaluation {
	eval := Evaluation{Allowed: true}
	for _, req := range requests {
		var allowed bool
		var reason string
		switch req.Key {
		case CapabilityDatabase:
			allowed, reason, _ = DatabaseAllowedByRecords(policies)
		case CapabilityRuntimeHTTP:
			allowed, reason, _ = RuntimeHTTPAllowedByRecords(policies)
		case CapabilityRuntimeWebSocket:
			allowed, reason, _ = RuntimeWebSocketAllowedByRecords(policies)
		default:
			continue
		}
		if allowed {
			continue
		}
		eval.Violations = append(eval.Violations, Violation{Key: req.Key, Required: req.Required, Reason: reason})
		if req.Required {
			eval.Allowed = false
		}
	}
	return eval
}

func capabilityAllowedByRecords(policies []domain.PolicyRecord, key string, defaultReason string) (bool, string, error) {
	allowed := appsettings.ParseBool(appsettings.Default(key))
	reason := ""
	for _, policy := range policies {
		if policy.Key != key {
			continue
		}
		switch policy.Mode {
		case "deny", "force_off":
			if policy.Reason != "" {
				reason = policy.Reason
			} else {
				reason = defaultReason
			}
			return false, reason, nil
		case "allow", "force_on":
			allowed = true
			if policy.Reason != "" {
				reason = policy.Reason
			}
		}
	}
	if !allowed && reason == "" {
		reason = defaultReason
	}
	return allowed, reason, nil
}

func DatabaseViolationFromSettings(settings map[string]string, allowed bool, reason string) (domain.PolicyViolation, bool) {
	enabled := appsettings.ParseBool(settings[appsettings.SettingDatabaseFeature])
	if !enabled || allowed {
		return domain.PolicyViolation{}, false
	}
	required := appsettings.ParseBool(settings[appsettings.SettingDatabaseFeatureRequired])
	if reason == "" {
		reason = "database is disabled by administrator policy"
	}
	severity := "degraded"
	if required {
		severity = "suspended"
	}
	return domain.PolicyViolation{
		Key:            appsettings.SettingDatabaseFeature,
		RequestedValue: "true",
		PolicyValue:    "deny",
		Severity:       severity,
		Reason:         reason,
	}, true
}
