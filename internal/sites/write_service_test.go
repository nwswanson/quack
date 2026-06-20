package sites

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"quack/internal/domain"
	appsettings "quack/internal/settings"
)

func TestSiteWriteServiceInvalidatesAfterSuccessfulWrites(t *testing.T) {
	db := &siteWriteServiceDatabase{}
	invalidator := &recordingInvalidator{}
	write := NewSiteWriteService(db, db, invalidator)
	ctx := context.Background()

	if err := write.SaveServerSettings(ctx, domain.ServerSettings{LogLevel: "warn"}); err != nil {
		t.Fatalf("SaveServerSettings error = %v", err)
	}
	if err := write.SavePolicy(ctx, domain.PolicyRecord{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow"}); err != nil {
		t.Fatalf("SavePolicy error = %v", err)
	}
	if err := write.SaveUploadSettings(ctx, "site-sha", 3, map[string]string{appsettings.SettingDatabaseFeature: "true"}); err != nil {
		t.Fatalf("SaveUploadSettings error = %v", err)
	}
	if err := write.FinishUpload(ctx, domain.UploadRecord{Site: "example.com", SiteSHA: "site-sha", Version: 3}); err != nil {
		t.Fatalf("FinishUpload error = %v", err)
	}

	assertContains(t, invalidator.calls, "settings")
	assertContains(t, invalidator.calls, "policies")
	assertContains(t, invalidator.calls, "site:example.com")
	assertContains(t, invalidator.calls, "version:site-sha:3")
}

func TestSiteWriteServiceDoesNotInvalidateFailedWrite(t *testing.T) {
	db := &siteWriteServiceDatabase{err: fmt.Errorf("write failed")}
	invalidator := &recordingInvalidator{}
	write := NewSiteWriteService(db, db, invalidator)

	if err := write.SaveServerSettings(context.Background(), domain.ServerSettings{LogLevel: "warn"}); err == nil {
		t.Fatalf("SaveServerSettings error = nil, want error")
	}
	if len(invalidator.calls) != 0 {
		t.Fatalf("invalidations = %v, want none", invalidator.calls)
	}
}

func TestSiteWriteServiceReconcilePolicyViolations(t *testing.T) {
	tests := []struct {
		name         string
		settings     map[string]string
		policies     []domain.PolicyRecord
		wantSaved    domain.PolicyViolation
		wantResolved bool
	}{
		{
			name: "saves degraded violation when optional database feature is denied",
			settings: map[string]string{
				appsettings.SettingDatabaseFeature:         "true",
				appsettings.SettingDatabaseFeatureRequired: "false",
			},
			policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "deny", Reason: "disabled"}},
			wantSaved: domain.PolicyViolation{
				SiteSHA: "site-sha", UploadVersion: 7, Key: appsettings.SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: "degraded", Reason: "disabled",
			},
		},
		{
			name: "saves suspended violation when required database feature is denied",
			settings: map[string]string{
				appsettings.SettingDatabaseFeature:         "true",
				appsettings.SettingDatabaseFeatureRequired: "true",
			},
			policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "deny", Reason: "disabled"}},
			wantSaved: domain.PolicyViolation{
				SiteSHA: "site-sha", UploadVersion: 7, Key: appsettings.SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: "suspended", Reason: "disabled",
			},
		},
		{
			name: "resolves violation when database feature is allowed",
			settings: map[string]string{
				appsettings.SettingDatabaseFeature:         "true",
				appsettings.SettingDatabaseFeatureRequired: "true",
			},
			policies:     []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: "allow"}},
			wantResolved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &siteWriteServiceDatabase{
				manifests: []domain.CurrentSiteManifest{{
					Site:     "example.com",
					SiteSHA:  "site-sha",
					Version:  7,
					Settings: tt.settings,
				}},
				policies: tt.policies,
			}
			invalidator := &recordingInvalidator{}
			write := NewSiteWriteService(db, db, invalidator)

			if err := write.ReconcilePolicyViolations(context.Background()); err != nil {
				t.Fatalf("ReconcilePolicyViolations error = %v", err)
			}
			assertContains(t, invalidator.calls, "version:site-sha:7")
			if tt.wantResolved {
				if len(db.resolvedViolations) != 1 {
					t.Fatalf("resolved violations = %d, want 1", len(db.resolvedViolations))
				}
				if got := db.resolvedViolations[0]; got != "site-sha:7:"+appsettings.SettingDatabaseFeature {
					t.Fatalf("resolved violation = %q, want site-sha:7:%s", got, appsettings.SettingDatabaseFeature)
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

type siteWriteServiceDatabase struct {
	settings  domain.ServerSettings
	policies  []domain.PolicyRecord
	manifests []domain.CurrentSiteManifest
	err       error

	savedViolations    []domain.PolicyViolation
	resolvedViolations []string
}

func (db *siteWriteServiceDatabase) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return db.settings, db.err
}

func (db *siteWriteServiceDatabase) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	if db.err != nil {
		return nil, db.err
	}
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

func (db *siteWriteServiceDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, db.err
}

func (db *siteWriteServiceDatabase) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return db.manifests, db.err
}

func (db *siteWriteServiceDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	return nil, db.err
}

func (db *siteWriteServiceDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return domain.UploadFileRecord{}, false, false, db.err
}

func (db *siteWriteServiceDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	return nil, false, db.err
}

func (db *siteWriteServiceDatabase) ServeSiteFile(ctx context.Context, site string, urlPath string, routePath string, staticRoot string, staticFile string) (ServeSiteFileDecision, error) {
	return ServeSiteFileDecision{}, db.err
}

func (db *siteWriteServiceDatabase) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	return db.err
}

func (db *siteWriteServiceDatabase) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	return db.err
}

func (db *siteWriteServiceDatabase) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	return db.err
}

func (db *siteWriteServiceDatabase) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
	return db.err
}

func (db *siteWriteServiceDatabase) SavePolicyViolation(ctx context.Context, violation domain.PolicyViolation) error {
	if db.err != nil {
		return db.err
	}
	db.savedViolations = append(db.savedViolations, violation)
	return nil
}

func (db *siteWriteServiceDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	if db.err != nil {
		return db.err
	}
	db.resolvedViolations = append(db.resolvedViolations, siteSHA+":"+strconv.FormatInt(version, 10)+":"+key)
	return nil
}

type recordingInvalidator struct {
	calls []string
}

func (i *recordingInvalidator) InvalidateServerSettings(ctx context.Context) error {
	i.calls = append(i.calls, "settings")
	return nil
}

func (i *recordingInvalidator) InvalidateSite(ctx context.Context, site string) error {
	i.calls = append(i.calls, "site:"+site)
	return nil
}

func (i *recordingInvalidator) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	i.calls = append(i.calls, "version:"+siteSHA+":"+strconv.FormatInt(version, 10))
	return nil
}

func (i *recordingInvalidator) InvalidatePolicies(ctx context.Context) error {
	i.calls = append(i.calls, "policies")
	return nil
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%v does not contain %q", values, want)
}
