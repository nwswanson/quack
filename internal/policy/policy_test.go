package policy

import (
	"context"
	"testing"

	"quack/internal/domain"
	"quack/internal/manifest"
	appsettings "quack/internal/settings"
)

func TestEvaluateOptionalCapabilityViolationAllowsWithDegradedViolation(t *testing.T) {
	eval := Evaluate([]domain.PolicyRecord{{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingDatabaseFeature,
		Mode:      "deny",
		Reason:    "disabled",
	}}, []CapabilityRequest{{Key: CapabilityDatabase, Required: false, Value: "true"}})

	if !eval.Allowed {
		t.Fatal("optional capability violation should not block evaluation")
	}
	if len(eval.Violations) != 1 || eval.Violations[0].Key != CapabilityDatabase || eval.Violations[0].Reason != "disabled" {
		t.Fatalf("violations = %+v, want database disabled violation", eval.Violations)
	}
}

func TestEvaluateRequiredCapabilityViolationBlocks(t *testing.T) {
	eval := Evaluate([]domain.PolicyRecord{{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingDatabaseFeature,
		Mode:      "deny",
	}}, []CapabilityRequest{{Key: CapabilityDatabase, Required: true, Value: "true"}})

	if eval.Allowed {
		t.Fatal("required capability violation should block evaluation")
	}
	if len(eval.Violations) != 1 || !eval.Violations[0].Required {
		t.Fatalf("violations = %+v, want required violation", eval.Violations)
	}
}

func TestEvaluateRuntimeHTTPDefaultsToDenied(t *testing.T) {
	eval := Evaluate(nil, []CapabilityRequest{{Key: CapabilityRuntimeHTTP, Required: true, Value: "true"}})

	if eval.Allowed {
		t.Fatal("runtime HTTP should default to denied")
	}
	if len(eval.Violations) != 1 || eval.Violations[0].Key != CapabilityRuntimeHTTP {
		t.Fatalf("violations = %+v, want runtime HTTP violation", eval.Violations)
	}
}

func TestEvaluateRuntimeHTTPAllowsExplicitPolicy(t *testing.T) {
	eval := Evaluate([]domain.PolicyRecord{{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingRuntimeHTTPFeature,
		Mode:      "allow",
	}}, []CapabilityRequest{{Key: CapabilityRuntimeHTTP, Required: true, Value: "true"}})

	if !eval.Allowed || len(eval.Violations) != 0 {
		t.Fatalf("evaluation = %+v, want allowed runtime HTTP", eval)
	}
}

func TestEvaluateRuntimeWebSocketDefaultsToDenied(t *testing.T) {
	eval := Evaluate(nil, []CapabilityRequest{{Key: CapabilityRuntimeWebSocket, Required: true, Value: "true"}})

	if eval.Allowed {
		t.Fatal("runtime websocket should default to denied")
	}
	if len(eval.Violations) != 1 || eval.Violations[0].Key != CapabilityRuntimeWebSocket {
		t.Fatalf("violations = %+v, want runtime websocket violation", eval.Violations)
	}
}

func TestEvaluateRuntimeWebSocketAllowsExplicitPolicy(t *testing.T) {
	eval := Evaluate([]domain.PolicyRecord{{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingRuntimeWebSocketFeature,
		Mode:      "allow",
	}}, []CapabilityRequest{{Key: CapabilityRuntimeWebSocket, Required: true, Value: "true"}})

	if !eval.Allowed || len(eval.Violations) != 0 {
		t.Fatalf("evaluation = %+v, want allowed runtime websocket", eval)
	}
}

func TestDatabaseAllowedUsesSystemPolicyAndDefault(t *testing.T) {
	allowed, _, err := DatabaseAllowed(context.Background(), policyLoader{}, domain.AdminUser{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("database should default to disabled")
	}

	allowed, reason, err := DatabaseAllowed(context.Background(), policyLoader{
		policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow", Reason: "ok"}},
	}, domain.AdminUser{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed || reason != "ok" {
		t.Fatalf("allowed = %v reason = %q, want allow ok", allowed, reason)
	}
}

func TestDatabaseAllowedSupportsSiteOverrideScope(t *testing.T) {
	allowed, reason, err := DatabaseAllowed(context.Background(), policyLoader{
		policies: []domain.PolicyRecord{
			{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow"},
			{ScopeType: domain.ScopeSite, ScopeID: "example.com", Key: appsettings.SettingDatabaseFeature, Mode: "deny", Reason: "site disabled"},
		},
	}, domain.AdminUser{}, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if allowed || reason != "site disabled" {
		t.Fatalf("allowed = %v reason = %q, want site denial", allowed, reason)
	}
}

func TestRequestsFromManifestConvertsDatabaseFeature(t *testing.T) {
	requests := RequestsFromManifest(manifest.Manifest{
		Features: manifest.Features{Database: manifest.FeatureFlag{Enabled: true, Required: true}},
	})
	if len(requests) != 1 || requests[0].Key != CapabilityDatabase || !requests[0].Required {
		t.Fatalf("requests = %+v, want required database request", requests)
	}
}

func TestRequestsFromManifestConvertsHTTPRoutes(t *testing.T) {
	requests := RequestsFromManifest(manifest.Manifest{
		Routes: []manifest.Route{{Path: "/api", Kind: manifest.RouteHTTP}},
	})
	if len(requests) != 1 || requests[0].Key != CapabilityRuntimeHTTP || !requests[0].Required {
		t.Fatalf("requests = %+v, want required runtime HTTP request", requests)
	}
}

func TestRequestsFromManifestConvertsWebSocketRoutes(t *testing.T) {
	requests := RequestsFromManifest(manifest.Manifest{
		Routes: []manifest.Route{{Path: "/api/somesocket", Kind: manifest.RouteWebSocket}},
	})
	if len(requests) != 1 || requests[0].Key != CapabilityRuntimeWebSocket || !requests[0].Required {
		t.Fatalf("requests = %+v, want required runtime websocket request", requests)
	}
}

func TestDatabaseViolationFromSettingsPreservesSeverity(t *testing.T) {
	violation, ok := DatabaseViolationFromSettings(map[string]string{
		appsettings.SettingDatabaseFeature:         "true",
		appsettings.SettingDatabaseFeatureRequired: "true",
	}, false, "disabled")
	if !ok {
		t.Fatal("expected violation")
	}
	if violation.Key != appsettings.SettingDatabaseFeature || violation.Severity != "suspended" || violation.Reason != "disabled" {
		t.Fatalf("violation = %+v, want suspended database violation", violation)
	}
}

type policyLoader struct {
	policies []domain.PolicyRecord
}

func (l policyLoader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, p := range l.policies {
		for _, scope := range scopes {
			if p.ScopeType == scope.Type && p.ScopeID == scope.ID {
				out = append(out, p)
			}
		}
	}
	return out, nil
}
