package sites

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"quack/internal/domain"
	"quack/internal/protocol"
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

func TestSiteReadServiceCurrentSiteRuntime(t *testing.T) {
	tests := []struct {
		name       string
		site       string
		violations []domain.PolicyViolation
		want       domain.SiteRuntimeDecision
	}{
		{
			name: "active without database violations",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      "unrelated",
				Severity: "suspended",
				Reason:   "ignored",
			}},
			want: domain.SiteRuntimeDecision{Status: domain.SiteRuntimeActive},
		},
		{
			name: "degraded database violation",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      appsettings.SettingDatabaseFeature,
				Severity: "degraded",
				Reason:   "database denied",
			}},
			want: domain.SiteRuntimeDecision{Status: domain.SiteRuntimeDegraded, Reason: "database denied"},
		},
		{
			name: "suspended database violation",
			site: "example.com",
			violations: []domain.PolicyViolation{{
				Key:      appsettings.SettingDatabaseFeature,
				Severity: "suspended",
				Reason:   "database required",
			}},
			want: domain.SiteRuntimeDecision{Status: domain.SiteRuntimeSuspendedByPolicy, Reason: "database required"},
		},
		{
			name: "unknown site defaults active",
			site: "missing.example",
			want: domain.SiteRuntimeDecision{Status: domain.SiteRuntimeActive},
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

			got, err := read.CurrentSiteRuntime(context.Background(), tt.site)
			if err != nil {
				t.Fatalf("CurrentSiteRuntime error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CurrentSiteRuntime = %+v, want %+v", got, tt.want)
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

	err := read.ValidateUploadManifest(context.Background(), domain.AdminUser{}, "example.com", protocol.SiteManifest{
		Features: protocol.SiteManifestFeatures{Database: protocol.SiteManifestDatabase{Enabled: true}},
	})
	var forbidden ForbiddenPolicyError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ValidateUploadManifest error = %v, want ForbiddenPolicyError", err)
	}
	if err.Error() != "database disabled" {
		t.Fatalf("ValidateUploadManifest error = %q, want policy reason", err.Error())
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

func TestSiteReadServiceServeSiteFileUsesHotReader(t *testing.T) {
	db := &siteReadServiceDatabase{
		serveDecision: ServeSiteFileDecision{
			Status:       ServeSiteFileFound,
			Site:         "example.com",
			RelativePath: "index.html",
			File:         domain.UploadFileRecord{RelativePath: "index.html", BlobPath: "blob"},
		},
	}
	read := NewSiteReadService(db)

	decision, err := read.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if decision.Status != ServeSiteFileFound || decision.File.BlobPath != "blob" {
		t.Fatalf("ServeSiteFile = %+v, want delegated decision", decision)
	}
	if db.serveCalls != 1 {
		t.Fatalf("ServeSiteFile calls = %d, want 1", db.serveCalls)
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

func TestSiteReadServiceSystemDatabasePolicyDefaultsToInherit(t *testing.T) {
	read := NewSiteReadService(&siteReadServiceDatabase{})

	policy, err := read.SystemDatabasePolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemDatabasePolicy error = %v", err)
	}
	if policy.ScopeType != domain.ScopeSystem || policy.Key != appsettings.SettingDatabaseFeature || policy.Mode != "inherit" {
		t.Fatalf("SystemDatabasePolicy = %+v, want inherit default", policy)
	}
}

type siteReadServiceDatabase struct {
	settings      domain.ServerSettings
	policies      []domain.PolicyRecord
	manifests     []domain.CurrentSiteManifest
	violations    map[string][]domain.PolicyViolation
	file          domain.UploadFileRecord
	files         []domain.UploadFileRecord
	serveDecision ServeSiteFileDecision
	serveCalls    int
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
	if db.files != nil {
		return db.files, true, nil
	}
	if db.file.RelativePath == "" {
		return nil, true, nil
	}
	return []domain.UploadFileRecord{db.file}, true, nil
}

func (db *siteReadServiceDatabase) ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error) {
	db.serveCalls++
	return db.serveDecision, nil
}
