package server

import (
	"context"
	"errors"
	"strconv"
	"testing"
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
	read := NewSiteReadService(db, nil)

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
			read := NewSiteReadService(db, nil)

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
	read := NewSiteReadService(db, nil)

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

func TestSiteReadServiceCurrentSiteFileDelegatesToCache(t *testing.T) {
	db := &siteReadServiceDatabase{
		file: UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", FileSHA: "sha", Bytes: 42},
	}
	read := NewSiteReadService(db, nil)

	file, fileOK, siteOK, err := read.CurrentSiteFile(context.Background(), "example.com", "index.html")
	if err != nil {
		t.Fatalf("CurrentSiteFile error = %v", err)
	}
	if !fileOK || !siteOK || file.Bytes != 42 {
		t.Fatalf("CurrentSiteFile = (%+v, %v, %v), want delegated file", file, fileOK, siteOK)
	}
}

func TestSiteReadServiceReconcilePolicyViolations(t *testing.T) {
	tests := []struct {
		name         string
		settings     map[string]string
		policies     []PolicyRecord
		wantSaved    PolicyViolation
		wantResolved bool
	}{
		{
			name: "saves degraded violation when optional database feature is denied",
			settings: map[string]string{
				SettingDatabaseFeature:         "true",
				SettingDatabaseFeatureRequired: "false",
			},
			policies: []PolicyRecord{{ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: "deny", Reason: "disabled"}},
			wantSaved: PolicyViolation{
				SiteSHA: "site-sha", UploadVersion: 7, Key: SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: "degraded", Reason: "disabled",
			},
		},
		{
			name: "saves suspended violation when required database feature is denied",
			settings: map[string]string{
				SettingDatabaseFeature:         "true",
				SettingDatabaseFeatureRequired: "true",
			},
			policies: []PolicyRecord{{ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: "deny", Reason: "disabled"}},
			wantSaved: PolicyViolation{
				SiteSHA: "site-sha", UploadVersion: 7, Key: SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: "suspended", Reason: "disabled",
			},
		},
		{
			name: "resolves violation when database feature is allowed",
			settings: map[string]string{
				SettingDatabaseFeature:         "true",
				SettingDatabaseFeatureRequired: "true",
			},
			policies:     []PolicyRecord{{ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: "allow"}},
			wantResolved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &siteReadServiceDatabase{
				manifests: []CurrentSiteManifest{{
					Site:     "example.com",
					SiteSHA:  "site-sha",
					Version:  7,
					Settings: tt.settings,
				}},
				policies: tt.policies,
			}
			read := NewSiteReadService(db, nil)

			if err := read.ReconcilePolicyViolations(context.Background()); err != nil {
				t.Fatalf("ReconcilePolicyViolations error = %v", err)
			}
			if tt.wantResolved {
				if len(db.resolvedViolations) != 1 {
					t.Fatalf("resolved violations = %d, want 1", len(db.resolvedViolations))
				}
				if got := db.resolvedViolations[0]; got != "site-sha:7:"+SettingDatabaseFeature {
					t.Fatalf("resolved violation = %q, want site-sha:7:%s", got, SettingDatabaseFeature)
				}
				return
			}
			if len(db.savedViolations) != 1 {
				t.Fatalf("saved violations = %d, want 1", len(db.savedViolations))
			}
			if got := db.savedViolations[0]; got != tt.wantSaved {
				t.Fatalf("saved violation = %+v, want %+v", got, tt.wantSaved)
			}
		})
	}
}

type siteReadServiceDatabase struct {
	Database
	settings           ServerSettings
	policies           []PolicyRecord
	manifests          []CurrentSiteManifest
	violations         map[string][]PolicyViolation
	file               UploadFileRecord
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

func (db *siteReadServiceDatabase) SavePolicyViolation(ctx context.Context, violation PolicyViolation) error {
	db.savedViolations = append(db.savedViolations, violation)
	return nil
}

func (db *siteReadServiceDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	db.resolvedViolations = append(db.resolvedViolations, siteSHA+":"+strconv.FormatInt(version, 10)+":"+key)
	return nil
}
