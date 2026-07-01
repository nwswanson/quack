package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quack/internal/adminui"
	"quack/internal/domain"
	appsecrets "quack/internal/secrets"
)

func TestAdmin2RequiresExistingAdminLogin(t *testing.T) {
	srv := New("", "", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/admin2", nil)
	req.Host = "quack.example.com"
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.HasPrefix(got, "/?error=") {
		t.Fatalf("location = %q, want existing login redirect", got)
	}
}

func TestAdmin2SecretStorePageUsesExistingSession(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	if err := appsecrets.NewService(db).Initialize(context.Background(), "opensesame", 42); err != nil {
		t.Fatalf("initialize secrets: %v", err)
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/admin2", nil)
	req.Host = "quack.example.com"
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Quack Admin 2", "Secret store", "Signed in as admin", `hx-post="/admin2/secrets/unlock"`, "Locked"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestAdmin2UnlockSecretStore(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	if err := appsecrets.NewService(db).Initialize(context.Background(), "opensesame", 42); err != nil {
		t.Fatalf("initialize secrets: %v", err)
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/admin2/secrets/unlock", strings.NewReader("passphrase=opensesame"))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Secret store unlocked.") || !strings.Contains(body, "Secret store is unlocked.") {
		t.Fatalf("body = %s, want unlocked state", body)
	}
}

func TestAdmin2UnlockRequiresPassphrase(t *testing.T) {
	db := &fakeDatabase{
		adminUser: domain.AdminUser{ID: 42, Username: "admin", AdminPriv: "admin:*"},
		sessions:  map[string]domain.AdminUser{"session": {ID: 42, Username: "admin", AdminPriv: "admin:*"}},
	}
	if err := appsecrets.NewService(db).Initialize(context.Background(), "opensesame", 42); err != nil {
		t.Fatalf("initialize secrets: %v", err)
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, "/admin2/secrets/unlock", strings.NewReader("passphrase="))
	req.Host = "quack.example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://quack.example.com")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: adminui.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "Passphrase is required.") || strings.Contains(body, "<!doctype html>") {
		t.Fatalf("body = %s, want htmx panel validation", body)
	}
}
