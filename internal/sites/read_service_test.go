package sites

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/policy"
	appsettings "quack/internal/settings"
)

func TestSiteReadServiceUploadPolicyUsesServerSettings(t *testing.T) {
	db := &siteReadServiceDatabase{
		settings: domain.ServerSettings{
			MaxUploadBytes:      2048,
			MaxUploadFiles:      9,
			MaxRetainedVersions: 4,
			LogLevel:            "warn",
		},
	}
	read := NewSiteReadService(db)

	policy, err := read.UploadPolicy(context.Background(), domain.AdminUser{ID: 1, AdminPriv: "admin:*"}, "example.com")
	if err != nil {
		t.Fatalf("UploadPolicy error = %v", err)
	}
	if policy.MaxUploadBytes.Value != 2048 || policy.MaxUploadFiles.Value != 9 || policy.MaxRetainedVersions.Value != 4 {
		t.Fatalf("UploadPolicy = %+v, want server settings values", policy)
	}
	if !policy.MaxUploadBytes.Editable || !policy.MaxUploadFiles.Editable || !policy.MaxRetainedVersions.Editable {
		t.Fatalf("UploadPolicy editable flags = %+v, want admin editable", policy)
	}
}

func TestSiteReadServiceCurrentSiteServingStatus(t *testing.T) {
	tests := []struct {
		name       string
		site       string
		violations []domain.PolicyViolation
		want       domain.SiteServingDecision
	}{
		{
			name: "active without database violations",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      "unrelated",
				Severity: "suspended",
				Reason:   "ignored",
			}},
			want: domain.SiteServingDecision{Status: domain.SiteServingActive},
		},
		{
			name: "degraded database violation",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      appsettings.SettingDatabaseFeature,
				Severity: "degraded",
				Reason:   "database denied",
			}},
			want: domain.SiteServingDecision{Status: domain.SiteServingDegraded, Reason: "database denied"},
		},
		{
			name: "suspended database violation",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      appsettings.SettingDatabaseFeature,
				Severity: "suspended",
				Reason:   "database required",
			}},
			want: domain.SiteServingDecision{Status: domain.SiteServingSuspendedByPolicy, Reason: "database required"},
		},
		{
			name: "unknown site defaults active",
			site: "missing.example",
			want: domain.SiteServingDecision{Status: domain.SiteServingActive},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &siteReadServiceDatabase{
				manifests: []domain.CurrentSiteManifest{{
					Site:    "example.com",
					SiteSHA: "site-sha",
					Version: 1,
				}},
				violations: map[string][]domain.PolicyViolation{"site-sha:1": tt.violations},
			}
			read := NewSiteReadService(db)

			got, err := read.CurrentSiteServingStatus(context.Background(), tt.site)
			if err != nil {
				t.Fatalf("CurrentSiteServingStatus error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CurrentSiteServingStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSiteReadServiceValidateUploadManifestRejectsDeniedDatabaseFeature(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingDatabaseFeature,
			Mode:      "deny",
			Reason:    "database disabled",
		}},
	}
	read := NewSiteReadService(db)

	err := read.ValidateUploadManifest(context.Background(), domain.AdminUser{}, "example.com", manifest.Manifest{
		Features: manifest.Features{Database: manifest.FeatureFlag{Enabled: true}},
	})
	var forbidden policy.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ValidateUploadManifest error = %v, want policy.ForbiddenError", err)
	}
	if err.Error() != "database disabled" {
		t.Fatalf("ValidateUploadManifest error = %q, want policy reason", err.Error())
	}
}

func TestSiteReadServiceValidateUploadManifestRejectsHTTPRouteByDefault(t *testing.T) {
	read := NewSiteReadService(&siteReadServiceDatabase{})

	err := read.ValidateUploadManifest(context.Background(), domain.AdminUser{}, "example.com", manifest.Manifest{
		Routes: []manifest.Route{{Path: "/api", Kind: manifest.RouteHTTP}},
	})
	var forbidden policy.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ValidateUploadManifest error = %v, want policy.ForbiddenError", err)
	}
	if err.Error() != "dynamic HTTP routes are disabled by administrator policy" {
		t.Fatalf("ValidateUploadManifest error = %q, want runtime HTTP policy reason", err.Error())
	}
}

func TestSiteReadServiceValidateUploadManifestAllowsHTTPRouteWithPolicy(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeHTTPFeature,
			Mode:      "allow",
		}},
	}
	read := NewSiteReadService(db)

	err := read.ValidateUploadManifest(context.Background(), domain.AdminUser{}, "example.com", manifest.Manifest{
		Routes: []manifest.Route{{Path: "/api", Kind: manifest.RouteHTTP}},
	})
	if err != nil {
		t.Fatalf("ValidateUploadManifest error = %v, want allowed HTTP route", err)
	}
}

func TestSiteReadServiceCurrentSiteFileUsesHotReader(t *testing.T) {
	db := &siteReadServiceDatabase{
		file: domain.UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", FileSHA: "sha", Bytes: 42},
	}
	read := NewSiteReadService(db)

	file, fileOK, siteOK, err := read.CurrentSiteFile(context.Background(), "example.com", "index.html")
	if err != nil {
		t.Fatalf("CurrentSiteFile error = %v", err)
	}
	if !fileOK || !siteOK || file.Bytes != 42 {
		t.Fatalf("CurrentSiteFile = (%+v, %v, %v), want delegated file", file, fileOK, siteOK)
	}
}

func TestSiteReadServiceServeSiteFileResolvesFromReadModels(t *testing.T) {
	db := &siteReadServiceDatabase{
		file: domain.UploadFileRecord{RelativePath: "index.html", BlobPath: "blob"},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileFound || decision.File.BlobPath != "blob" {
		t.Fatalf("ServeSiteFile = %+v, want resolved static file", decision)
	}
}

func TestSiteReadServiceServeSiteFileUsesConfiguredStaticRoot(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "public"},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "index.html", BlobPath: "private-index"},
			{RelativePath: "public/index.html", BlobPath: "public-index"},
			{RelativePath: "public/app.js", BlobPath: "public-app"},
		},
	}
	read := NewSiteReadService(db)

	rootDecision, err := read.ServeSiteFile(context.Background(), "example.com", "/", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile root error = %v", err)
	}
	if rootDecision.Status != ServeSiteFileFound || rootDecision.RelativePath != "index.html" || rootDecision.File.BlobPath != "public-index" {
		t.Fatalf("root decision = %+v, want public index served as URL root", rootDecision)
	}

	appDecision, err := read.ServeSiteFile(context.Background(), "example.com", "/app.js", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile app error = %v", err)
	}
	if appDecision.Status != ServeSiteFileFound || appDecision.RelativePath != "app.js" || appDecision.File.BlobPath != "public-app" {
		t.Fatalf("app decision = %+v, want public app served as URL app.js", appDecision)
	}
}

func TestSiteReadServiceServeSiteFileDoesNotServeFilesAboveStaticRoot(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "public"},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "private.html", BlobPath: "private"},
			{RelativePath: "scripts/build.sh", BlobPath: "script"},
			{RelativePath: "public/index.html", BlobPath: "public-index"},
		},
	}
	read := NewSiteReadService(db)

	for _, urlPath := range []string{"/private.html", "/scripts/build.sh"} {
		decision, err := read.ServeSiteFile(context.Background(), "example.com", urlPath, "", "", "")
		if err != nil {
			t.Fatalf("ServeSiteFile %s error = %v", urlPath, err)
		}
		if decision.Status != ServeSiteFileNotFound {
			t.Fatalf("ServeSiteFile %s = %+v, want not found above static root", urlPath, decision)
		}
	}
}

func TestSiteReadServiceServeSiteFileUsesRouteStaticRoot(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "legacy"},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "legacy/index.html", BlobPath: "legacy-index"},
			{RelativePath: "public/index.html", BlobPath: "public-index"},
			{RelativePath: "public/app.js", BlobPath: "public-app"},
		},
	}
	read := NewSiteReadService(db)

	rootDecision, err := read.ServeSiteFile(context.Background(), "example.com", "/", "/", "public", "")
	if err != nil {
		t.Fatalf("ServeSiteFile root error = %v", err)
	}
	if rootDecision.Status != ServeSiteFileFound || rootDecision.File.BlobPath != "public-index" {
		t.Fatalf("root decision = %+v, want route static root over legacy static.root", rootDecision)
	}

	appDecision, err := read.ServeSiteFile(context.Background(), "example.com", "/app.js", "/", "public", "")
	if err != nil {
		t.Fatalf("ServeSiteFile app error = %v", err)
	}
	if appDecision.Status != ServeSiteFileFound || appDecision.RelativePath != "app.js" || appDecision.File.BlobPath != "public-app" {
		t.Fatalf("app decision = %+v, want public app served from route root", appDecision)
	}
}

func TestSiteReadServiceServeSiteFileStripsStaticRoutePathBeforeRootLookup(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "public/assets/app.js", BlobPath: "asset-app"},
			{RelativePath: "public/assets/assets/app.js", BlobPath: "double-prefixed-app"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/assets/app.js", "/assets", "public/assets", "")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileFound || decision.RelativePath != "app.js" || decision.File.BlobPath != "asset-app" {
		t.Fatalf("decision = %+v, want route path stripped before static root lookup", decision)
	}
}

func TestSiteReadServiceServeSiteFileUsesRouteStaticFile(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "public/favicon.ico", BlobPath: "root-favicon"},
			{RelativePath: "media/favicon.ico", BlobPath: "media-favicon"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/favicon.ico", "/favicon.ico", "", "media/favicon.ico")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileFound || decision.RelativePath != "favicon.ico" || decision.File.BlobPath != "media-favicon" {
		t.Fatalf("decision = %+v, want exact static file target", decision)
	}
}

func TestSiteReadServiceServeSiteFileMissingRouteStaticFileIsNotFound(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "public/favicon.ico", BlobPath: "root-favicon"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/favicon.ico", "/favicon.ico", "", "media/favicon.ico")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileNotFound {
		t.Fatalf("decision = %+v, want missing static file target to be not found", decision)
	}
}

func TestSiteReadServiceServeSiteFileUsesStoredStaticFileRoute(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{
				appsettings.SettingRoutes: `[{"path":"/","kind":"static","root":"public"},{"path":"/favicon.ico","kind":"static","file":"media/favicon.ico"}]`,
			},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "public/favicon.ico", BlobPath: "root-favicon"},
			{RelativePath: "media/favicon.ico", BlobPath: "media-favicon"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/favicon.ico", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileFound || decision.File.BlobPath != "media-favicon" {
		t.Fatalf("decision = %+v, want stored static file route target", decision)
	}
}

func TestSiteReadServiceServeSiteFileDirectoryRedirectUsesStaticRoot(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "public"},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "docs/index.html", BlobPath: "private-docs"},
			{RelativePath: "public/docs/index.html", BlobPath: "public-docs"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/docs", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileDirectoryRedirect || decision.RelativePath != "docs/index.html" {
		t.Fatalf("decision = %+v, want URL-facing docs directory redirect", decision)
	}
}

func TestSiteReadServiceServeSiteFileStaticRootWithDefaultSiteFallback(t *testing.T) {
	db := &siteReadServiceDatabase{
		settings: domain.ServerSettings{DefaultSite: "home"},
		manifests: []domain.CurrentSiteManifest{{
			Site: "home", SiteSHA: "home-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "public"},
		}},
		filesBySite: map[string][]domain.UploadFileRecord{
			"home": {
				{RelativePath: "index.html", BlobPath: "private-home"},
				{RelativePath: "public/index.html", BlobPath: "public-home"},
			},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "missing", "/", "", "", "")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Site != "home" || decision.Status != ServeSiteFileFound || decision.File.BlobPath != "public-home" {
		t.Fatalf("decision = %+v, want default site public root", decision)
	}
}

func TestSiteReadServiceServeSiteFileRejectsMalformedStoredStaticRoot(t *testing.T) {
	db := &siteReadServiceDatabase{
		manifests: []domain.CurrentSiteManifest{{
			Site: "example.com", SiteSHA: "site-sha", Version: 1,
			Settings: map[string]string{appsettings.SettingStaticRoot: "public/../private"},
		}},
		files: []domain.UploadFileRecord{
			{RelativePath: "private/index.html", BlobPath: "private-index"},
		},
	}
	read := NewSiteReadService(db)

	_, err := read.ServeSiteFile(context.Background(), "example.com", "/", "", "", "")
	if err == nil {
		t.Fatal("ServeSiteFile error = nil, want malformed static root error")
	}
	if !strings.Contains(err.Error(), "static.root cannot contain ..") {
		t.Fatalf("ServeSiteFile error = %v, want static root validation error", err)
	}
}

func TestSiteReadServiceSystemDatabasePolicy(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingDatabaseFeature,
			Mode:      "deny",
			Reason:    "disabled",
		}},
	}
	read := NewSiteReadService(db)

	policy, err := read.SystemDatabasePolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemDatabasePolicy error = %v", err)
	}
	if policy.Mode != "deny" || policy.Reason != "disabled" {
		t.Fatalf("SystemDatabasePolicy = %+v, want database policy", policy)
	}
}

func TestSiteReadServiceSystemDatabasePolicyDefaultsToDeny(t *testing.T) {
	read := NewSiteReadService(&siteReadServiceDatabase{})

	policy, err := read.SystemDatabasePolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemDatabasePolicy error = %v", err)
	}
	if policy.ScopeType != domain.ScopeSystem || policy.Key != appsettings.SettingDatabaseFeature || policy.Mode != "deny" {
		t.Fatalf("SystemDatabasePolicy = %+v, want deny default", policy)
	}
}

func TestSiteReadServiceSystemRuntimeHTTPPolicy(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeHTTPFeature,
			Mode:      "allow",
			Reason:    "enabled for starlark",
		}},
	}
	read := NewSiteReadService(db)

	policy, err := read.SystemRuntimeHTTPPolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemRuntimeHTTPPolicy error = %v", err)
	}
	if policy.Mode != "allow" || policy.Reason != "enabled for starlark" {
		t.Fatalf("SystemRuntimeHTTPPolicy = %+v, want runtime HTTP policy", policy)
	}
}

func TestSiteReadServiceSystemRuntimeHTTPPolicyDefaultsToDeny(t *testing.T) {
	read := NewSiteReadService(&siteReadServiceDatabase{})

	policy, err := read.SystemRuntimeHTTPPolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemRuntimeHTTPPolicy error = %v", err)
	}
	if policy.ScopeType != domain.ScopeSystem || policy.Key != appsettings.SettingRuntimeHTTPFeature || policy.Mode != "deny" {
		t.Fatalf("SystemRuntimeHTTPPolicy = %+v, want deny default", policy)
	}
}

func TestSiteReadServiceSystemHardwareCameraPolicyDefaultsToAllow(t *testing.T) {
	policy := domain.PolicyRecord{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingHardwareCameraFeature,
		Mode:      defaultPolicyMode(appsettings.SettingHardwareCameraFeature),
	}
	if policy.Mode != "allow" {
		t.Fatalf("hardware camera policy = %+v, want allow default", policy)
	}
}

func TestSiteReadServiceSystemRuntimeWebSocketPolicy(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeWebSocketFeature,
			Mode:      "allow",
			Reason:    "enabled for sockets",
		}},
	}
	read := NewSiteReadService(db)

	policy, err := read.SystemRuntimeWebSocketPolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemRuntimeWebSocketPolicy error = %v", err)
	}
	if policy.Mode != "allow" || policy.Reason != "enabled for sockets" {
		t.Fatalf("SystemRuntimeWebSocketPolicy = %+v, want runtime websocket policy", policy)
	}
}

type siteReadServiceDatabase struct {
	settings    domain.ServerSettings
	policies    []domain.PolicyRecord
	manifests   []domain.CurrentSiteManifest
	violations  map[string][]domain.PolicyViolation
	file        domain.UploadFileRecord
	files       []domain.UploadFileRecord
	filesBySite map[string][]domain.UploadFileRecord
}

func (db *siteReadServiceDatabase) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return db.settings, nil
}

func (db *siteReadServiceDatabase) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, policy := range db.policies {
		for _, scope := range scopes {
			if policy.ScopeType == scope.Type && policy.ScopeID == scope.ID {
				out = append(out, policy)
			}
		}
	}
	return out, nil
}

func (db *siteReadServiceDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, nil
}

func (db *siteReadServiceDatabase) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return db.manifests, nil
}

func (db *siteReadServiceDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	if db.violations == nil {
		return nil, nil
	}
	return db.violations[siteSHA+":"+strconv.FormatInt(version, 10)], nil
}

func (db *siteReadServiceDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return db.file, db.file.RelativePath != "", true, nil
}

func (db *siteReadServiceDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	if db.filesBySite != nil {
		files, ok := db.filesBySite[site]
		return files, ok, nil
	}
	if db.files != nil {
		return db.files, true, nil
	}
	if db.file.RelativePath == "" {
		return nil, true, nil
	}
	return []domain.UploadFileRecord{db.file}, true, nil
}
