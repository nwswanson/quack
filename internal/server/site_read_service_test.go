package server

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSiteReadServiceUploadPolicyUsesServerSettings(t *testing.T) {
	db := &siteReadServiceDatabase{
		settings: ServerSettings{
			MaxUploadBytes:      2048,
			MaxUploadFiles:      9,
			MaxRetainedVersions: 4,
			LogLevel:            "warn",
		},
	}
	read := NewSiteReadService(NewPassthroughHotDataReader(db))

	policy, err := read.UploadPolicy(context.Background(), AdminUser{ID: 1, AdminPriv: "admin:*"}, "example.com")
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
		violations []PolicyViolation
		want       SiteRuntimeDecision
	}{
		{
			name: "active without database violations",
			site: "example.com",
			violations: []PolicyViolation{{
				Key:      "unrelated",
				Severity: "suspended",
				Reason:   "ignored",
			}},
			want: SiteRuntimeDecision{Status: SiteRuntimeActive},
		},
		{
			name: "degraded database violation",
			site: "example.com",
			violations: []PolicyViolation{{
				Key:      SettingDatabaseFeature,
				Severity: "degraded",
				Reason:   "database denied",
			}},
			want: SiteRuntimeDecision{Status: SiteRuntimeDegraded, Reason: "database denied"},
		},
		{
			name: "suspended database violation",
			site: "example.com",
			violations: []PolicyViolation{{
				Key:      SettingDatabaseFeature,
				Severity: "suspended",
				Reason:   "database required",
			}},
			want: SiteRuntimeDecision{Status: SiteRuntimeSuspendedByPolicy, Reason: "database required"},
		},
		{
			name: "unknown site defaults active",
			site: "missing.example",
			want: SiteRuntimeDecision{Status: SiteRuntimeActive},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &siteReadServiceDatabase{
				manifests: []CurrentSiteManifest{{
					Site:    "example.com",
					SiteSHA: "site-sha",
					Version: 1,
				}},
				violations: map[string][]PolicyViolation{"site-sha:1": tt.violations},
			}
			read := NewSiteReadService(NewPassthroughHotDataReader(db))

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
		policies: []PolicyRecord{{
			ScopeType: ScopeSystem,
			Key:       SettingDatabaseFeature,
			Mode:      "deny",
			Reason:    "database disabled",
		}},
	}
	read := NewSiteReadService(NewPassthroughHotDataReader(db))

	err := read.ValidateUploadManifest(context.Background(), AdminUser{}, "example.com", SiteManifest{
		Features: SiteManifestFeatures{Database: SiteManifestDatabase{Enabled: true}},
	})
	var forbidden forbiddenPolicyError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ValidateUploadManifest error = %v, want forbiddenPolicyError", err)
	}
	if err.Error() != "database disabled" {
		t.Fatalf("ValidateUploadManifest error = %q, want policy reason", err.Error())
	}
}

func TestSiteReadServiceCurrentSiteFileUsesHotReader(t *testing.T) {
	db := &siteReadServiceDatabase{
		file: UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", FileSHA: "sha", Bytes: 42},
	}
	read := NewSiteReadService(NewPassthroughHotDataReader(db))

	file, fileOK, siteOK, err := read.CurrentSiteFile(context.Background(), "example.com", "index.html")
	if err != nil {
		t.Fatalf("CurrentSiteFile error = %v", err)
	}
	if !fileOK || !siteOK || file.Bytes != 42 {
		t.Fatalf("CurrentSiteFile = (%+v, %v, %v), want delegated file", file, fileOK, siteOK)
	}
}

func TestSiteReadServiceServeSiteFileUsesCachedSiteBundle(t *testing.T) {
	db := &siteReadServiceDatabase{
		settings: ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
		files: []UploadFileRecord{{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:old",
			FileSHA:      "old",
			Bytes:        42,
		}},
	}
	hot := NewMemoryHotDataReader(NewPassthroughHotDataReader(db), MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute})
	read := NewSiteReadService(hot)

	first, err := read.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	if first.Status != ServeSiteFileFound || first.File.BlobPath != "blobs/site:site-sha/1/file:old" {
		t.Fatalf("ServeSiteFile = %+v, want old blob", first)
	}

	db.files[0].BlobPath = "blobs/site:site-sha/1/file:new"
	second, err := read.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("second ServeSiteFile error = %v", err)
	}
	if second.Status != ServeSiteFileFound || second.File.BlobPath != "blobs/site:site-sha/1/file:old" {
		t.Fatalf("second ServeSiteFile = %+v, want cached old blob", second)
	}
	if db.fileListCalls != 1 {
		t.Fatalf("ListCurrentSiteFiles calls = %d, want 1", db.fileListCalls)
	}
}

func TestSiteReadServiceSystemDatabasePolicy(t *testing.T) {
	db := &siteReadServiceDatabase{
		policies: []PolicyRecord{{
			ScopeType: ScopeSystem,
			Key:       SettingDatabaseFeature,
			Mode:      "deny",
			Reason:    "disabled",
		}},
	}
	read := NewSiteReadService(NewPassthroughHotDataReader(db))

	policy, err := read.SystemDatabasePolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemDatabasePolicy error = %v", err)
	}
	if policy.Mode != "deny" || policy.Reason != "disabled" {
		t.Fatalf("SystemDatabasePolicy = %+v, want database policy", policy)
	}
}

func TestSiteReadServiceSystemDatabasePolicyDefaultsToInherit(t *testing.T) {
	read := NewSiteReadService(NewPassthroughHotDataReader(&siteReadServiceDatabase{}))

	policy, err := read.SystemDatabasePolicy(context.Background())
	if err != nil {
		t.Fatalf("SystemDatabasePolicy error = %v", err)
	}
	if policy.ScopeType != ScopeSystem || policy.Key != SettingDatabaseFeature || policy.Mode != "inherit" {
		t.Fatalf("SystemDatabasePolicy = %+v, want inherit default", policy)
	}
}

type siteReadServiceDatabase struct {
	Database
	settings           ServerSettings
	policies           []PolicyRecord
	manifests          []CurrentSiteManifest
	violations         map[string][]PolicyViolation
	file               UploadFileRecord
	files              []UploadFileRecord
	fileListCalls      int
	savedViolations    []PolicyViolation
	resolvedViolations []string
}

func (db *siteReadServiceDatabase) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	return db.settings, nil
}

func (db *siteReadServiceDatabase) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	var out []PolicyRecord
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

func (db *siteReadServiceDatabase) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	return db.manifests, nil
}

func (db *siteReadServiceDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	if db.violations == nil {
		return nil, nil
	}
	return db.violations[siteSHA+":"+strconv.FormatInt(version, 10)], nil
}

func (db *siteReadServiceDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return db.file, db.file.RelativePath != "", true, nil
}

func (db *siteReadServiceDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]UploadFileRecord, bool, error) {
	db.fileListCalls++
	if db.files != nil {
		return db.files, true, nil
	}
	if db.file.RelativePath == "" {
		return nil, true, nil
	}
	return []UploadFileRecord{db.file}, true, nil
}

func (db *siteReadServiceDatabase) SavePolicyViolation(ctx context.Context, violation PolicyViolation) error {
	db.savedViolations = append(db.savedViolations, violation)
	return nil
}

func (db *siteReadServiceDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	db.resolvedViolations = append(db.resolvedViolations, siteSHA+":"+strconv.FormatInt(version, 10)+":"+key)
	return nil
}
