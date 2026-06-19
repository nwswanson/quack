package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"quack/internal/adminui"
	"quack/internal/domain"
	"strings"
	"testing"
)

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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sites: []domain.PublishedSite{
			{Site: "alpha", PublishedBy: "alice", CurrentVersion: 2, VersionCount: 2, FileCount: 3, ByteCount: 300, UpdatedAt: "2026-01-01T00:00:00Z"},
			{Site: "beta", PublishedBy: "bob", CurrentVersion: 1, VersionCount: 1, FileCount: 1, ByteCount: 100, UpdatedAt: "2026-01-02T00:00:00Z"},
		},
		sessions: map[string]domain.AdminUser{},
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
	if cookie.Name != adminui.SessionCookieName {
		t.Fatalf("cookie = %q, want %q", cookie.Name, adminui.SessionCookieName)
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://alice.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
	get.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
		settings:  domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
	}
	srv := New("", "token", fakeStorage{}, db, opts)

	update := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=536870912&max_upload_files=10000&log_level=debug"))
	update.Host = "quack.example.com"
	update.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	update.Header.Set("Origin", "https://quack.example.com")
	update.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
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
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{},
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
