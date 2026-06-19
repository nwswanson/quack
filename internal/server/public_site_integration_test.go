package server

import (
	"net/http"
	"net/http/httptest"
	"quack/internal/domain"
	"testing"
)

func TestNginxStyleStaticRouting(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "blog-index", "blog index")
	writeTestBlob(t, root, "file-js", "file js")

	srv := New("", "", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "blog/index.html"): {
				RelativePath: "blog/index.html",
				BlobPath:     "blog-index",
			},
			fileKey("foo", "file.js"): {
				RelativePath: "file.js",
				BlobPath:     "file-js",
			},
		},
	}, DefaultOptions())

	tests := map[string]struct {
		path     string
		status   int
		location string
		body     string
	}{
		"directory slash serves index": {
			path:   "/blog/",
			status: http.StatusOK,
			body:   "blog index",
		},
		"index file serves directly": {
			path:   "/blog/index.html",
			status: http.StatusOK,
			body:   "blog index",
		},
		"directory without slash redirects": {
			path:     "/blog",
			status:   http.StatusMovedPermanently,
			location: "/blog/",
		},
		"exact file still wins": {
			path:   "/file.js",
			status: http.StatusOK,
			body:   "file js",
		},
		"missing path is not an index": {
			path:   "/missing",
			status: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Host = "foo.example.com"
		rec := httptest.NewRecorder()
		srv.Public.Handler.ServeHTTP(rec, req)

		if rec.Code != tc.status {
			t.Fatalf("%s: status = %d, want %d; body=%s", name, rec.Code, tc.status, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != tc.location {
			t.Fatalf("%s: location = %q, want %q", name, got, tc.location)
		}
		if tc.body != "" && rec.Body.String() != tc.body {
			t.Fatalf("%s: body = %q, want %q", name, rec.Body.String(), tc.body)
		}
	}
}

func TestWwwHostServesSiteFromSecondLabel(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "site-index", "site index")

	srv := New("", "", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("nathanielswanson", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "site-index",
			},
		},
	}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "www.nathanielswanson.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}

func TestDefaultSiteFallbackForUnknownSite(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "default-index", "default index")
	db := &fakeDatabase{
		settings: domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, DefaultSite: "home", LogLevel: "warn"},
		files: map[string]domain.UploadFileRecord{
			fileKey("home", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "default-index",
			},
		},
	}
	srv := New("", "", "", fakeStorage{root: root}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "missing.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "default index" {
		t.Fatalf("body = %q, want default index", rec.Body.String())
	}
}

func TestDefaultSiteDoesNotHandleMissingPathForExistingSite(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "default-file", "default file")
	writeTestBlob(t, root, "foo-index", "foo index")
	db := &fakeDatabase{
		settings: domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, DefaultSite: "home", LogLevel: "warn"},
		files: map[string]domain.UploadFileRecord{
			fileKey("home", "missing.html"): {
				RelativePath: "missing.html",
				BlobPath:     "default-file",
			},
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "foo-index",
			},
		},
	}
	srv := New("", "", "", fakeStorage{root: root}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/missing.html", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestExplicitServePathIsDisabled(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "blog-index", "blog index")

	srv := New("", "", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "blog/index.html"): {
				RelativePath: "blog/index.html",
				BlobPath:     "blog-index",
			},
		},
	}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/serve/foo/blog", nil)
	req.Host = "anything.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestSiteHostRootStillServesSite(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "index", "site index")
	srv := New("", "", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "index",
			},
		},
	}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}
