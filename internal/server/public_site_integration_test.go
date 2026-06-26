package server

import (
	"net/http"
	"net/http/httptest"
	"quack/internal/domain"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
	"strings"
	"testing"
)

func TestNginxStyleStaticRouting(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "blog-index", "blog index")
	writeTestBlob(t, root, "file-js", "file js")

	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
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

func TestConfiguredStaticRootServesSubtreeAsURLRoot(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "private-index", "private index")
	writeTestBlob(t, root, "public-index", "public index")
	writeTestBlob(t, root, "public-docs", "public docs")

	db := &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "private-index",
			},
			fileKey("foo", "public/index.html"): {
				RelativePath: "public/index.html",
				BlobPath:     "public-index",
			},
			fileKey("foo", "docs/index.html"): {
				RelativePath: "docs/index.html",
				BlobPath:     "private-docs",
			},
			fileKey("foo", "public/docs/index.html"): {
				RelativePath: "public/docs/index.html",
				BlobPath:     "public-docs",
			},
		},
		sites: []domain.PublishedSite{{Site: "foo", SiteSHA: "foo-sha", CurrentVersion: 2}},
		uploadSettings: map[string]map[string]string{
			"foo-sha:2": {appsettings.SettingStaticRoot: "public"},
		},
	}
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

	tests := map[string]struct {
		path     string
		status   int
		location string
		body     string
	}{
		"root maps to public index": {
			path:   "/",
			status: http.StatusOK,
			body:   "public index",
		},
		"directory maps below public": {
			path:   "/docs/",
			status: http.StatusOK,
			body:   "public docs",
		},
		"directory redirect hides public prefix": {
			path:     "/docs",
			status:   http.StatusMovedPermanently,
			location: "/docs/",
		},
		"upload root file is above static root": {
			path:   "/index.html",
			status: http.StatusOK,
			body:   "public index",
		},
		"static root prefix is not an alternate route": {
			path:   "/public/index.html",
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

func TestStaticRouteRootServesSubtreeAsURLRoot(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "private-index", "private index")
	writeTestBlob(t, root, "public-index", "public index")
	writeTestBlob(t, root, "public-docs", "public docs")
	writeTestBlob(t, root, "public-asset", "public asset")

	db := &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "private-index",
			},
			fileKey("foo", "public/index.html"): {
				RelativePath: "public/index.html",
				BlobPath:     "public-index",
			},
			fileKey("foo", "public/docs/index.html"): {
				RelativePath: "public/docs/index.html",
				BlobPath:     "public-docs",
			},
			fileKey("foo", "public/assets/app.js"): {
				RelativePath: "public/assets/app.js",
				BlobPath:     "public-asset",
			},
		},
		sites: []domain.PublishedSite{{Site: "foo", SiteSHA: "foo-sha", CurrentVersion: 2}},
		uploadSettings: map[string]map[string]string{
			"foo-sha:2": {appsettings.SettingRoutes: `[{"path":"/","kind":"static","root":"public"}]`},
		},
	}
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

	tests := map[string]struct {
		path     string
		status   int
		location string
		body     string
	}{
		"root maps to route root index": {
			path:   "/",
			status: http.StatusOK,
			body:   "public index",
		},
		"directory maps below route root": {
			path:   "/docs/",
			status: http.StatusOK,
			body:   "public docs",
		},
		"directory redirect hides route root": {
			path:     "/docs",
			status:   http.StatusMovedPermanently,
			location: "/docs/",
		},
		"upload root file is above route root": {
			path:   "/index.html",
			status: http.StatusOK,
			body:   "public index",
		},
		"route root prefix is not an alternate route": {
			path:   "/public/index.html",
			status: http.StatusNotFound,
		},
		"asset maps below route root": {
			path:   "/assets/app.js",
			status: http.StatusOK,
			body:   "public asset",
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

func TestStaticRouteFileServesExactArchiveFile(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "public-index", "public index")
	writeTestBlob(t, root, "public-favicon", "public favicon")
	writeTestBlob(t, root, "media-favicon", "media favicon")
	writeTestBlob(t, root, "fallback-details", "fallback details")

	db := &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "public/index.html"): {
				RelativePath: "public/index.html",
				BlobPath:     "public-index",
			},
			fileKey("foo", "public/favicon.ico"): {
				RelativePath: "public/favicon.ico",
				BlobPath:     "public-favicon",
			},
			fileKey("foo", "public/favicon.ico/details"): {
				RelativePath: "public/favicon.ico/details",
				BlobPath:     "fallback-details",
			},
			fileKey("foo", "media/favicon.ico"): {
				RelativePath: "media/favicon.ico",
				BlobPath:     "media-favicon",
			},
		},
		sites: []domain.PublishedSite{{Site: "foo", SiteSHA: "foo-sha", CurrentVersion: 2}},
		uploadSettings: map[string]map[string]string{
			"foo-sha:2": {appsettings.SettingRoutes: `[{"path":"/","kind":"static","root":"public"},{"path":"/favicon.ico","kind":"static","file":"media/favicon.ico"}]`},
		},
	}
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

	tests := map[string]struct {
		path string
		body string
	}{
		"exact file route uses static file target": {
			path: "/favicon.ico",
			body: "media favicon",
		},
		"child path falls back to root route": {
			path: "/favicon.ico/details",
			body: "fallback details",
		},
	}

	for name, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Host = "foo.example.com"
		rec := httptest.NewRecorder()
		srv.Public.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", name, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != tc.body {
			t.Fatalf("%s: body = %q, want %q", name, rec.Body.String(), tc.body)
		}
	}
}

func TestWwwHostServesSiteFromSecondLabel(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "site-index", "site index")

	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
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
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

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
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

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

	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
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
	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
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

func TestPublicRuntimeStarlarkRouteExecutesBehindPolicyGate(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "app.star", `
def handle(req):
    method, path, query, headers, body = req
    return (
        202,
        {"content-type": "text/plain", "x-runtime": "starlark"},
        "%s %s %s %s" % (method, path, query, body),
    )
`)
	writeTestBlob(t, root, "index", "static index")
	db := &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {RelativePath: "index.html", BlobPath: "index"},
		},
		sites: []domain.PublishedSite{{Site: "foo", SiteSHA: "foo-sha", CurrentVersion: 7}},
		runtimeRoutes: map[string][]appruntime.RouteMetadata{
			"foo-sha:7": {{
				Site:                 "foo",
				SiteSHA:              "foo-sha",
				Version:              7,
				RoutePath:            "/api",
				RouteKind:            appruntime.RouteHTTP,
				RuntimeKind:          appruntime.RuntimeStarlark,
				BundleObjectKey:      "app.star",
				Methods:              []string{http.MethodPost},
				RequiredCapabilities: []string{"runtime.http"},
			}},
		},
		policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeHTTPFeature,
			Mode:      "allow",
		}},
	}
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/api/echo?x=1", strings.NewReader("hello"))
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if rec.Header().Get("X-Runtime") != "starlark" {
		t.Fatalf("x-runtime = %q, want starlark", rec.Header().Get("X-Runtime"))
	}
	if rec.Body.String() != `POST /echo x=1 b"hello"` {
		t.Fatalf("body = %q, want starlark response", rec.Body.String())
	}

	staticReq := httptest.NewRequest(http.MethodGet, "/", nil)
	staticReq.Host = "foo.example.com"
	staticRec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(staticRec, staticReq)
	if staticRec.Code != http.StatusOK || staticRec.Body.String() != "static index" {
		t.Fatalf("static response = %d %q, want unaffected static index", staticRec.Code, staticRec.Body.String())
	}
}
