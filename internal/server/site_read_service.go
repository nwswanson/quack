package server

import (
	"context"
	"fmt"
	"path"
	"strconv"

	appsettings "quack/internal/settings"
)

type SiteReadService interface {
	ServerSettings(ctx context.Context) (ServerSettings, error)
	UploadPolicy(ctx context.Context, actor AdminUser, site string) (UploadPolicy, error)
	ValidateUploadManifest(ctx context.Context, actor AdminUser, site string, manifest SiteManifest) error
	CurrentSiteRuntime(ctx context.Context, site string) (SiteRuntimeDecision, error)
	CurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error)
	ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error)
	SystemDatabasePolicy(ctx context.Context) (PolicyRecord, error)
}

type ServeSiteFileStatus string

const (
	ServeSiteFileFound             ServeSiteFileStatus = "found"
	ServeSiteFileDirectoryRedirect ServeSiteFileStatus = "directory_redirect"
	ServeSiteFileEmptyIndex        ServeSiteFileStatus = "empty_index"
	ServeSiteFileNotFound          ServeSiteFileStatus = "not_found"
	ServeSiteFileSuspended         ServeSiteFileStatus = "suspended"
)

type ServeSiteFileDecision struct {
	Status       ServeSiteFileStatus
	Site         string
	RelativePath string
	File         UploadFileRecord
	Runtime      SiteRuntimeDecision
}

type siteReadService struct {
	hot HotDataReader
}

func NewSiteReadService(hot HotDataReader) SiteReadService {
	return siteReadService{hot: hot}
}

func (s siteReadService) ServerSettings(ctx context.Context) (ServerSettings, error) {
	return s.hot.GetServerSettings(ctx)
}

func (s siteReadService) UploadPolicy(ctx context.Context, actor AdminUser, site string) (UploadPolicy, error) {
	settings, err := s.hot.GetServerSettings(ctx)
	if err != nil {
		return UploadPolicy{}, err
	}
	return UploadPolicy{
		MaxUploadBytes:      EffectiveValue[int64]{Value: settings.MaxUploadBytes, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
		MaxUploadFiles:      EffectiveValue[int64]{Value: settings.MaxUploadFiles, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
		MaxRetainedVersions: EffectiveValue[int64]{Value: settings.MaxRetainedVersions, Source: "server_settings", Editable: Can(actor, "server.settings.edit")},
	}, nil
}

func (s siteReadService) ValidateUploadManifest(ctx context.Context, actor AdminUser, site string, manifest SiteManifest) error {
	allowed, reason, err := databaseAllowed(ctx, s.hot, actor, site)
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

func (s siteReadService) CurrentSiteRuntime(ctx context.Context, site string) (SiteRuntimeDecision, error) {
	return currentSiteRuntime(ctx, s.hot, site)
}

func (s siteReadService) CurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return s.hot.FindCurrentSiteFile(ctx, site, relativePath)
}

func (s siteReadService) ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error) {
	return s.hot.ServeSiteFile(ctx, site, urlPath)
}

func resolveSiteFile(ctx context.Context, hot HotDataReader, site string, urlPath string, defaultSite string, usingDefault bool) (ServeSiteFileDecision, error) {
	decision, err := currentSiteRuntime(ctx, hot, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if decision.Status == SiteRuntimeSuspendedByPolicy {
		return ServeSiteFileDecision{Status: ServeSiteFileSuspended, Site: site, Runtime: decision}, nil
	}

	files, siteExists, err := hot.ListCurrentSiteFiles(ctx, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if !siteExists && !usingDefault && defaultSite != "" && defaultSite != site {
		return resolveSiteFile(ctx, hot, defaultSite, urlPath, defaultSite, true)
	}

	fileByPath := make(map[string]UploadFileRecord, len(files))
	for _, file := range files {
		fileByPath[file.RelativePath] = file
	}

	relativePath, wantsIndex := requestedRelativePath(urlPath)
	if file, ok := fileByPath[relativePath]; ok {
		return ServeSiteFileDecision{
			Status:       ServeSiteFileFound,
			Site:         site,
			RelativePath: relativePath,
			File:         file,
			Runtime:      decision,
		}, nil
	}

	if shouldTryDirectoryIndex(urlPath, relativePath, wantsIndex) {
		indexPath := path.Join(relativePath, "index.html")
		if _, ok := fileByPath[indexPath]; ok {
			return ServeSiteFileDecision{
				Status:       ServeSiteFileDirectoryRedirect,
				Site:         site,
				RelativePath: indexPath,
				Runtime:      decision,
			}, nil
		}
	}

	if wantsIndex {
		return ServeSiteFileDecision{Status: ServeSiteFileEmptyIndex, Site: site, RelativePath: relativePath, Runtime: decision}, nil
	}
	return ServeSiteFileDecision{Status: ServeSiteFileNotFound, Site: site, RelativePath: relativePath, Runtime: decision}, nil
}

func currentSiteRuntime(ctx context.Context, hot HotDataReader, site string) (SiteRuntimeDecision, error) {
	manifests, err := hot.ListCurrentSiteManifests(ctx)
	if err != nil {
		return SiteRuntimeDecision{}, err
	}
	for _, manifest := range manifests {
		if manifest.Site != site {
			continue
		}
		violations, err := hot.ListPolicyViolations(ctx, manifest.SiteSHA, manifest.Version)
		if err != nil {
			return SiteRuntimeDecision{}, err
		}
		return runtimeDecisionFromViolations(violations), nil
	}
	return SiteRuntimeDecision{Status: SiteRuntimeActive}, nil
}

func (s siteReadService) SystemDatabasePolicy(ctx context.Context) (PolicyRecord, error) {
	policies, err := s.hot.LoadPolicies(ctx, []PolicyScope{{Type: ScopeSystem, ID: ""}})
	if err != nil {
		return PolicyRecord{}, err
	}
	policy := PolicyRecord{ScopeType: ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "inherit"}
	for _, p := range policies {
		if p.Key == appsettings.SettingDatabaseFeature {
			policy = p
			break
		}
	}
	return policy, nil
}

func databaseAllowed(ctx context.Context, hot HotDataReader, actor AdminUser, site string) (bool, string, error) {
	scopes := []PolicyScope{{Type: ScopeSystem, ID: ""}}
	if actor.ID > 0 {
		scopes = append(scopes, PolicyScope{Type: ScopeUser, ID: strconv.FormatInt(actor.ID, 10)})
	}
	if site != "" {
		scopes = append(scopes, PolicyScope{Type: ScopeSite, ID: site})
	}
	policies, err := hot.LoadPolicies(ctx, scopes)
	if err != nil {
		return false, "", err
	}
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

func runtimeDecisionFromViolations(violations []PolicyViolation) SiteRuntimeDecision {
	decision := SiteRuntimeDecision{Status: SiteRuntimeActive}
	for _, violation := range violations {
		if violation.Key != appsettings.SettingDatabaseFeature {
			continue
		}
		if violation.Severity == "suspended" {
			return SiteRuntimeDecision{Status: SiteRuntimeSuspendedByPolicy, Reason: violation.Reason}
		}
		if decision.Status == SiteRuntimeActive {
			decision = SiteRuntimeDecision{Status: SiteRuntimeDegraded, Reason: violation.Reason}
		}
	}
	return decision
}
