package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"quack/internal/cache"
	"quack/internal/domain"
	"quack/internal/publichttp"
	appruntime "quack/internal/runtime"
	"quack/internal/settings"
	"quack/internal/storage"
	"testing"
	"time"

	"quack/internal/sites"
	"quack/internal/statichttp"
)

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
		settings: domain.ServerSettings{DefaultSite: "", MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles},
		manifests: []domain.CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{settings.SettingDatabaseFeature: "false", settings.SettingDatabaseFeatureRequired: "false"},
		}},
		file: domain.UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     blobPath,
			FileSHA:      "file-sha",
			Bytes:        int64(len(body)),
		},
		fileOK:     true,
		siteExists: true,
	}
	benchServeFile(b, "passthrough", cache.NewPassthroughHotDataReader(source))
	benchServeFile(b, "otter", cache.NewOtterHotDataReader(source, cache.OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func benchServeFile(b *testing.B, name string, reader cache.HotDataReader) {
	staticHandler := statichttp.New(staticStore{}, sites.NewSiteReadService(reader))
	h := publichttp.New(staticHandler)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	mux.ServeHTTP(&discardResponseWriter{header: http.Header{}}, req)
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				w := &discardResponseWriter{header: http.Header{}}
				mux.ServeHTTP(w, req)
				if w.status != http.StatusOK {
					b.Fatalf("status = %d, want %d", w.status, http.StatusOK)
				}
			}
		})
	})
}

type staticHotDataReader struct {
	settings   domain.ServerSettings
	manifests  []domain.CurrentSiteManifest
	file       domain.UploadFileRecord
	fileOK     bool
	siteExists bool
}

func (r staticHotDataReader) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return r.settings, nil
}

func (r staticHotDataReader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	return nil, nil
}

func (r staticHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, nil
}

func (r staticHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return r.manifests, nil
}

func (r staticHotDataReader) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	return nil, nil
}

func (r staticHotDataReader) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	return nil, nil
}

func (r staticHotDataReader) ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error) {
	return nil, true, nil
}

func (r staticHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	return nil, nil
}

func (r staticHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return r.file, r.fileOK, r.siteExists, nil
}

func (r staticHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	if !r.fileOK {
		return nil, r.siteExists, nil
	}
	return []domain.UploadFileRecord{r.file}, r.siteExists, nil
}

type staticStore struct{}

func (staticStore) AcceptFile(ctx context.Context, file storage.StoredFile) (storage.StoredFileResult, error) {
	return storage.StoredFileResult{}, nil
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
