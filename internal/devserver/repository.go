package devserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"quack/internal/domain"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
)

type Repository struct {
	current  atomic.Pointer[DevSiteSource]
	settings domain.ServerSettings
	policies []domain.PolicyRecord
}

func NewRepository(settings domain.ServerSettings) *Repository {
	settings = withDevSettingDefaults(settings)
	repo := &Repository{
		settings: settings,
		policies: []domain.PolicyRecord{
			{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPFeature, Mode: "allow", Value: "true", Reason: "allowed by dev-server"},
			{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPClientFeature, Mode: "allow", Value: "true", Reason: "allowed by dev-server"},
			{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeWebSocketFeature, Mode: "allow", Value: "true", Reason: "allowed by dev-server"},
			{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow", Value: "true", Reason: "allowed by dev-server"},
		},
	}
	return repo
}

func (r *Repository) Current() (*DevSiteSource, bool) {
	current := r.current.Load()
	return current, current != nil
}

func (r *Repository) Refresh(ctx context.Context, rootDir string, site string) (*DevSiteSource, error) {
	generation := int64(1)
	if current, ok := r.Current(); ok {
		generation = current.Generation + 1
	}
	source, err := LoadSiteSource(ctx, rootDir, site, generation)
	if err != nil {
		return nil, err
	}
	r.current.Store(source)
	return source, nil
}

func (r *Repository) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	if err := ctx.Err(); err != nil {
		return domain.ServerSettings{}, err
	}
	settings := r.settings
	if current, ok := r.Current(); ok && settings.DefaultSite == "" {
		settings.DefaultSite = current.Site
	}
	return settings, nil
}

func (r *Repository) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]domain.PolicyRecord(nil), r.policies...), nil
}

func (r *Repository) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.match(siteSHA, version)
	if !ok {
		return nil, nil
	}
	return cloneSettings(current.Settings), nil
}

func (r *Repository) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.Current()
	if !ok {
		return nil, nil
	}
	return []domain.CurrentSiteManifest{{
		Site:     current.Site,
		SiteSHA:  current.SiteSHA,
		Version:  current.Generation,
		Settings: cloneSettings(current.Settings),
	}}, nil
}

func (r *Repository) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *Repository) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.UploadFileRecord{}, false, false, err
	}
	current, ok := r.Current()
	if !ok || current.Site != site {
		return domain.UploadFileRecord{}, false, false, nil
	}
	file, found := current.Files[relativePath]
	return file, found, true, nil
}

func (r *Repository) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	current, ok := r.Current()
	if !ok || current.Site != site {
		return nil, false, nil
	}
	return sortedFiles(current.Files), true, nil
}

func (r *Repository) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.Current()
	if !ok {
		return nil, nil
	}
	return cloneRoutes(current.Routes), nil
}

func (r *Repository) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.match(siteSHA, version)
	if !ok {
		return nil, nil
	}
	return cloneRoutes(current.Routes), nil
}

func (r *Repository) ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	current, ok := r.match(siteSHA, version)
	if !ok {
		return nil, false, nil
	}
	return sortedFiles(current.Files), true, nil
}

func (r *Repository) ListRuntimeAPIProxies(ctx context.Context, siteSHA string, version int64) ([]manifest.APIProxy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.match(siteSHA, version)
	if !ok {
		return nil, nil
	}
	value := current.Settings[appsettings.SettingRuntimeHTTPClientAPIProxies]
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var out []manifest.APIProxy
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.Current()
	if !ok {
		return nil, nil
	}
	var bytes int64
	for _, file := range current.Files {
		bytes += file.Bytes
	}
	return []domain.PublishedSite{{
		Site:           current.Site,
		SiteSHA:        current.SiteSHA,
		CurrentVersion: current.Generation,
		VersionCount:   1,
		FileCount:      int64(len(current.Files)),
		ByteCount:      bytes,
		UpdatedAt:      current.LoadedAt.Format("2006-01-02T15:04:05Z07:00"),
		LiveState:      "published",
		ServingStatus:  domain.SiteServingActive,
	}}, nil
}

func (r *Repository) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	return r.ListPublishedSites(ctx, 0, true)
}

func (r *Repository) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := r.match(siteSHA, 0)
	if !ok || current.Site != site {
		return nil, nil
	}
	var bytes int64
	for _, file := range current.Files {
		bytes += file.Bytes
	}
	return []domain.RevisionRecord{{
		Version:    current.Generation,
		Current:    true,
		Files:      int64(len(current.Files)),
		Bytes:      bytes,
		FinishedAt: current.LoadedAt.Format("2006-01-02T15:04:05Z07:00"),
	}}, nil
}

func (r *Repository) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, fmt.Errorf("rollback is unsupported in dev mode")
}

func (r *Repository) RollbackSiteToVersion(ctx context.Context, user domain.AdminUser, site string, siteSHA string, version int64) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, fmt.Errorf("rollback is unsupported in dev mode")
}

func (r *Repository) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	return domain.UnpublishRecord{}, fmt.Errorf("unpublish is unsupported in dev mode")
}

func (r *Repository) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	return domain.PublishRecord{}, fmt.Errorf("publish is unsupported in dev mode")
}

func (r *Repository) DeleteSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (bool, error) {
	return false, fmt.Errorf("delete site is unsupported in dev mode")
}

func (r *Repository) InvalidateSite(ctx context.Context, site string) error {
	return ctx.Err()
}

func (r *Repository) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	return ctx.Err()
}

func (r *Repository) match(siteSHA string, version int64) (*DevSiteSource, bool) {
	current, ok := r.Current()
	if !ok {
		return nil, false
	}
	if siteSHA != "" && current.SiteSHA != siteSHA {
		return nil, false
	}
	if version > 0 && current.Generation != version {
		return nil, false
	}
	return current, true
}

func withDevSettingDefaults(settings domain.ServerSettings) domain.ServerSettings {
	if settings.MaxUploadBytes <= 0 {
		settings.MaxUploadBytes = 1 << 40
	}
	if settings.MaxUploadFiles <= 0 {
		settings.MaxUploadFiles = 1_000_000
	}
	if settings.MaxRuntimeDurationMillis <= 0 {
		settings.MaxRuntimeDurationMillis = 60_000
	}
	if settings.HTTPClientMaxBytes <= 0 {
		settings.HTTPClientMaxBytes = 16 << 20
	}
	if settings.HTTPClientMaxTimeoutMS <= 0 {
		settings.HTTPClientMaxTimeoutMS = 10_000
	}
	if settings.MaxWebSocketConnections <= 0 {
		settings.MaxWebSocketConnections = 4096
	}
	if settings.MaxWebSocketConnectionsPerSite <= 0 {
		settings.MaxWebSocketConnectionsPerSite = 4096
	}
	if settings.HTTPCacheMode == "" {
		settings.HTTPCacheMode = "anti_cache"
	}
	if settings.MemoryPersistenceMode == "" {
		settings.MemoryPersistenceMode = "off"
	}
	if settings.LogLevel == "" {
		settings.LogLevel = "debug"
	}
	return settings
}

func cloneSettings(settings map[string]string) map[string]string {
	out := make(map[string]string, len(settings))
	for key, value := range settings {
		out[key] = value
	}
	return out
}

func cloneRoutes(routes []appruntime.RouteMetadata) []appruntime.RouteMetadata {
	out := append([]appruntime.RouteMetadata(nil), routes...)
	for i := range out {
		out[i].Methods = append([]string(nil), out[i].Methods...)
		out[i].RequiredCapabilities = append([]string(nil), out[i].RequiredCapabilities...)
	}
	return out
}

func sortedFiles(files map[string]domain.UploadFileRecord) []domain.UploadFileRecord {
	out := make([]domain.UploadFileRecord, 0, len(files))
	for _, file := range files {
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelativePath < out[j].RelativePath
	})
	return out
}
