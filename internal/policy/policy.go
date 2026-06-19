package policy

import (
	"context"
	"strconv"

	"quack/internal/domain"
	"quack/internal/manifest"
	appsettings "quack/internal/settings"
)

const CapabilityDatabase = "database"

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

func RequestsFromManifest(manifest manifest.Manifest) []CapabilityRequest {
	if !manifest.Features.Database.Enabled {
		return nil
	}
	return []CapabilityRequest{{
		Key:      CapabilityDatabase,
		Required: manifest.Features.Database.Required,
		Value:    "true",
	}}
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
	allowed := appsettings.ParseBool(appsettings.Default(appsettings.SettingDatabaseFeature))
	reason := ""
	for _, policy := range policies {
		if policy.Key != appsettings.SettingDatabaseFeature {
			continue
		}
		switch policy.Mode {
		case "deny", "force_off":
			if policy.Reason != "" {
				reason = policy.Reason
			} else {
				reason = "database is disabled by administrator policy"
			}
			return false, reason, nil
		case "allow", "force_on":
			allowed = true
			if policy.Reason != "" {
				reason = policy.Reason
			}
		}
	}
	return allowed, reason, nil
}

func Evaluate(policies []domain.PolicyRecord, requests []CapabilityRequest) Evaluation {
	eval := Evaluation{Allowed: true}
	for _, req := range requests {
		switch req.Key {
		case CapabilityDatabase:
			allowed, reason, _ := DatabaseAllowedByRecords(policies)
			if allowed {
				continue
			}
			if reason == "" {
				reason = "database is disabled by administrator policy"
			}
			eval.Violations = append(eval.Violations, Violation{Key: req.Key, Required: req.Required, Reason: reason})
			if req.Required {
				eval.Allowed = false
			}
		}
	}
	return eval
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
