package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryHotDataReaderCachesServerSettingsUntilTTL(t *testing.T) {
	now := time.Unix(100, 0)
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{
		TTL:         time.Second,
		NegativeTTL: time.Second,
		Now:         func() time.Time { return now },
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

	now = now.Add(2 * time.Second)
	if _, err := hot.GetServerSettings(context.Background()); err != nil {
		t.Fatalf("expired GetServerSettings error = %v", err)
	}
	if source.serverSettingsCalls != 2 {
		t.Fatalf("server settings calls after expiry = %d, want 2", source.serverSettingsCalls)
	}
}

func TestMemoryHotDataReaderCachesNegativeFileLookup(t *testing.T) {
	source := &countingHotDataReader{
		file:       UploadFileRecord{},
		fileOK:     false,
		siteExists: true,
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

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

func TestMemoryHotDataReaderDoesNotCacheErrors(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		err:      fmt.Errorf("database unavailable"),
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

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

func TestMemoryHotDataReaderSingleflightsConcurrentMisses(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		block:    make(chan struct{}),
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

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

func TestMemoryHotDataReaderReturnsMutableCopies(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn", Locked: map[string]bool{"default_site": true}},
		manifests: []CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{SettingDatabaseFeature: "true"},
		}},
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Second, NegativeTTL: time.Second})

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

func TestMemoryHotDataReaderInvalidation(t *testing.T) {
	source := &countingHotDataReader{
		settings: ServerSettings{MaxUploadBytes: 1, MaxUploadFiles: 2, LogLevel: "warn"},
		file:     UploadFileRecord{RelativePath: "index.html", BlobPath: "blob", Bytes: 10},
		fileOK:   true,
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute})

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

func TestMemoryHotDataReaderCachesServeSiteFileDecision(t *testing.T) {
	source := &countingHotDataReader{
		serveDecision: ServeSiteFileDecision{
			Status:       ServeSiteFileFound,
			Site:         "example.com",
			RelativePath: "index.html",
			File:         UploadFileRecord{RelativePath: "index.html", BlobPath: "old-blob"},
		},
	}
	hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute})

	first, err := hot.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("ServeSiteFile error = %v", err)
	}
	source.serveDecision.File.BlobPath = "new-blob"
	second, err := hot.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("second ServeSiteFile error = %v", err)
	}
	if first.File.BlobPath != "old-blob" || second.File.BlobPath != "old-blob" {
		t.Fatalf("ServeSiteFile decisions = %+v then %+v, want cached old blob", first, second)
	}
	if source.serveCalls != 1 {
		t.Fatalf("serve calls = %d, want 1", source.serveCalls)
	}

	if err := hot.InvalidateSite(context.Background(), "example.com"); err != nil {
		t.Fatalf("InvalidateSite error = %v", err)
	}
	third, err := hot.ServeSiteFile(context.Background(), "example.com", "/")
	if err != nil {
		t.Fatalf("ServeSiteFile after invalidation error = %v", err)
	}
	if third.File.BlobPath != "new-blob" {
		t.Fatalf("ServeSiteFile after invalidation = %+v, want new blob", third)
	}
	if source.serveCalls != 2 {
		t.Fatalf("serve calls after invalidation = %d, want 2", source.serveCalls)
	}
}

type countingHotDataReader struct {
	mu sync.Mutex

	settings      ServerSettings
	manifests     []CurrentSiteManifest
	file          UploadFileRecord
	fileOK        bool
	siteExists    bool
	files         []UploadFileRecord
	serveDecision ServeSiteFileDecision
	err           error
	block         chan struct{}

	serverSettingsCalls int
	manifestCalls       int
	fileCalls           int
	filesCalls          int
	serveCalls          int
}

func (r *countingHotDataReader) wait() {
	if r.block != nil {
		<-r.block
	}
}

func (r *countingHotDataReader) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	r.mu.Lock()
	r.serverSettingsCalls++
	r.mu.Unlock()
	r.wait()
	if r.err != nil {
		return ServerSettings{}, r.err
	}
	return r.settings, nil
}

func (r *countingHotDataReader) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	return nil, r.err
}

func (r *countingHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, r.err
}

func (r *countingHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	r.mu.Lock()
	r.manifestCalls++
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return r.manifests, nil
}

func (r *countingHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	return nil, r.err
}

func (r *countingHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	r.mu.Lock()
	r.fileCalls++
	r.mu.Unlock()
	if r.err != nil {
		return UploadFileRecord{}, false, false, r.err
	}
	return r.file, r.fileOK, r.siteExists, nil
}

func (r *countingHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]UploadFileRecord, bool, error) {
	r.mu.Lock()
	r.filesCalls++
	r.mu.Unlock()
	if r.err != nil {
		return nil, false, r.err
	}
	if r.files != nil {
		return r.files, r.siteExists, nil
	}
	if r.file.RelativePath == "" {
		return nil, r.siteExists, nil
	}
	return []UploadFileRecord{r.file}, r.siteExists, nil
}

func (r *countingHotDataReader) ServeSiteFile(ctx context.Context, site string, urlPath string) (ServeSiteFileDecision, error) {
	r.mu.Lock()
	r.serveCalls++
	r.mu.Unlock()
	if r.err != nil {
		return ServeSiteFileDecision{}, r.err
	}
	return r.serveDecision, nil
}
