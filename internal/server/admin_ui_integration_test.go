package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"quack/internal/adminui"
	"quack/internal/domain"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
	"strings"
	"testing"
)

func TestAdminRootShowsLoginPlaceholder(t *testing.T) {
	srv := New("", "", "token", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "quack.example.com"
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

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
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sites: []domain.PublishedSite{
			{Site: "alpha", SiteSHA: sites.HashName("alpha"), PublishedBy: "alice", CurrentVersion: 2, VersionCount: 2, FileCount: 3, ByteCount: 300, UpdatedAt: "2026-01-01T00:00:00Z", LiveState: "live"},
			{Site: "beta", SiteSHA: sites.HashName("beta"), PublishedBy: "bob", CurrentVersion: 1, VersionCount: 1, FileCount: 1, ByteCount: 100, UpdatedAt: "2026-01-02T00:00:00Z", LiveState: "unpublished"},
		},
		revisions: []domain.RevisionRecord{
			{Version: 2, Current: true},
			{Version: 1},
		},
		settings: domain.ServerSettings{DefaultSite: "alpha", LogLevel: "warn"},
		sessions: map[string]domain.AdminUser{},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	loginBody := strings.NewReader("username=admin&password=secret")
	loginReq := httptest.NewRequest(http.MethodPost, "/login", loginBody)
	loginReq.Host = "quack.example.com"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", "https://quack.example.com")
	loginReq.Header.Set("X-Forwarded-Proto", "https")
	loginRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(loginRec, loginReq)

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
	srv.Admin.Handler.ServeHTTP(rootRec, rootReq)

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
	for _, want := range []string{`class="site-card"`, "Default", `value="unpublish"`, `value="publish"`, "Roll back", `value="2" selected`, "v2 (current)", `value="1"`, `value="delete"`} {
		if !strings.Contains(rootRec.Body.String(), want) {
			t.Fatalf("body = %q, want site action/default marker %q", rootRec.Body.String(), want)
		}
	}
	if !strings.Contains(rootRec.Body.String(), `href="/users"`) || !strings.Contains(rootRec.Body.String(), `href="/settings"`) || !strings.Contains(rootRec.Body.String(), `href="/policy"`) {
		t.Fatalf("body = %q, want admin navigation", rootRec.Body.String())
	}

	settingsReq := httptest.NewRequest(http.MethodGet, "/settings", nil)
	settingsReq.Host = "quack.example.com"
	settingsReq.AddCookie(cookie)
	settingsRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(settingsRec, settingsReq)
	if settingsRec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want %d; body=%s", settingsRec.Code, http.StatusOK, settingsRec.Body.String())
	}
	if !strings.Contains(settingsRec.Body.String(), "Server Settings") || !strings.Contains(settingsRec.Body.String(), `aria-current="page">Server Settings`) {
		t.Fatalf("body = %q, want server settings page", settingsRec.Body.String())
	}

	policyReq := httptest.NewRequest(http.MethodGet, "/policy", nil)
	policyReq.Host = "quack.example.com"
	policyReq.AddCookie(cookie)
	policyRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(policyRec, policyReq)
	if policyRec.Code != http.StatusOK {
		t.Fatalf("policy status = %d, want %d; body=%s", policyRec.Code, http.StatusOK, policyRec.Body.String())
	}
	if !strings.Contains(policyRec.Body.String(), "Policies") || !strings.Contains(policyRec.Body.String(), "Dynamic HTTP routes policy") {
		t.Fatalf("body = %q, want policy page", policyRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.Host = "quack.example.com"
	logoutReq.Header.Set("Origin", "https://quack.example.com")
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusSeeOther)
	}
	if _, ok := db.sessions[cookie.Value]; ok {
		t.Fatal("session still exists after logout")
	}
}

func TestAdminSiteActions(t *testing.T) {
	deletedSites := []string{}
	db := &fakeDatabase{
		adminUser:       domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:        map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
		publish:         domain.PublishRecord{Published: true, LiveState: "live"},
		unpublish:       domain.UnpublishRecord{Unpublished: true, LiveState: "unpublished"},
		rollback:        domain.RollbackRecord{RolledBack: true, PreviousVersion: 3},
		rollbackVersion: 0,
	}
	srv := New("", "", "token", fakeStorage{deletedSites: &deletedSites}, db, DefaultOptions())

	postAction := func(form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/sites/action", strings.NewReader(form))
		req.Host = "quack.example.com"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://quack.example.com")
		req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
		rec := httptest.NewRecorder()
		srv.Admin.Handler.ServeHTTP(rec, req)
		return rec
	}

	rec := postAction("site=alpha&action=unpublish")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?message=Site+unpublished." {
		t.Fatalf("unpublish = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	rec = postAction("site=alpha&action=publish")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?message=Site+published." {
		t.Fatalf("publish = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	rec = postAction("site=alpha&action=rollback&version=2")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?message=Site+rolled+back+to+version+2." {
		t.Fatalf("rollback = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if db.rollbackVersion != 2 {
		t.Fatalf("rollback version = %d, want 2", db.rollbackVersion)
	}

	rec = postAction("site=alpha&action=delete")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?message=Site+deleted." {
		t.Fatalf("delete = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if len(deletedSites) != 1 || deletedSites[0] != sites.HashName("alpha") {
		t.Fatalf("deleted sites = %#v, want alpha hash", deletedSites)
	}
}

func TestAdminRollbackToCurrentVersionIsNoop(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
		sites:     []domain.PublishedSite{{Site: "alpha", CurrentVersion: 2}},
		rollback:  domain.RollbackRecord{RolledBack: true, PreviousVersion: 3},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/sites/action", strings.NewReader("site=alpha&action=rollback&version=2"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?message=Site+is+already+at+version+2." {
		t.Fatalf("rollback current = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if db.rollbackVersion != 2 {
		t.Fatalf("rollback version = %d, want 2", db.rollbackVersion)
	}
}

func TestAdminPolicyUpdateSavesDatabaseAndRuntimeHTTPPolicies(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	form := "database_policy_mode=deny&database_policy_reason=db+off&runtime_http_policy_mode=allow&runtime_http_policy_reason=runtime+ok"
	req := httptest.NewRequest(http.MethodPost, "/policy", strings.NewReader(form))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	got := map[string]domain.PolicyRecord{}
	for _, policy := range db.policies {
		got[policy.Key] = policy
	}
	if policy := got[appsettings.SettingDatabaseFeature]; policy.Mode != "deny" || policy.Reason != "db off" {
		t.Fatalf("database policy = %+v, want deny db off", policy)
	}
	if policy := got[appsettings.SettingRuntimeHTTPFeature]; policy.Mode != "allow" || policy.Reason != "runtime ok" {
		t.Fatalf("runtime HTTP policy = %+v, want allow runtime ok", policy)
	}
}

func TestAdminCreateUserShowsGeneratedCredentials(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, body)
	}
	for _, want := range []string{"alice", "generated-password", "generated-token"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
	}
	for _, want := range []string{"admin", "admin:*", "user"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want users table with %q", body, want)
		}
	}
}

func TestAdminPostRejectsSiblingOrigin(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("username=alice&admin_priv=user"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://alice.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid origin") {
		t.Fatalf("body = %q, want invalid origin", rec.Body.String())
	}
}

func TestAdminPostRejectsMissingOrigin(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestAdminSettingsUpdate(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=1024&max_upload_files=12"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/settings?message=Settings+saved." {
		t.Fatalf("location = %q, want settings message redirect", got)
	}
	if db.settings.MaxUploadBytes != 1024 || db.settings.MaxUploadFiles != 12 {
		t.Fatalf("settings = %#v, want updated values", db.settings)
	}

	get := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil)
	get.Host = "quack.example.com"
	get.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	page := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(page, get)
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
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
		settings:  domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	update := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("max_upload_bytes=536870912&max_upload_files=10000&log_level=debug"))
	update.Host = "quack.example.com"
	update.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	update.Header.Set("Origin", "https://quack.example.com")
	update.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	before404 := logs.Len()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Host = "foo.example.com"
	rec = httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if logs.Len() == before404 || !strings.Contains(logs.String()[before404:], "status=404") {
		t.Fatalf("logs after 404 = %q, want request log", logs.String()[before404:])
	}
}

func TestAdminLoginRejectsInvalidPassword(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=bad"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

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
