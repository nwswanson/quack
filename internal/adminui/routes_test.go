package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginAdminRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")

	if !sameOriginAdminRequest(req) {
		t.Fatal("same origin request rejected")
	}

	req.Header.Set("Origin", "https://other.example.com")
	if sameOriginAdminRequest(req) {
		t.Fatal("cross origin request accepted")
	}
}

func TestAdminSessionCookieSecureBehindProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	cookie := adminSessionCookie(req, "session", 60)
	if cookie.Name != SessionCookieName {
		t.Fatalf("cookie = %q, want %q", cookie.Name, SessionCookieName)
	}
	if !cookie.Secure {
		t.Fatal("cookie should be secure behind https proxy")
	}
}
