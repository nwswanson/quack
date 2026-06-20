package statichttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

func TestHandlerServesBlobForStaticRequest(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeSiteFile(rec, req, Request{Site: "foo", URLPath: "/"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}

func TestHandlerPreservesDirectoryRedirect(t *testing.T) {
	h := New(testStore{}, testReadService{
		decision: sites.ServeSiteFileDecision{
			Status: sites.ServeSiteFileDirectoryRedirect,
			Site:   "foo",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/blog?x=1", nil)
	rec := httptest.NewRecorder()
	h.ServeSiteFile(rec, req, Request{Site: "foo", URLPath: "/blog"})

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/blog/?x=1" {
		t.Fatalf("location = %q, want /blog/?x=1", got)
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

func (r testReadService) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, siteManifest manifest.Manifest) error {
	return nil
}

func (r testReadService) CurrentSiteServingStatus(ctx context.Context, site string) (domain.SiteServingDecision, error) {
	return domain.SiteServingDecision{}, nil
}

func (r testReadService) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return domain.UploadFileRecord{}, false, false, nil
}

func (r testReadService) ServeSiteFile(ctx context.Context, site string, urlPath string, routePath string, staticRoot string, staticFile string) (sites.ServeSiteFileDecision, error) {
	return r.decision, nil
}

func (r testReadService) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

func (r testReadService) SystemRuntimeHTTPPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

type testStore struct {
	appstorage.Storage
	root string
}

func (s testStore) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	return os.Open(filepath.Join(s.root, blobPath))
}
