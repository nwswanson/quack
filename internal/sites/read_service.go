package sites

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/policy"
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
}

type SiteReadService interface {
	ServerSettings(ctx context.Context) (domain.ServerSettings, error)
	UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error)
	ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest manifest.Manifest) error
	CurrentSiteServingStatus(ctx context.Context, site string) (domain.SiteServingDecision, error)
	CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ServeSiteFile(ctx context.Context, site string, urlPath string, routePath string, staticRoot string, staticFile string) (ServeSiteFileDecision, error)
	SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error)
	SystemRuntimeHTTPPolicy(ctx context.Context) (domain.PolicyRecord, error)
	SystemRuntimeHTTPClientPolicy(ctx context.Context) (domain.PolicyRecord, error)
	SystemRuntimeWebSocketPolicy(ctx context.Context) (domain.PolicyRecord, error)
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
	Serving      domain.SiteServingDecision
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

func (s siteReadService) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest manifest.Manifest) error {
	policies, err := s.hot.LoadPolicies(ctx, policy.ScopesFor(actor, site))
	if err != nil {
		return err
	}
	eval := policy.Evaluate(policies, policy.RequestsFromManifest(manifest))
	if len(eval.Violations) > 0 {
		return policy.ForbiddenError{Reason: eval.Violations[0].Reason}
	}
	return nil
}

func (s siteReadService) CurrentSiteServingStatus(ctx context.Context, site string) (domain.SiteServingDecision, error) {
	return currentSiteServingStatus(ctx, s.hot, site)
}

func (s siteReadService) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return s.hot.FindCurrentSiteFile(ctx, site, relativePath)
}

func (s siteReadService) ServeSiteFile(ctx context.Context, site string, urlPath string, routePath string, staticRoot string, staticFile string) (ServeSiteFileDecision, error) {
	settings, err := s.hot.GetServerSettings(ctx)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	return ResolveSiteFile(ctx, s.hot, site, urlPath, routePath, staticRoot, staticFile, strings.TrimSpace(settings.DefaultSite), false)
}

type SiteFileResolver interface {
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
}

func ResolveSiteFile(ctx context.Context, hot SiteFileResolver, site string, urlPath string, routePath string, routeStaticRoot string, routeStaticFile string, defaultSite string, usingDefault bool) (ServeSiteFileDecision, error) {
	current, hasCurrent, decision, err := currentSiteManifestAndServingStatus(ctx, hot, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if decision.Status == domain.SiteServingSuspendedByPolicy {
		return ServeSiteFileDecision{Status: ServeSiteFileSuspended, Site: site, Serving: decision}, nil
	}

	files, siteExists, err := hot.ListCurrentSiteFiles(ctx, site)
	if err != nil {
		return ServeSiteFileDecision{}, err
	}
	if !siteExists && !usingDefault && defaultSite != "" && defaultSite != site {
		return ResolveSiteFile(ctx, hot, defaultSite, urlPath, "", "", "", defaultSite, true)
	}

	fileByPath := make(map[string]domain.UploadFileRecord, len(files))
	for _, file := range files {
		fileByPath[file.RelativePath] = file
	}

	resolvedURLPath := staticURLPathForRoute(urlPath, routePath)
	relativePath, wantsIndex := RequestedRelativePath(resolvedURLPath)
	staticRoot := strings.TrimSpace(routeStaticRoot)
	staticFile := strings.TrimSpace(routeStaticFile)
	if hasCurrent {
		if staticFile != "" {
			staticFile, err = manifest.SanitizeStaticFile(staticFile)
			if err != nil {
				return ServeSiteFileDecision{}, err
			}
		} else if staticRoot != "" {
			staticRoot, err = manifest.SanitizeStaticRoot(staticRoot)
			if err != nil {
				return ServeSiteFileDecision{}, err
			}
		} else if routeTarget, ok, err := staticRouteTargetFromSettings(current.Settings, urlPath); err != nil {
			return ServeSiteFileDecision{}, err
		} else if ok {
			staticRoot = routeTarget.root
			staticFile = routeTarget.file
			resolvedURLPath = staticURLPathForRoute(urlPath, routeTarget.path)
			relativePath, wantsIndex = RequestedRelativePath(resolvedURLPath)
		} else {
			// Deprecated: legacy upload setting fallback for releases uploaded
			// before static route roots existed. Remove with SettingStaticRoot.
			staticRoot, err = manifest.SanitizeStaticRoot(current.Settings[appsettings.SettingStaticRoot])
			if err != nil {
				return ServeSiteFileDecision{}, err
			}
		}
	}
	if staticFile != "" {
		relativePath, _ = RequestedRelativePath(urlPath)
		if file, ok := fileByPath[staticFile]; ok {
			return ServeSiteFileDecision{
				Status:       ServeSiteFileFound,
				Site:         site,
				RelativePath: relativePath,
				File:         file,
				Serving:      decision,
			}, nil
		}
		return ServeSiteFileDecision{Status: ServeSiteFileNotFound, Site: site, RelativePath: relativePath, Serving: decision}, nil
	}
	staticPath := path.Join(staticRoot, relativePath)
	if file, ok := fileByPath[staticPath]; ok {
		return ServeSiteFileDecision{
			Status:       ServeSiteFileFound,
			Site:         site,
			RelativePath: relativePath,
			File:         file,
			Serving:      decision,
		}, nil
	}

	if ShouldTryDirectoryIndex(resolvedURLPath, relativePath, wantsIndex) {
		indexPath := path.Join(staticRoot, relativePath, "index.html")
		if _, ok := fileByPath[indexPath]; ok {
			return ServeSiteFileDecision{
				Status:       ServeSiteFileDirectoryRedirect,
				Site:         site,
				RelativePath: path.Join(relativePath, "index.html"),
				Serving:      decision,
			}, nil
		}
	}

	if wantsIndex {
		return ServeSiteFileDecision{Status: ServeSiteFileEmptyIndex, Site: site, RelativePath: relativePath, Serving: decision}, nil
	}
	return ServeSiteFileDecision{Status: ServeSiteFileNotFound, Site: site, RelativePath: relativePath, Serving: decision}, nil
}

type staticRouteTarget struct {
	path string
	root string
	file string
}

func staticRouteTargetFromSettings(settings map[string]string, urlPath string) (staticRouteTarget, bool, error) {
	var declared []manifest.Route
	if raw := strings.TrimSpace(settings[appsettings.SettingRoutes]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &declared); err != nil {
			return staticRouteTarget{}, false, nil
		}
	}
	cleanURLPath := cleanRoutePath(urlPath)
	var best manifest.Route
	for _, route := range declared {
		kind := route.Kind
		if kind == "" {
			kind = manifest.RouteStatic
		}
		if kind != manifest.RouteStatic {
			continue
		}
		routePath := cleanRoutePath(route.Path)
		if !staticManifestRouteMatches(cleanURLPath, routePath, route.File) {
			continue
		}
		if best.Path == "" || len(routePath) > len(cleanRoutePath(best.Path)) {
			best = route
		}
	}
	if best.Path == "" || (strings.TrimSpace(best.Root) == "" && strings.TrimSpace(best.File) == "") {
		return staticRouteTarget{}, false, nil
	}
	target := staticRouteTarget{path: cleanRoutePath(best.Path)}
	if strings.TrimSpace(best.Root) != "" {
		root, err := manifest.SanitizeStaticRoot(best.Root)
		if err != nil {
			return staticRouteTarget{}, false, err
		}
		target.root = root
	}
	if strings.TrimSpace(best.File) != "" {
		file, err := manifest.SanitizeStaticFile(best.File)
		if err != nil {
			return staticRouteTarget{}, false, err
		}
		target.file = file
	}
	return target, true, nil
}

func staticManifestRouteMatches(urlPath string, routePath string, staticFile string) bool {
	if strings.TrimSpace(staticFile) != "" {
		return urlPath == routePath
	}
	return routeMatches(urlPath, routePath)
}

func staticURLPathForRoute(urlPath string, routePath string) string {
	routePath = cleanRoutePath(routePath)
	if routePath == "/" {
		return urlPath
	}
	cleanURLPath := cleanRoutePath(urlPath)
	if cleanURLPath == routePath {
		return "/"
	}
	prefix := strings.TrimRight(routePath, "/") + "/"
	if strings.HasPrefix(cleanURLPath, prefix) {
		return "/" + strings.TrimPrefix(cleanURLPath, prefix)
	}
	return urlPath
}

func routeMatches(urlPath string, routePath string) bool {
	if routePath == "/" {
		return true
	}
	return urlPath == routePath || strings.HasPrefix(urlPath, strings.TrimRight(routePath, "/")+"/")
}

func cleanRoutePath(value string) string {
	clean := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if clean == "." {
		return "/"
	}
	return clean
}

func currentSiteServingStatus(ctx context.Context, hot SiteFileResolver, site string) (domain.SiteServingDecision, error) {
	_, _, decision, err := currentSiteManifestAndServingStatus(ctx, hot, site)
	return decision, err
}

func currentSiteManifestAndServingStatus(ctx context.Context, hot SiteFileResolver, site string) (domain.CurrentSiteManifest, bool, domain.SiteServingDecision, error) {
	manifests, err := hot.ListCurrentSiteManifests(ctx)
	if err != nil {
		return domain.CurrentSiteManifest{}, false, domain.SiteServingDecision{}, err
	}
	for _, current := range manifests {
		if current.Site != site {
			continue
		}
		violations, err := hot.ListPolicyViolations(ctx, current.SiteSHA, current.Version)
		if err != nil {
			return domain.CurrentSiteManifest{}, false, domain.SiteServingDecision{}, err
		}
		return current, true, servingDecisionFromViolations(violations), nil
	}
	return domain.CurrentSiteManifest{}, false, domain.SiteServingDecision{Status: domain.SiteServingActive}, nil
}

func (s siteReadService) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return s.systemPolicy(ctx, appsettings.SettingDatabaseFeature)
}

func (s siteReadService) SystemRuntimeHTTPPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return s.systemPolicy(ctx, appsettings.SettingRuntimeHTTPFeature)
}

func (s siteReadService) SystemRuntimeHTTPClientPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return s.systemPolicy(ctx, appsettings.SettingRuntimeHTTPClientFeature)
}

func (s siteReadService) SystemRuntimeWebSocketPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return s.systemPolicy(ctx, appsettings.SettingRuntimeWebSocketFeature)
}

func (s siteReadService) systemPolicy(ctx context.Context, key string) (domain.PolicyRecord, error) {
	policies, err := s.hot.LoadPolicies(ctx, []domain.PolicyScope{{Type: domain.ScopeSystem, ID: ""}})
	if err != nil {
		return domain.PolicyRecord{}, err
	}
	policy := domain.PolicyRecord{ScopeType: domain.ScopeSystem, Key: key, Mode: defaultPolicyMode(key)}
	for _, p := range policies {
		if p.Key == key {
			policy = p
			break
		}
	}
	return policy, nil
}

func defaultPolicyMode(key string) string {
	if appsettings.ParseBool(appsettings.Default(key)) {
		return "allow"
	}
	return "deny"
}

func servingDecisionFromViolations(violations []domain.PolicyViolation) domain.SiteServingDecision {
	decision := domain.SiteServingDecision{Status: domain.SiteServingActive}
	for _, violation := range violations {
		if violation.Key != appsettings.SettingDatabaseFeature {
			continue
		}
		if violation.Severity == "suspended" {
			return domain.SiteServingDecision{Status: domain.SiteServingSuspendedByPolicy, Reason: violation.Reason}
		}
		if decision.Status == domain.SiteServingActive {
			decision = domain.SiteServingDecision{Status: domain.SiteServingDegraded, Reason: violation.Reason}
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
