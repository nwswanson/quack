package server

import (
	"context"
	"fmt"
	"strconv"
)

type EffectiveValue[T any] struct {
	Value    T
	Source   string
	Editable bool
	Reason   string
}

type UploadPolicy struct {
	MaxUploadBytes      EffectiveValue[int64]
	MaxUploadFiles      EffectiveValue[int64]
	MaxRetainedVersions EffectiveValue[int64]
}

type SiteRuntimeStatus string

const (
	SiteRuntimeActive            SiteRuntimeStatus = "active"
	SiteRuntimeDegraded          SiteRuntimeStatus = "degraded"
	SiteRuntimeSuspendedByPolicy SiteRuntimeStatus = "suspended_by_policy"
)

type SiteRuntimeDecision struct {
	Status SiteRuntimeStatus
	Reason string
}

type Resolver struct {
	db    Database
	cache HotDataCache
}

func NewResolver(db Database, caches ...HotDataCache) Resolver {
	var cache HotDataCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	if cache == nil {
		cache = NewPassthroughHotDataCache(db)
	}
	return Resolver{db: db, cache: cache}
}

func (r Resolver) hotDataCache() HotDataCache {
	if r.cache != nil {
		return r.cache
	}
	return NewPassthroughHotDataCache(r.db)
}

func (r Resolver) ResolveUploadPolicy(ctx context.Context, actor AdminUser, site string) (UploadPolicy, error) {
	settings, err := r.hotDataCache().GetServerSettings(ctx)
	if err != nil {
		return UploadPolicy{}, err
	}
	return UploadPolicy{
		MaxUploadBytes:      EffectiveValue[int64]{Value: settings.MaxUploadBytes, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
		MaxUploadFiles:      EffectiveValue[int64]{Value: settings.MaxUploadFiles, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
		MaxRetainedVersions: EffectiveValue[int64]{Value: settings.MaxRetainedVersions, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
	}, nil
}

func (r Resolver) ValidateUploadManifest(ctx context.Context, actor AdminUser, site string, manifest SiteManifest) error {
	allowed, reason, err := r.databaseAllowed(ctx, actor, site)
	if err != nil {
		return err
	}
	if manifest.Features.Database.Enabled && !allowed {
		if reason == "" {
			reason = "database is disabled by administrator policy"
		}
		return forbiddenPolicyError{err: fmt.Errorf("%s", reason)}
	}
	return nil
}

func (r Resolver) ResolveCurrentSiteRuntime(ctx context.Context, site string) (SiteRuntimeDecision, error) {
	manifests, err := r.hotDataCache().ListCurrentSiteManifests(ctx)
	if err != nil {
		return SiteRuntimeDecision{}, err
	}
	for _, manifest := range manifests {
		if manifest.Site != site {
			continue
		}
		violations, err := r.hotDataCache().ListPolicyViolations(ctx, manifest.SiteSHA, manifest.Version)
		if err != nil {
			return SiteRuntimeDecision{}, err
		}
		decision := SiteRuntimeDecision{Status: SiteRuntimeActive}
		for _, violation := range violations {
			if violation.Key != SettingDatabaseFeature {
				continue
			}
			if violation.Severity == "suspended" {
				return SiteRuntimeDecision{Status: SiteRuntimeSuspendedByPolicy, Reason: violation.Reason}, nil
			}
			if decision.Status == SiteRuntimeActive {
				decision = SiteRuntimeDecision{Status: SiteRuntimeDegraded, Reason: violation.Reason}
			}
		}
		return decision, nil
	}
	return SiteRuntimeDecision{Status: SiteRuntimeActive}, nil
}

func (r Resolver) ReconcilePolicyViolations(ctx context.Context) error {
	manifests, err := r.hotDataCache().ListCurrentSiteManifests(ctx)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		enabled := parseBoolSetting(manifest.Settings[SettingDatabaseFeature])
		required := parseBoolSetting(manifest.Settings[SettingDatabaseFeatureRequired])
		allowed, reason, err := r.databaseAllowed(ctx, AdminUser{}, manifest.Site)
		if err != nil {
			return err
		}
		if enabled && !allowed {
			severity := "degraded"
			if required {
				severity = "suspended"
			}
			if reason == "" {
				reason = "database is disabled by administrator policy"
			}
			if err := r.db.SavePolicyViolation(ctx, PolicyViolation{
				SiteSHA: manifest.SiteSHA, UploadVersion: manifest.Version, Key: SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: severity, Reason: reason,
			}); err != nil {
				return err
			}
			continue
		}
		if err := r.db.ResolvePolicyViolation(ctx, manifest.SiteSHA, manifest.Version, SettingDatabaseFeature); err != nil {
			return err
		}
	}
	return nil
}

func (r Resolver) databaseAllowed(ctx context.Context, actor AdminUser, site string) (bool, string, error) {
	scopes := []PolicyScope{{Type: ScopeSystem, ID: ""}}
	if actor.ID > 0 {
		scopes = append(scopes, PolicyScope{Type: ScopeUser, ID: strconv.FormatInt(actor.ID, 10)})
	}
	if site != "" {
		scopes = append(scopes, PolicyScope{Type: ScopeSite, ID: site})
	}
	policies, err := r.hotDataCache().LoadPolicies(ctx, scopes)
	if err != nil {
		return false, "", err
	}
	allowed := parseBoolSetting(settingDefault(SettingDatabaseFeature))
	reason := ""
	for _, policy := range policies {
		if policy.Key != SettingDatabaseFeature {
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

type forbiddenPolicyError struct {
	err error
}

func (e forbiddenPolicyError) Error() string {
	return e.err.Error()
}

func (e forbiddenPolicyError) Unwrap() error {
	return e.err
}
