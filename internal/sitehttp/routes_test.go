package sitehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

func TestHandlerServesBlobFromHostSite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index-blob"), []byte("site index"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(testStore{root: root}, testReadService{
		decision: sites.ServeSiteFileDecision{
			Status:       sites.ServeSiteFileFound,
			Site:         "foo",
			RelativePath: "index.html",
			File:         domain.UploadFileRecord{BlobPath: "index-blob"},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}

type testReadService struct {
	decision sites.ServeSiteFileDecision
}

func (r testReadService) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return domain.ServerSettings{}, nil
}

func (r testReadService) UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error) {
	return domain.UploadPolicy{}, nil
}

func (r testReadService) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest protocol.SiteManifest) error {
	return nil
}

func (r testReadService) CurrentSiteRuntime(ctx context.Context, site string) (domain.SiteRuntimeDecision, error) {
	return domain.SiteRuntimeDecision{}, nil
}

func (r testReadService) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return domain.UploadFileRecord{}, false, false, nil
}

func (r testReadService) ServeSiteFile(ctx context.Context, site string, urlPath string) (sites.ServeSiteFileDecision, error) {
	return r.decision, nil
}

func (r testReadService) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

type testStore struct {
	appstorage.Storage
	root string
}

func (s testStore) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	return os.Open(filepath.Join(s.root, blobPath))
}
