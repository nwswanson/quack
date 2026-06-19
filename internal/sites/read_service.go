package sites

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"quack/internal/domain"
	"quack/internal/protocol"
	appsettings "quack/internal/settings"
)

type HotDataReader interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
	ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error)
}

type SiteReadService interface {
	ServerSettings(ctx context.Context) (domain.ServerSettings, error)
	UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error)
	ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest protocol.SiteManifest) error
	CurrentSiteRuntime(ctx context.Context, site string) (domain.SiteRuntimeDecision, error)
	CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error)
	SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error)
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
	File         domain.UploadFileRecord
	Runtime      domain.SiteRuntimeDecision
}

type siteReadService struct {
	hot HotDataReader
}

func NewSiteReadService(hot HotDataReader) SiteReadService {
	return siteReadService{hot: hot}
}

func (s siteReadService) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return s.hot.GetServerSettings(ctx)
}

func (s siteReadService) UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error) {
	settings, err := s.hot.GetServerSettings(ctx)
	if err != nil {
		return domain.UploadPolicy{}, err
	}
	return domain.UploadPolicy{
		MaxUploadBytes:      domain.EffectiveValue[int64]{Value: settings.MaxUploadBytes, Source: "server_settings", Editable: can(actor, "server.settings.edit")},
		MaxUploadFiles:      domain.EffectiveValue[int64]{Value: settings.MaxUploadFiles, Source: "server_settings", Editable: can(actor, "server.settings.edit")},
		MaxRetainedVersions: domain.EffectiveValue[int64]{Value: settings.MaxRetainedVersions, Source: "server_settings", Editable: can(actor, "server.settings.edit")},
	}, nil
}

func (s siteReadService) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest protocol.SiteManifest) error {
	allowed, reason, err := databaseAllowed(ctx, s.hot, actor, site)
	if err != nil {
		return err
	}
	if manifest.Features.Database.Enabled && !allowed {
		if reason == "" {
			reason = "database is disabled by administrator policy"
		}
		return ForbiddenPolicyError{err: fmt.Errorf("%s", reason)}
	}
	return nil
}

func (s siteReadService) CurrentSiteRuntime(ctx context.Context, site string) (domain.SiteRuntimeDecision, error) {
	return currentSiteRuntime(ctx, s.hot, site)
}

func (s siteReadService) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return s.hot.FindCurrentSiteFile(ctx, site, relativePath)
}

func (s siteReadService) ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error) {
	return s.hot.ServeSiteFile(ctx, site, urlPath)
}

type SiteFileResolver interface {
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
}

func ResolveSiteFile(ctx context.Context, hot SiteFileResolver, site string, urlPath string, defaultSite string, usingDefault bool) (ServeSiteFileDecision, error) {
	decision, err := currentSiteRuntime(ctx, hot, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if decision.Status == domain.SiteRuntimeSuspendedByPolicy {
		return ServeSiteFileDecision{Status: ServeSiteFileSuspended, Site: site, Runtime: decision}, nil
	}

	files, siteExists, err := hot.ListCurrentSiteFiles(ctx, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if !siteExists && !usingDefault && defaultSite != "" && defaultSite != site {
		return ResolveSiteFile(ctx, hot, defaultSite, urlPath, defaultSite, true)
	}

	fileByPath := make(map[string]domain.UploadFileRecord, len(files))
	for _, file := range files {
		fileByPath[file.RelativePath] = file
	}

	relativePath, wantsIndex := RequestedRelativePath(urlPath)
	if file, ok := fileByPath[relativePath]; ok {
		return ServeSiteFileDecision{
			Status:       ServeSiteFileFound,
			Site:         site,
			RelativePath: relativePath,
			File:         file,
			Runtime:      decision,
		}, nil
	}

	if ShouldTryDirectoryIndex(urlPath, relativePath, wantsIndex) {
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

func currentSiteRuntime(ctx context.Context, hot SiteFileResolver, site string) (domain.SiteRuntimeDecision, error) {
	manifests, err := hot.ListCurrentSiteManifests(ctx)
	if err != nil {
		return domain.SiteRuntimeDecision{}, err
	}
	for _, manifest := range manifests {
		if manifest.Site != site {
			continue
		}
		violations, err := hot.ListPolicyViolations(ctx, manifest.SiteSHA, manifest.Version)
		if err != nil {
			return domain.SiteRuntimeDecision{}, err
		}
		return runtimeDecisionFromViolations(violations), nil
	}
	return domain.SiteRuntimeDecision{Status: domain.SiteRuntimeActive}, nil
}

func (s siteReadService) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	policies, err := s.hot.LoadPolicies(ctx, []domain.PolicyScope{{Type: domain.ScopeSystem, ID: ""}})
	if err != nil {
		return domain.PolicyRecord{}, err
	}
	policy := domain.PolicyRecord{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "inherit"}
	for _, p := range policies {
		if p.Key == appsettings.SettingDatabaseFeature {
			policy = p
			break
		}
	}
	return policy, nil
}

func databaseAllowed(ctx context.Context, hot HotDataReader, actor domain.AdminUser, site string) (bool, string, error) {
	scopes := []domain.PolicyScope{{Type: domain.ScopeSystem, ID: ""}}
	if actor.ID > 0 {
		scopes = append(scopes, domain.PolicyScope{Type: domain.ScopeUser, ID: strconv.FormatInt(actor.ID, 10)})
	}
	if site != "" {
		scopes = append(scopes, domain.PolicyScope{Type: domain.ScopeSite, ID: site})
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

func DatabaseAllowed(ctx context.Context, hot HotDataReader, actor domain.AdminUser, site string) (bool, string, error) {
	return databaseAllowed(ctx, hot, actor, site)
}

func runtimeDecisionFromViolations(violations []domain.PolicyViolation) domain.SiteRuntimeDecision {
	decision := domain.SiteRuntimeDecision{Status: domain.SiteRuntimeActive}
	for _, violation := range violations {
		if violation.Key != appsettings.SettingDatabaseFeature {
			continue
		}
		if violation.Severity == "suspended" {
			return domain.SiteRuntimeDecision{Status: domain.SiteRuntimeSuspendedByPolicy, Reason: violation.Reason}
		}
		if decision.Status == domain.SiteRuntimeActive {
			decision = domain.SiteRuntimeDecision{Status: domain.SiteRuntimeDegraded, Reason: violation.Reason}
		}
	}
	return decision
}

func can(user domain.AdminUser, action string) bool {
	if user.IsAdmin() {
		return true
	}
	switch action {
	case "sites.upload", "sites.delete":
		return user.ID > 0
	default:
		return false
	}
}

type ForbiddenPolicyError struct {
	err error
}

func (e ForbiddenPolicyError) Error() string {
	return e.err.Error()
}

func (e ForbiddenPolicyError) Unwrap() error {
	return e.err
}
