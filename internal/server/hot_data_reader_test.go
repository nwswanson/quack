package server

import (
	"context"
	"testing"
)

func TestPassthroughHotDataReaderCopiesMutableResults(t *testing.T) {
	db := &readerBackingDatabase{
		settings: ServerSettings{
			MaxUploadBytes: DefaultMaxUploadBytes,
			MaxUploadFiles: DefaultMaxUploadFiles,
			LogLevel:       "warn",
			Locked:         map[string]bool{"default_site": true},
		},
		policies: []PolicyRecord{{ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: "allow"}},
		uploadSettings: map[string]string{
			SettingDatabaseFeature: "true",
		},
		manifests: []CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  3,
			Settings: map[string]string{SettingDatabaseFeatureRequired: "true"},
		}},
		violations: []PolicyViolation{{SiteSHA: "site-sha", UploadVersion: 3, Key: SettingDatabaseFeature}},
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

	policies, err := reader.LoadPolicies(ctx, []PolicyScope{{Type: ScopeSystem, ID: ""}})
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
	uploadSettings[SettingDatabaseFeature] = "false"
	if db.uploadSettings[SettingDatabaseFeature] != "true" {
		t.Fatalf("LoadUploadSettings leaked map mutation")
	}

	manifests, err := reader.ListCurrentSiteManifests(ctx)
	if err != nil {
		t.Fatalf("ListCurrentSiteManifests error = %v", err)
	}
	manifests[0].Site = "mutated.example"
	manifests[0].Settings[SettingDatabaseFeatureRequired] = "false"
	if db.manifests[0].Site != "example.com" || db.manifests[0].Settings[SettingDatabaseFeatureRequired] != "true" {
		t.Fatalf("ListCurrentSiteManifests leaked mutable result")
	}

	violations, err := reader.ListPolicyViolations(ctx, "site-sha", 3)
	if err != nil {
		t.Fatalf("ListPolicyViolations error = %v", err)
	}
	violations[0].Key = "mutated"
	if db.violations[0].Key != SettingDatabaseFeature {
		t.Fatalf("ListPolicyViolations leaked slice mutation")
	}
}

func TestPassthroughHotDataReaderFindCurrentSiteFileDelegates(t *testing.T) {
	db := &readerBackingDatabase{
		file: UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", FileSHA: "sha", Bytes: 12},
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
	Database
	settings       ServerSettings
	policies       []PolicyRecord
	uploadSettings map[string]string
	manifests      []CurrentSiteManifest
	violations     []PolicyViolation
	file           UploadFileRecord
}

func (db *readerBackingDatabase) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	return db.settings, nil
}

func (db *readerBackingDatabase) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	return db.policies, nil
}

func (db *readerBackingDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return db.uploadSettings, nil
}

func (db *readerBackingDatabase) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	return db.manifests, nil
}

func (db *readerBackingDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	return db.violations, nil
}

func (db *readerBackingDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return db.file, true, true, nil
}
