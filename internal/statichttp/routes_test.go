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
			File:         domain.UploadFileRecord{BlobPath: "index-blob", FileSHA: "index-sha"},
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
	if got := rec.Header().Get("Cache-Control"); got != "public, no-cache, must-revalidate" {
		t.Fatalf("cache-control = %q, want revalidate policy", got)
	}
	if got := rec.Header().Get("CDN-Cache-Control"); got != "no-cache" {
		t.Fatalf("cdn-cache-control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Cloudflare-CDN-Cache-Control"); got != "no-cache" {
		t.Fatalf("cloudflare-cdn-cache-control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("ETag"); got != `"index-sha"` {
		t.Fatalf("etag = %q, want %q", got, `"index-sha"`)
	}
}

func TestHandlerRevalidatesStaticBlobWithETag(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app-blob"), []byte("console.log('fresh')"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(testStore{root: root}, testReadService{
		decision: sites.ServeSiteFileDecision{
			Status:       sites.ServeSiteFileFound,
			Site:         "foo",
			RelativePath: "app.js",
			File:         domain.UploadFileRecord{BlobPath: "app-blob", FileSHA: "app-sha"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("If-None-Match", `"app-sha"`)
	rec := httptest.NewRecorder()
	h.ServeSiteFile(rec, req, Request{Site: "foo", URLPath: "/app.js"})

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotModified, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", rec.Body.Len())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, no-cache, must-revalidate" {
		t.Fatalf("cache-control = %q, want revalidate policy", got)
	}
	if got := rec.Header().Get("ETag"); got != `"app-sha"` {
		t.Fatalf("etag = %q, want %q", got, `"app-sha"`)
	}
}

func TestHandlerCanServeStaticBlobWithAntiCacheHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app-blob"), []byte("console.log('fresh')"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(testStore{root: root}, testReadService{
		settings: domain.ServerSettings{HTTPCacheMode: "anti_cache"},
		decision: sites.ServeSiteFileDecision{
			Status:       sites.ServeSiteFileFound,
			Site:         "foo",
			RelativePath: "app.js",
			File:         domain.UploadFileRecord{BlobPath: "app-blob", FileSHA: "app-sha"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeSiteFile(rec, req, Request{Site: "foo", URLPath: "/app.js"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, max-age=0, must-revalidate" {
		t.Fatalf("cache-control = %q, want anti-cache policy", got)
	}
	if got := rec.Header().Get("CDN-Cache-Control"); got != "no-store" {
		t.Fatalf("cdn-cache-control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Cloudflare-CDN-Cache-Control"); got != "no-store" {
		t.Fatalf("cloudflare-cdn-cache-control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("pragma = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Expires"); got != "0" {
		t.Fatalf("expires = %q, want 0", got)
	}
}

func TestHandlerCanServeStaticBlobWithMaxAgeHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app-blob"), []byte("console.log('fresh')"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(testStore{root: root}, testReadService{
		settings: domain.ServerSettings{HTTPCacheMode: "max_age", HTTPCacheMaxAgeSeconds: 14400},
		decision: sites.ServeSiteFileDecision{
			Status:       sites.ServeSiteFileFound,
			Site:         "foo",
			RelativePath: "app.js",
			File:         domain.UploadFileRecord{BlobPath: "app-blob", FileSHA: "app-sha"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeSiteFile(rec, req, Request{Site: "foo", URLPath: "/app.js"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, header := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control"} {
		if got := rec.Header().Get(header); got != "public, max-age=14400" {
			t.Fatalf("%s = %q, want public, max-age=14400", header, got)
		}
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
	settings domain.ServerSettings
	decision sites.ServeSiteFileDecision
}

func (r testReadService) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return r.settings, nil
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

func (r testReadService) SystemRuntimeWebSocketPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

type testStore struct {
	appstorage.Storage
	root string
}

func (s testStore) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	return os.Open(filepath.Join(s.root, blobPath))
}
