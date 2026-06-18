package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestOtterHotDataReaderCachesServerSettingsUntilTTL(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{
		TTL:         time.Second,
		NegativeTTL: time.Second,
	})

	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("first GetServerSettings error = %v", err)
	}
	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("second GetServerSettings error = %v", err)
	}
	if source.serverSettingsCalls != 1 {
		t.Fatalf("server settings calls = %d, want 1", source.serverSettingsCalls)
	}

	time.Sleep(1100 * time.Millisecond)
	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("expired GetServerSettings error = %v", err)
	}
	if source.serverSettingsCalls != 2 {
		t.Fatalf("server settings calls after expiry = %d, want 2", source.serverSettingsCalls)
	}
}

func TestOtterHotDataReaderCachesNegativeFileLookup(t *testing.T) {
	source := &countingHotDataReader{
		file:       UploadFileRecord{},
		fileOK:     false,
		siteExists: true,
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

	for i := 0; i < 2; i++ {
		_, ok, siteExists, err := hot.FindCurrentSiteFile(context.Background(), "example.com", "missing.html")
		if err != nil {
			t.Fatalf("FindCurrentSiteFile error = %v", err)
		}
		if ok || !siteExists {
			t.Fatalf("FindCurrentSiteFile = ok=%v siteExists=%v, want missing file on existing site", ok, siteExists)
		}
	}
	if source.fileCalls != 1 {
		t.Fatalf("file calls = %d, want 1", source.fileCalls)
	}
}

func TestOtterHotDataReaderDoesNotCacheErrors(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		err:      fmt.Errorf("database unavailable"),
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

	if _, err := hot.GetServerSettings(context.Background()); err == nil {
		t.Fatalf("first GetServerSettings error = nil, want error")
	}
	source.err = nil
	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("second GetServerSettings error = %v, want success", err)
	}
	if source.serverSettingsCalls != 2 {
		t.Fatalf("server settings calls = %d, want 2", source.serverSettingsCalls)
	}
}

func TestOtterHotDataReaderSingleflightsConcurrentMisses(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		block:    make(chan struct{}),
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			if _, err := hot.GetServerSettings(context.Background()); err != nil {
				t.Errorf("GetServerSettings error = %v", err)
			}
		}()
	}
	for {
		source.mu.Lock()
		calls := source.serverSettingsCalls
		source.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(source.block)
	wg.Wait()
	if source.serverSettingsCalls != 1 {
		t.Fatalf("server settings calls = %d, want 1", source.serverSettingsCalls)
	}
}

func TestOtterHotDataReaderReturnsMutableCopies(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn", Locked: map[string]bool{"default_site": true}},
		manifests: []CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{SettingDatabaseFeature: "true"},
		}},
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

	settings, err := hot.GetServerSettings(context.Background())
	if err != nil {
		t.Fatalf("GetServerSettings error = %v", err)
	}
	settings.Locked["default_site"] = false
	settingsAgain, err := hot.GetServerSettings(context.Background())
	if err != nil {
		t.Fatalf("second GetServerSettings error = %v", err)
	}
	if !settingsAgain.Locked["default_site"] {
		t.Fatalf("cached server settings Locked map was mutated")
	}

	manifests, err := hot.ListCurrentSiteManifests(context.Background())
	if err != nil {
		t.Fatalf("ListCurrentSiteManifests error = %v", err)
	}
	manifests[0].Settings[SettingDatabaseFeature] = "false"
	manifestsAgain, err := hot.ListCurrentSiteManifests(context.Background())
	if err != nil {
		t.Fatalf("second ListCurrentSiteManifests error = %v", err)
	}
	if manifestsAgain[0].Settings[SettingDatabaseFeature] != "true" {
		t.Fatalf("cached manifest settings map was mutated")
	}
}

func TestOtterHotDataReaderInvalidation(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		file:     UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", Bytes: 10},
		fileOK:   true,
	}
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute})

	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("GetServerSettings error = %v", err)
	}
	if err := hot.InvalidateServerSettings(context.Background()); err != nil {
		t.Fatalf("InvalidateServerSettings error = %v", err)
	}
	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("GetServerSettings after invalidation error = %v", err)
	}
	if source.serverSettingsCalls != 2 {
		t.Fatalf("server settings calls = %d, want 2", source.serverSettingsCalls)
	}

	if _, _, _, err := hot.FindCurrentSiteFile(context.Background(), "example.com", "index.html"); err != nil {
		t.Fatalf("FindCurrentSiteFile error = %v", err)
	}
	if err := hot.InvalidateSite(context.Background(), "example.com"); err != nil {
		t.Fatalf("InvalidateSite error = %v", err)
	}
	if _, _, _, err := hot.FindCurrentSiteFile(context.Background(), "example.com", "index.html"); err != nil {
		t.Fatalf("FindCurrentSiteFile after invalidation error = %v", err)
	}
	if source.fileCalls != 2 {
		t.Fatalf("file calls = %d, want 2", source.fileCalls)
	}
}

func TestOtterHotDataReaderReloadsCurrentFileAfterSiteUpdate(t *testing.T) {
	db := &siteUpdateCacheDatabase{
		file: UploadFileRecord{RelativePath: "index.html", BlobPath: "old-blob", Bytes: 10},
	}
	hot := NewOtterHotDataReader(db, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute})
	write := NewSiteWriteService(db, hot, hot)
	ctx := context.Background()

	file, ok, siteExists, err := hot.FindCurrentSiteFile(ctx, "example.com", "index.html")
	if err != nil {
		t.Fatalf("FindCurrentSiteFile error = %v", err)
	}
	if !ok || !siteExists || file.BlobPath != "old-blob" {
		t.Fatalf("FindCurrentSiteFile = (%+v, %v, %v), want old cached file", file, ok, siteExists)
	}

	db.file = UploadFileRecord{RelativePath: "index.html", BlobPath: "new-blob", Bytes: 20}
	if err := write.FinishUpload(ctx, UploadRecord{Site: "example.com", SiteSHA: "site-sha", Version: 2}); err != nil {
		t.Fatalf("FinishUpload error = %v", err)
	}

	file, ok, siteExists, err = hot.FindCurrentSiteFile(ctx, "example.com", "index.html")
	if err != nil {
		t.Fatalf("FindCurrentSiteFile after update error = %v", err)
	}
	if !ok || !siteExists || file.BlobPath != "new-blob" {
		t.Fatalf("FindCurrentSiteFile after update = (%+v, %v, %v), want reloaded file", file, ok, siteExists)
	}
	if db.fileCalls != 2 {
		t.Fatalf("file calls = %d, want 2", db.fileCalls)
	}
}

type siteUpdateCacheDatabase struct {
	Database
	file      UploadFileRecord
	fileCalls int
}

func (db *siteUpdateCacheDatabase) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	return ServerSettings{}, nil
}

func (db *siteUpdateCacheDatabase) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	return nil, nil
}

func (db *siteUpdateCacheDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, nil
}

func (db *siteUpdateCacheDatabase) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	return nil, nil
}

func (db *siteUpdateCacheDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	return nil, nil
}

func (db *siteUpdateCacheDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	db.fileCalls++
	return db.file, db.file.RelativePath == relativePath, true, nil
}

func (db *siteUpdateCacheDatabase) SaveServerSettings(ctx context.Context, settings ServerSettings) error {
	return nil
}

func (db *siteUpdateCacheDatabase) SavePolicy(ctx context.Context, policy PolicyRecord) error {
	return nil
}

func (db *siteUpdateCacheDatabase) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	return nil
}

func (db *siteUpdateCacheDatabase) FinishUpload(ctx context.Context, upload UploadRecord) error {
	return nil
}

func (db *siteUpdateCacheDatabase) RollbackSite(ctx context.Context, user AdminUser, site string, siteSHA string) (RollbackRecord, error) {
	return RollbackRecord{}, nil
}

func (db *siteUpdateCacheDatabase) UnpublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (UnpublishRecord, error) {
	return UnpublishRecord{}, nil
}

func (db *siteUpdateCacheDatabase) PublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (PublishRecord, error) {
	return PublishRecord{}, nil
}

func (db *siteUpdateCacheDatabase) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return true, nil
}

func (db *siteUpdateCacheDatabase) SavePolicyViolation(ctx context.Context, violation PolicyViolation) error {
	return nil
}

func (db *siteUpdateCacheDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	return nil
}
