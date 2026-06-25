package runtime

import (
	"context"
	"fmt"
	"quack/internal/domain"
	"quack/internal/policy"
)

func (s *service) checkCapabilities(ctx context.Context, route RouteMetadata) error {
	if s.policies == nil {
		return ErrCapabilityDenied
	}
	policies, err := s.policies.LoadPolicies(ctx, policy.ScopesFor(domain.AdminUser{}, route.Site))
	if err != nil {
		return err
	}
	requests, err := capabilityRequests(requiredCapabilities(route))
	if err != nil {
		return err
	}
	eval := policy.Evaluate(policies, requests)
	if eval.Allowed {
		return nil
	}
	if len(eval.Violations) > 0 && eval.Violations[0].Reason != "" {
		return fmt.Errorf("%w: %s", ErrCapabilityDenied, eval.Violations[0].Reason)
	}
	return ErrCapabilityDenied
}
func requiredCapabilities(route RouteMetadata) []string {
	capabilities := append([]string(nil), route.RequiredCapabilities...)
	if route.RouteKind == RouteHTTP && !containsCapability(capabilities, policy.CapabilityRuntimeHTTP) {
		capabilities = append(capabilities, policy.CapabilityRuntimeHTTP)
	}
	if route.RouteKind == RouteWebSocket && !containsCapability(capabilities, policy.CapabilityRuntimeWebSocket) {
		capabilities = append(capabilities, policy.CapabilityRuntimeWebSocket)
	}
	return capabilities
}
func capabilityRequests(capabilities []string) ([]policy.CapabilityRequest, error) {
	requests := make([]policy.CapabilityRequest, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability != policy.CapabilityRuntimeHTTP &&
			capability != policy.CapabilityRuntimeHTTPClient &&
			capability != policy.CapabilityRuntimeWebSocket &&
			capability != policy.CapabilityHardwareCamera {
			return nil, fmt.Errorf("%w: unsupported runtime capability %s", ErrCapabilityDenied, capability)
		}
		requests = append(requests, policy.CapabilityRequest{Key: capability, Required: true, Value: "true"})
	}
	return requests, nil
}
func containsCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
