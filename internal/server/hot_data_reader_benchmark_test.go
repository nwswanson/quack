package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkHotDataReaderFindCurrentSiteFile(b *testing.B) {
	source := staticHotDataReader{
		file: UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        40643,
		},
		fileOK:     true,
		siteExists: true,
	}
	benchCurrentFileReader(b, "passthrough", NewPassthroughHotDataReader(source))
	benchCurrentFileReader(b, "memory", NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
	benchCurrentFileReader(b, "otter", NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func BenchmarkHotDataReaderServeMetadata(b *testing.B) {
	source := staticHotDataReader{
		settings: ServerSettings{DefaultSite: "", MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles},
		manifests: []CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{SettingDatabaseFeature: "false", SettingDatabaseFeatureRequired: "false"},
		}},
		file: UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        40643,
		},
		fileOK:     true,
		siteExists: true,
	}
	benchServeMetadataReader(b, "passthrough", NewPassthroughHotDataReader(source))
	benchServeMetadataReader(b, "memory", NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
	benchServeMetadataReader(b, "otter", NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func BenchmarkServeFileWithBlob(b *testing.B) {
	root := b.TempDir()
	blobPath := filepath.Join(root, "index-blob")
	body := make([]byte, 40643)
	for i := range body {
		body[i] = 'x'
	}
	if err := os.WriteFile(blobPath, body, 0o644); err != nil {
		b.Fatal(err)
	}

	source := staticHotDataReader{
		settings: ServerSettings{DefaultSite: "", MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles},
		manifests: []CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{SettingDatabaseFeature: "false", SettingDatabaseFeatureRequired: "false"},
		}},
		file: UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     blobPath,
			FileSHA:      "file-sha",
			Bytes:        int64(len(body)),
		},
		fileOK:     true,
		siteExists: true,
	}
	benchServeFile(b, "passthrough", passthroughHotDataReader{db: source})
	benchServeFile(b, "otter", NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func benchCurrentFileReader(b *testing.B, name string, reader HotDataReader) {
	ctx := context.Background()
	if _, _, _, err := reader.FindCurrentSiteFile(ctx, "example.com", "index.html"); err != nil {
		b.Fatal(err)
	}
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				file, ok, siteExists, err := reader.FindCurrentSiteFile(ctx, "example.com", "index.html")
				if err != nil || !ok || !siteExists || file.BlobPath == "" {
					b.Fatalf("FindCurrentSiteFile = (%+v, %v, %v, %v)", file, ok, siteExists, err)
				}
			}
		})
	})
}

func benchServeMetadataReader(b *testing.B, name string, reader HotDataReader) {
	ctx := context.Background()
	read := NewSiteReadService(reader)
	if _, err := read.ServerSettings(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := read.CurrentSiteRuntime(ctx, "example.com"); err != nil {
		b.Fatal(err)
	}
	if _, _, _, err := read.CurrentSiteFile(ctx, "example.com", "index.html"); err != nil {
		b.Fatal(err)
	}
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := read.ServerSettings(ctx); err != nil {
					b.Fatal(err)
				}
				if _, err := read.CurrentSiteRuntime(ctx, "example.com"); err != nil {
					b.Fatal(err)
				}
				file, ok, siteExists, err := read.CurrentSiteFile(ctx, "example.com", "index.html")
				if err != nil || !ok || !siteExists || file.BlobPath == "" {
					b.Fatalf("CurrentSiteFile = (%+v, %v, %v, %v)", file, ok, siteExists, err)
				}
			}
		})
	})
}

func benchServeFile(b *testing.B, name string, reader HotDataReader) {
	h := &handler{
		store: staticStore{},
		read:  NewSiteReadService(reader),
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	h.handleServeFile(&discardResponseWriter{header: http.Header{}}, req)
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				w := &discardResponseWriter{header: http.Header{}}
				h.handleServeFile(w, req)
				if w.status != http.StatusOK {
					b.Fatalf("status = %d, want %d", w.status, http.StatusOK)
				}
			}
		})
	})
}

type staticHotDataReader struct {
	settings   ServerSettings
	manifests  []CurrentSiteManifest
	file       UploadFileRecord
	fileOK     bool
	siteExists bool
}

func (r staticHotDataReader) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	return r.settings, nil
}

func (r staticHotDataReader) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	return nil, nil
}

func (r staticHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, nil
}

func (r staticHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	return r.manifests, nil
}

func (r staticHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	return nil, nil
}

func (r staticHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return r.file, r.fileOK, r.siteExists, nil
}

func (r staticHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]UploadFileRecord, bool, error) {
	if !r.fileOK {
		return nil, r.siteExists, nil
	}
	return []UploadFileRecord{r.file}, r.siteExists, nil
}

type staticStore struct{}

func (staticStore) AcceptFile(ctx context.Context, file StoredFile) (StoredFileResult, error) {
	return StoredFileResult{}, nil
}

func (staticStore) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	return os.Open(blobPath)
}

func (staticStore) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	return nil
}

func (staticStore) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

type discardResponseWriter struct {
	header http.Header
	status int
}

func (w *discardResponseWriter) Header() http.Header {
	return w.header
}

func (w *discardResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *discardResponseWriter) WriteHeader(status int) {
	w.status = status
}
