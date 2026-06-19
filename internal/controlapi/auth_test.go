package controlapi

import (
	"net/http/httptest"
	"testing"
)

func TestAuthorizedRequiresExplicitUnauthenticatedMode(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/uploads/archive", nil)

	if authorized(req, "", false) {
		t.Fatal("empty token authorized without allowUnauthenticated")
	}
	if !authorized(req, "", true) {
		t.Fatal("empty token did not authorize with allowUnauthenticated")
	}
}

func TestAuthorizedBearerToken(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/uploads/archive", nil)
	req.Header.Set("Authorization", "Bearer secret")

	if !authorized(req, "secret", false) {
		t.Fatal("valid bearer token did not authorize")
	}
	if authorized(req, "other", false) {
		t.Fatal("wrong bearer token authorized")
	}
}
