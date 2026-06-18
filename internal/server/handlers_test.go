package server

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"quack/internal/protocol"
)

func TestSiteFromHost(t *testing.T) {
	tests := map[string]string{
		"foo.bar.domain.com": "foo",
		"domain.com":         "domain",
		"foo.domain.com":     "foo",
		"foo.example.com:80": "foo",
		"LOCALHOST:8080":     "localhost",
		"bad_site.example":   "",
		"v1.example.com":     "",
	}

	for input, want := range tests {
		got := siteFromHost(input)
		if got != want {
			t.Fatalf("siteFromHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalSiteName(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
		ok    bool
	}{
		"lowercases": {input: " Foo ", want: "foo", ok: true},
		"hyphen":     {input: "foo-bar", want: "foo-bar", ok: true},
		"dots":       {input: "foo.example", ok: false},
		"underscore": {input: "foo_bar", ok: false},
		"reserved":   {input: "serve", ok: false},
		"leading":    {input: "-foo", ok: false},
		"trailing":   {input: "foo-", ok: false},
	}

	for name, tc := range tests {
		got, err := canonicalSiteName(tc.input)
		if tc.ok && err != nil {
			t.Fatalf("%s: canonicalSiteName returned error: %v", name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: canonicalSiteName returned nil error", name)
		}
		if got != tc.want {
			t.Fatalf("%s: canonicalSiteName = %q, want %q", name, got, tc.want)
		}
	}
}

func TestRequestedRelativePath(t *testing.T) {
	tests := map[string]struct {
		path       string
		want       string
		wantsIndex bool
	}{
		"root":      {path: "/", want: "index.html", wantsIndex: true},
		"file":      {path: "/file.js", want: "file.js", wantsIndex: false},
		"nested":    {path: "/assets/app.js", want: "assets/app.js", wantsIndex: false},
		"directory": {path: "/docs/", want: "docs/index.html", wantsIndex: false},
		"sanitized": {path: "/My File!.html", want: "My_File_.html", wantsIndex: false},
		"traversal": {path: "/../file.js", want: "file.js", wantsIndex: false},
	}

	for name, tc := range tests {
		got, wantsIndex := requestedRelativePath(tc.path)
		if got != tc.want || wantsIndex != tc.wantsIndex {
			t.Fatalf("%s: requestedRelativePath(%q) = (%q, %v), want (%q, %v)", name, tc.path, got, wantsIndex, tc.want, tc.wantsIndex)
		}
	}
}

func TestSiteAndPathFromServePath(t *testing.T) {
	tests := map[string]struct {
		path     string
		site     string
		filePath string
		ok       bool
	}{
		"missing site": {path: "/serve/", ok: false},
		"site root":    {path: "/serve/foo", site: "foo", filePath: "/", ok: true},
		"site slash":   {path: "/serve/foo/", site: "foo", filePath: "/", ok: true},
		"site file":    {path: "/serve/foo/file.js", site: "foo", filePath: "/file.js", ok: true},
		"nested file":  {path: "/serve/foo/assets/app.js", site: "foo", filePath: "/assets/app.js", ok: true},
		"escaped site": {path: "/serve/foo%20bar/file.js", site: "foo bar", filePath: "/file.js", ok: true},
	}

	for name, tc := range tests {
		site, filePath, ok := siteAndPathFromServePath(tc.path)
		if site != tc.site || filePath != tc.filePath || ok != tc.ok {
			t.Fatalf("%s: siteAndPathFromServePath(%q) = (%q, %q, %v), want (%q, %q, %v)", name, tc.path, site, filePath, ok, tc.site, tc.filePath, tc.ok)
		}
	}
}

func TestNginxStyleStaticRouting(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "blog-index", "blog index")
	writeTestBlob(t, root, "file-js", "file js")

	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]UploadFileRecord{
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
		srv.Handler.ServeHTTP(rec, req)

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

func TestDefaultSiteFallbackForUnknownSite(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "default-index", "default index")
	db := &fakeDatabase{
		settings: ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, DefaultSite: "home", LogLevel: "warn"},
		files: map[string]UploadFileRecord{
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
	srv.Handler.ServeHTTP(rec, req)

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
		settings: ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, DefaultSite: "home", LogLevel: "warn"},
		files: map[string]UploadFileRecord{
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
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestExplicitServePathIsDisabled(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "blog-index", "blog index")

	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]UploadFileRecord{
			fileKey("foo", "blog/index.html"): {
				RelativePath: "blog/index.html",
				BlobPath:     "blog-index",
			},
		},
	}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/serve/foo/blog", nil)
	req.Host = "anything.example.com"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestLoginCheck(t *testing.T) {
	srv := New("", "token", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	t.Run("authorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if rec.Body.String() != "{\"ok\":true}\n" {
			t.Fatalf("body = %q, want ok", rec.Body.String())
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
		req.Header.Set("Authorization", "Bearer bad")
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		if rec.Body.String() != "{\"ok\":false,\"error\":\"unauthorized\"}\n" {
			t.Fatalf("body = %q, want unauthorized", rec.Body.String())
		}
	})
}

func TestLoginCheckAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("body = %q, want ok", rec.Body.String())
	}
}

func TestAdminPathsRequireAdminHost(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	srv := New("", "token", fakeStorage{}, &fakeDatabase{}, opts)

	tests := map[string]struct {
		method string
		path   string
	}{
		"login check": {method: http.MethodPost, path: protocol.LoginCheckPath},
		"upload":      {method: http.MethodPost, path: protocol.UploadArchivePath},
		"delete":      {method: http.MethodDelete, path: protocol.DeleteSitePathPrefix + "foo"},
		"future v1":   {method: http.MethodGet, path: "/v1/future"},
	}

	for name, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Host = "foo.example.com"
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want %d; body=%s", name, rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}
}

func TestAdminPathsAllowAdminHost(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	srv := New("", "token", fakeStorage{}, &fakeDatabase{}, opts)

	req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
	req.Host = "quack.example.com"
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestAdminHostRootShowsLoginPlaceholder(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	srv := New("", "token", fakeStorage{}, &fakeDatabase{}, opts)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "quack.example.com"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q, want html", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Quack Admin") || !strings.Contains(body, `action="/login"`) {
		t.Fatalf("body = %q, want login form", body)
	}
}

func TestAdminLoginAndLogout(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sites: []PublishedSite{
			{Site: "alpha", PublishedBy: "alice", CurrentVersion: 2, VersionCount: 2, FileCount: 3, ByteCount: 300, UpdatedAt: "2026-01-01T00:00:00Z"},
			{Site: "beta", PublishedBy: "bob", CurrentVersion: 1, VersionCount: 1, FileCount: 1, ByteCount: 100, UpdatedAt: "2026-01-02T00:00:00Z"},
		},
		sessions: map[string]AdminUser{},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	loginBody := strings.NewReader("username=admin&password=secret")
	loginReq := httptest.NewRequest(http.MethodPost, "/login", loginBody)
	loginReq.Host = "quack.example.com"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", "https://quack.example.com")
	loginReq.Header.Set("X-Forwarded-Proto", "https")
	loginRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusSeeOther, loginRec.Body.String())
	}
	cookie := loginRec.Result().Cookies()[0]
	if cookie.Name != adminSessionCookieName {
		t.Fatalf("cookie = %q, want %q", cookie.Name, adminSessionCookieName)
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
	if !cookie.Secure {
		t.Fatal("session cookie is not Secure behind https proxy")
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootReq.Host = "quack.example.com"
	rootReq.AddCookie(cookie)
	rootRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rootRec, rootReq)

	if rootRec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want %d; body=%s", rootRec.Code, http.StatusOK, rootRec.Body.String())
	}
	if !strings.Contains(rootRec.Body.String(), "Signed in as admin") {
		t.Fatalf("body = %q, want signed-in state", rootRec.Body.String())
	}
	if !strings.Contains(rootRec.Body.String(), "Published Sites") {
		t.Fatalf("body = %q, want published sites section", rootRec.Body.String())
	}
	if !strings.Contains(rootRec.Body.String(), "alpha") || !strings.Contains(rootRec.Body.String(), "alice") {
		t.Fatalf("body = %q, want alpha by alice", rootRec.Body.String())
	}
	if !strings.Contains(rootRec.Body.String(), "beta") || !strings.Contains(rootRec.Body.String(), "bob") {
		t.Fatalf("body = %q, want beta by bob", rootRec.Body.String())
	}
	if !strings.Contains(rootRec.Body.String(), "Server Settings") {
		t.Fatalf("body = %q, want server settings section", rootRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.Host = "quack.example.com"
	logoutReq.Header.Set("Origin", "https://quack.example.com")
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusSeeOther)
	}
	if _, ok := db.sessions[cookie.Value]; ok {
		t.Fatal("session still exists after logout")
	}
}

func TestAdminCreateUserShowsGeneratedCredentials(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, body)
	}
	for _, want := range []string{"alice", "generated-password", "generated-token"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
	}
}

func TestAdminPostRejectsSiblingOrigin(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://alice.example.com")
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid origin") {
		t.Fatalf("body = %q, want invalid origin", rec.Body.String())
	}
}

func TestAdminPostRejectsMissingOrigin(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestAdminSettingsUpdate(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/?message=Settings+saved." {
		t.Fatalf("location = %q, want settings message redirect", got)
	}
	if db.settings.MaxUploadBytes != 1024 || db.settings.MaxUploadFiles != 12 {
		t.Fatalf("settings = %#v, want updated values", db.settings)
	}

	get := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil)
	get.Host = "quack.example.com"
	get.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	page := httptest.NewRecorder()
	srv.Handler.ServeHTTP(page, get)
	if page.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", page.Code, http.StatusOK, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "Settings saved.") {
		t.Fatalf("body = %q, want settings message", page.Body.String())
	}
}

func TestAdminSettingsUpdateAppliesLogLevelImmediately(t *testing.T) {
	var logs bytes.Buffer
	if err := ConfigureLogger("warn", &logs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ConfigureLogger("warn", io.Discard)
	})

	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
		settings:  ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	update := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=536870912&max_upload_files=10000&log_level=debug"))
	update.Host = "quack.example.com"
	update.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	update.Header.Set("Origin", "https://quack.example.com")
	update.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	before404 := logs.Len()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Host = "foo.example.com"
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if logs.Len() == before404 || !strings.Contains(logs.String()[before404:], "status=404") {
		t.Fatalf("logs after 404 = %q, want request log", logs.String()[before404:])
	}
}

func TestAdminLoginRejectsInvalidPassword(t *testing.T) {
	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	db := &fakeDatabase{
		adminUser: AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]AdminUser{},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=bad"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("invalid login set cookies")
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Fatalf("body = %q, want invalid login message", rec.Body.String())
	}
}

func TestSiteHostRootStillServesSite(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "index", "site index")

	opts := DefaultOptions()
	opts.AdminHost = "https://quack.example.com"
	srv := New("", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]UploadFileRecord{
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "index",
			},
		},
	}, opts)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}

func TestUploadRejectsTooManyFiles(t *testing.T) {
	db := &fakeDatabase{settings: ServerSettings{
		MaxUploadBytes: DefaultMaxUploadBytes,
		MaxUploadFiles: 1,
	}}
	srv := New("", "", fakeStorage{}, db, Options{
		AllowUnauthenticated: true,
	})

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"one.txt": "one",
		"two.txt": "two",
	}))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestUploadRejectsTooManyBytes(t *testing.T) {
	db := &fakeDatabase{settings: ServerSettings{
		MaxUploadBytes: 128,
		MaxUploadFiles: DefaultMaxUploadFiles,
	}}
	srv := New("", "", fakeStorage{}, db, Options{
		AllowUnauthenticated: true,
	})

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"large.txt": "this content is intentionally long enough to push the tar request over the tiny test limit",
	}))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestUploadAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: ServerSettings{
			MaxUploadBytes: DefaultMaxUploadBytes,
			MaxUploadFiles: DefaultMaxUploadFiles,
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := uploadRequest(t, tarArchive(t, map[string]string{"index.html": "hello"}))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if db.lastPublisherUserID != 7 {
		t.Fatalf("publisher user id = %d, want 7", db.lastPublisherUserID)
	}
	if db.linkedUserID != 7 || db.linkedSiteSHA == "" {
		t.Fatalf("linked site = (%d, %q), want user 7 and site sha", db.linkedUserID, db.linkedSiteSHA)
	}
}

func TestUploadPrunesVersionsWhenRetentionOverflows(t *testing.T) {
	deletedVersions := []int64{}
	db := &fakeDatabase{
		prunedVersions: []int64{1, 2},
		settings: ServerSettings{
			MaxUploadBytes:      DefaultMaxUploadBytes,
			MaxUploadFiles:      DefaultMaxUploadFiles,
			MaxRetainedVersions: 3,
			LogLevel:            "warn",
		},
	}
	srv := New("", "", fakeStorage{deletedVersions: &deletedVersions}, db, Options{AllowUnauthenticated: true})

	req := uploadRequest(t, tarArchive(t, map[string]string{"index.html": "hello"}))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got, want := deletedVersions, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted versions = %#v, want %#v", got, want)
	}
}

func TestRevisionListReturnsWarningWithoutOlderRevisions(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		revisions: []RevisionRecord{{Version: 3, Current: true, Files: 1, Bytes: 5}},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteRevisionPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"warning":"no older revisions available"`) {
		t.Fatalf("body = %s, want warning", rec.Body.String())
	}
}

func TestRollbackReturnsWarningWithoutOlderRevisions(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		rollback: RollbackRecord{CurrentVersion: 3, Warning: "no older revisions available"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteRollbackPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"rolled_back":false`) || !strings.Contains(rec.Body.String(), `"warning":"no older revisions available"`) {
		t.Fatalf("body = %s, want no-op rollback warning", rec.Body.String())
	}
}

func TestUnpublishSite(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		unpublish: UnpublishRecord{Unpublished: true, LiveState: "unpublished"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteUnpublishPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unpublished":true`) || !strings.Contains(rec.Body.String(), `"live_state":"unpublished"`) {
		t.Fatalf("body = %s, want unpublished response", rec.Body.String())
	}
}

func TestPublishSite(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		publish: PublishRecord{Published: true, LiveState: "live"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SitePublishPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"published":true`) || !strings.Contains(rec.Body.String(), `"live_state":"live"`) {
		t.Fatalf("body = %s, want publish response", rec.Body.String())
	}
}

func TestListSitesReturnsSiteSummaries(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		sites: []PublishedSite{{
			Site: "foo", SiteSHA: "foo-sha", PublishedBy: "alice",
			CurrentVersion: 2, VersionCount: 3, FileCount: 4, ByteCount: 512, UpdatedAt: "2026-06-16T12:00:00Z",
		}},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.SitesPath, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"site":"foo"`, `"current_version":2`, `"runtime_status":"active"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

func TestListSitesAllRequiresAdmin(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.SitesPath+"?all=true", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestSetDefaultSiteRequiresAdminAndSaves(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"admin-token": {ID: 1, Username: "admin", AdminPriv: "admin:*"},
			"user-token":  {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	userReq := httptest.NewRequest(http.MethodPost, protocol.SettingsDefaultSitePath, strings.NewReader(`{"default_site":"home"}`))
	userReq.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, userReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, protocol.SettingsDefaultSitePath, strings.NewReader(`{"default_site":"home"}`))
	adminReq.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, adminReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if db.settings.DefaultSite != "home" {
		t.Fatalf("default site = %q, want home", db.settings.DefaultSite)
	}
}

func TestDeleteAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodDelete, protocol.DeleteSitePathPrefix+"foo", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func uploadRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, protocol.UploadArchivePath, bytes.NewReader(body))
	req.Header.Set("Content-Type", protocol.ContentTypeTar)
	req.Header.Set(protocol.HeaderSite, "foo")
	return req
}

func tarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeStorage struct {
	root            string
	deletedVersions *[]int64
}

func (fakeStorage) AcceptFile(ctx context.Context, file StoredFile) (StoredFileResult, error) {
	n, err := io.Copy(io.Discard, file.Body)
	if err != nil {
		return StoredFileResult{}, err
	}
	return StoredFileResult{
		BlobPath: "blobs/site:fake/1/file:fake",
		FileSHA:  "fake",
		Bytes:    n,
	}, nil
}

func (s fakeStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	if s.root != "" {
		return os.Open(filepath.Join(s.root, blobPath))
	}
	return nil, os.ErrNotExist
}

func (fakeStorage) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

func (s fakeStorage) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	if s.deletedVersions != nil {
		*s.deletedVersions = append(*s.deletedVersions, version)
	}
	return nil
}

type fakeDatabase struct {
	files                map[string]UploadFileRecord
	adminUser            AdminUser
	usersByToken         map[string]AdminUser
	sessions             map[string]AdminUser
	settings             ServerSettings
	policies             []PolicyRecord
	uploadSettings       map[string]map[string]string
	violations           map[string][]PolicyViolation
	prunedVersions       []int64
	revisions            []RevisionRecord
	rollback             RollbackRecord
	unpublish            UnpublishRecord
	publish              PublishRecord
	sites                []PublishedSite
	lastPublisherUserID  int64
	lastPublisherIsAdmin bool
	linkedUserID         int64
	linkedSiteSHA        string
}

func (db *fakeDatabase) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (UploadRecord, error) {
	db.lastPublisherUserID = publisherUserID
	db.lastPublisherIsAdmin = publisherIsAdmin
	return UploadRecord{
		Site:    site,
		SiteSHA: siteSHA,
		Version: 1,
		State:   UploadStateUploading,
	}, nil
}

func (fakeDatabase) FinishUpload(ctx context.Context, upload UploadRecord) error {
	return nil
}

func (fakeDatabase) FailUpload(ctx context.Context, upload UploadRecord, reason string) error {
	return nil
}

func (db fakeDatabase) FindCurrentFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, error) {
	file, fileOK, _, err := db.FindCurrentSiteFile(ctx, site, relativePath)
	return file, fileOK, err
}

func (db fakeDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	file, ok := db.files[fileKey(site, relativePath)]
	if ok {
		return file, true, true, nil
	}
	for key := range db.files {
		if strings.HasPrefix(key, site+"\x00") {
			return UploadFileRecord{}, false, true, nil
		}
	}
	return UploadFileRecord{}, false, false, nil
}

func (db fakeDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]UploadFileRecord, bool, error) {
	var out []UploadFileRecord
	for key, file := range db.files {
		if strings.HasPrefix(key, site+"\x00") {
			out = append(out, file)
		}
	}
	return out, len(out) > 0, nil
}

func (db *fakeDatabase) ListSiteRevisions(ctx context.Context, user AdminUser, site string, siteSHA string) ([]RevisionRecord, error) {
	return db.revisions, nil
}

func (db *fakeDatabase) RollbackSite(ctx context.Context, user AdminUser, site string, siteSHA string) (RollbackRecord, error) {
	return db.rollback, nil
}

func (db *fakeDatabase) UnpublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (UnpublishRecord, error) {
	return db.unpublish, nil
}

func (db *fakeDatabase) PublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (PublishRecord, error) {
	return db.publish, nil
}

func (fakeDatabase) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return true, nil
}

func (db *fakeDatabase) AuthenticateAdmin(ctx context.Context, username string, password string) (AdminUser, bool, error) {
	if db.adminUser.ID == 0 || username != db.adminUser.Username || password != "secret" {
		return AdminUser{}, false, nil
	}
	return db.adminUser, true, nil
}

func (db *fakeDatabase) FindUserByToken(ctx context.Context, token string) (AdminUser, bool, error) {
	if db.usersByToken != nil {
		user, ok := db.usersByToken[token]
		return user, ok, nil
	}
	if token == "user-token" && db.adminUser.ID > 0 {
		return db.adminUser, true, nil
	}
	return AdminUser{}, false, nil
}

func (db *fakeDatabase) CreateAdminSession(ctx context.Context, userID int64) (string, error) {
	if db.sessions == nil {
		db.sessions = map[string]AdminUser{}
	}
	token := "test-session-token"
	db.sessions[token] = db.adminUser
	return token, nil
}

func (db *fakeDatabase) FindAdminSession(ctx context.Context, token string) (AdminUser, bool, error) {
	user, ok := db.sessions[token]
	return user, ok, nil
}

func (db *fakeDatabase) DeleteAdminSession(ctx context.Context, token string) error {
	delete(db.sessions, token)
	return nil
}

func (db *fakeDatabase) CreateUser(ctx context.Context, username string, adminPriv string) (CreatedUser, error) {
	return CreatedUser{
		User:     AdminUser{ID: 99, Username: username, AdminPriv: adminPriv},
		Password: "generated-password",
		Token:    "generated-token",
	}, nil
}

func (db *fakeDatabase) ListUserSites(ctx context.Context, userID int64) ([]PublishedSite, error) {
	return db.sites, nil
}

func (db *fakeDatabase) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]PublishedSite, error) {
	if includeAll {
		return db.sites, nil
	}
	return db.sites, nil
}

func (db *fakeDatabase) ListPublishedSitesByUsername(ctx context.Context, username string) ([]PublishedSite, error) {
	return db.sites, nil
}

func (db *fakeDatabase) LinkUserSite(ctx context.Context, userID int64, siteSHA string) error {
	db.linkedUserID = userID
	db.linkedSiteSHA = siteSHA
	return nil
}

func (db *fakeDatabase) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	if db.settings.MaxUploadBytes == 0 && db.settings.MaxUploadFiles == 0 && db.settings.LogLevel == "" {
		return ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"}, nil
	}
	if db.settings.LogLevel == "" {
		db.settings.LogLevel = "warn"
	}
	return db.settings, nil
}

func (db *fakeDatabase) SaveServerSettings(ctx context.Context, settings ServerSettings) error {
	db.settings = settings
	return nil
}

func (db *fakeDatabase) PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error) {
	return db.prunedVersions, nil
}

func (db *fakeDatabase) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	var out []PolicyRecord
	for _, policy := range db.policies {
		for _, scope := range scopes {
			if policy.ScopeType == scope.Type && policy.ScopeID == scope.ID {
				out = append(out, policy)
			}
		}
	}
	return out, nil
}

func (db *fakeDatabase) SavePolicy(ctx context.Context, policy PolicyRecord) error {
	if policy.ScopeType == "" {
		policy.ScopeType = ScopeSystem
	}
	for i := range db.policies {
		if db.policies[i].ScopeType == policy.ScopeType && db.policies[i].ScopeID == policy.ScopeID && db.policies[i].Key == policy.Key {
			if policy.Mode == "inherit" {
				db.policies = append(db.policies[:i], db.policies[i+1:]...)
				return nil
			}
			db.policies[i] = policy
			return nil
		}
	}
	if policy.Mode != "inherit" {
		db.policies = append(db.policies, policy)
	}
	return nil
}

func (db *fakeDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	if db.uploadSettings == nil {
		return map[string]string{}, nil
	}
	settings := db.uploadSettings[siteSHA+":"+strconv.FormatInt(version, 10)]
	out := map[string]string{}
	for k, v := range settings {
		out[k] = v
	}
	return out, nil
}

func (db *fakeDatabase) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	if db.uploadSettings == nil {
		db.uploadSettings = map[string]map[string]string{}
	}
	key := siteSHA + ":" + strconv.FormatInt(version, 10)
	db.uploadSettings[key] = settings
	return nil
}

func (db *fakeDatabase) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	var out []CurrentSiteManifest
	for _, site := range db.sites {
		settings, _ := db.LoadUploadSettings(ctx, site.SiteSHA, site.CurrentVersion)
		out = append(out, CurrentSiteManifest{Site: site.Site, SiteSHA: site.SiteSHA, Version: site.CurrentVersion, Settings: settings})
	}
	return out, nil
}

func (db *fakeDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	if db.violations == nil {
		return nil, nil
	}
	return db.violations[siteSHA+":"+strconv.FormatInt(version, 10)], nil
}

func (db *fakeDatabase) SavePolicyViolation(ctx context.Context, violation PolicyViolation) error {
	if db.violations == nil {
		db.violations = map[string][]PolicyViolation{}
	}
	key := violation.SiteSHA + ":" + strconv.FormatInt(violation.UploadVersion, 10)
	db.violations[key] = []PolicyViolation{violation}
	return nil
}

func (db *fakeDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	if db.violations != nil {
		delete(db.violations, siteSHA+":"+strconv.FormatInt(version, 10))
	}
	return nil
}

func (fakeDatabase) Close() error {
	return nil
}

func fileKey(site string, relativePath string) string {
	return site + "\x00" + relativePath
}

func writeTestBlob(t *testing.T, root string, name string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
