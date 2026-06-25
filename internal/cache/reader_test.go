package cache

import (
	"context"
	"testing"

	"quack/internal/domain"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
)

func TestPassthroughHotDataReaderCopiesMutableResults(t *testing.T) {
	db := &readerBackingDatabase{
		settings: domain.ServerSettings{
			MaxUploadBytes: appsettings.DefaultMaxUploadBytes,
			MaxUploadFiles: appsettings.DefaultMaxUploadFiles,
			LogLevel:       "warn",
			Locked:         map[string]bool{"default_site": true},
		},
		policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow"}},
		uploadSettings: map[string]string{
			appsettings.SettingDatabaseFeature: "true",
		},
		manifests: []domain.CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  3,
			Settings: map[string]string{appsettings.SettingDatabaseFeatureRequired: "true"},
		}},
		runtimeRoutes: []appruntime.RouteMetadata{{
			Site: "example.com", SiteSHA: "site-sha", Version: 3, RoutePath: "/api", RouteKind: appruntime.RouteHTTP, Methods: []string{"GET"},
		}},
		violations: []domain.PolicyViolation{{SiteSHA: "site-sha", UploadVersion: 3, Key: appsettings.SettingDatabaseFeature}},
	}
	reader := NewPassthroughHotDataReader(db)
	ctx := context.Background()

	settings, err := reader.GetServerSettings(ctx)
	if err != nil {
		t.Fatalf("GetServerSettings error = %v", err)
	}
	settings.Locked["default_site"] = false
	if db.settings.Locked["default_site"] != true {
		t.Fatalf("GetServerSettings leaked Locked map mutation")
	}

	policies, err := reader.LoadPolicies(ctx, []domain.PolicyScope{{Type: domain.ScopeSystem, ID: ""}})
	if err != nil {
		t.Fatalf("LoadPolicies error = %v", err)
	}
	policies[0].Mode = "deny"
	if db.policies[0].Mode != "allow" {
		t.Fatalf("LoadPolicies leaked slice mutation")
	}

	uploadSettings, err := reader.LoadUploadSettings(ctx, "site-sha", 3)
	if err != nil {
		t.Fatalf("LoadUploadSettings error = %v", err)
	}
	uploadSettings[appsettings.SettingDatabaseFeature] = "false"
	if db.uploadSettings[appsettings.SettingDatabaseFeature] != "true" {
		t.Fatalf("LoadUploadSettings leaked map mutation")
	}

	manifests, err := reader.ListCurrentSiteManifests(ctx)
	if err != nil {
		t.Fatalf("ListCurrentSiteManifests error = %v", err)
	}
	manifests[0].Site = "mutated.example"
	manifests[0].Settings[appsettings.SettingDatabaseFeatureRequired] = "false"
	if db.manifests[0].Site != "example.com" || db.manifests[0].Settings[appsettings.SettingDatabaseFeatureRequired] != "true" {
		t.Fatalf("ListCurrentSiteManifests leaked mutable result")
	}

	runtimeRoutes, err := reader.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		t.Fatalf("ListCurrentRuntimeRoutes error = %v", err)
	}
	runtimeRoutes[0].Methods[0] = "POST"
	if db.runtimeRoutes[0].Methods[0] != "GET" {
		t.Fatalf("ListCurrentRuntimeRoutes leaked mutable result")
	}

	db.file = domain.UploadFileRecord{RelativePath: "data.txt", BlobPath: "data-blob"}
	bundleFiles, _, err := reader.ListRuntimeBundleFiles(ctx, "site-sha", 3)
	if err != nil {
		t.Fatalf("ListRuntimeBundleFiles error = %v", err)
	}
	bundleFiles[0].BlobPath = "mutated"
	if db.file.BlobPath != "data-blob" {
		t.Fatalf("ListRuntimeBundleFiles leaked mutable result")
	}

	violations, err := reader.ListPolicyViolations(ctx, "site-sha", 3)
	if err != nil {
		t.Fatalf("ListPolicyViolations error = %v", err)
	}
	violations[0].Key = "mutated"
	if db.violations[0].Key != appsettings.SettingDatabaseFeature {
		t.Fatalf("ListPolicyViolations leaked slice mutation")
	}
}

func TestPassthroughHotDataReaderFindCurrentSiteFileDelegates(t *testing.T) {
	db := &readerBackingDatabase{
		file: domain.UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", FileSHA: "sha", Bytes: 12},
	}
	reader := NewPassthroughHotDataReader(db)

	file, fileOK, siteOK, err := reader.FindCurrentSiteFile(context.Background(), "example.com", "index.html")
	if err != nil {
		t.Fatalf("FindCurrentSiteFile error = %v", err)
	}
	if !fileOK || !siteOK || file.BlobPath != "blob" {
		t.Fatalf("FindCurrentSiteFile = (%+v, %v, %v), want delegated file", file, fileOK, siteOK)
	}
}

type readerBackingDatabase struct {
	settings       domain.ServerSettings
	policies       []domain.PolicyRecord
	uploadSettings map[string]string
	manifests      []domain.CurrentSiteManifest
	runtimeRoutes  []appruntime.RouteMetadata
	violations     []domain.PolicyViolation
	file           domain.UploadFileRecord
}

func (db *readerBackingDatabase) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return db.settings, nil
}

func (db *readerBackingDatabase) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	return db.policies, nil
}

func (db *readerBackingDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return db.uploadSettings, nil
}

func (db *readerBackingDatabase) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return db.manifests, nil
}

func (db *readerBackingDatabase) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	return db.runtimeRoutes, nil
}

func (db *readerBackingDatabase) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	return db.runtimeRoutes, nil
}

func (db *readerBackingDatabase) ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error) {
	if db.file.RelativePath == "" {
		return nil, true, nil
	}
	return []domain.UploadFileRecord{db.file}, true, nil
}

func (db *readerBackingDatabase) ListRuntimeAPIProxies(ctx context.Context, siteSHA string, version int64) ([]manifest.APIProxy, error) {
	return nil, nil
}

func (db *readerBackingDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	return db.violations, nil
}

func (db *readerBackingDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return db.file, true, true, nil
}

func (db *readerBackingDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	return []domain.UploadFileRecord{db.file}, true, nil
}
