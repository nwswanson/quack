package controlapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"quack/internal/domain"
)

type authUsers map[string]domain.AdminUser

func (u authUsers) FindUserByToken(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	user, ok := u[token]
	return user, ok, nil
}

func TestAuthorizedAPIUserRequiresKnownUserToken(t *testing.T) {
	h := New(Options{
		Users: authUsers{
			"secret": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	})

	req := httptest.NewRequest("POST", "/v1/uploads/archive", nil)
	req.Header.Set("Authorization", "Bearer secret")

	user, ok, err := h.authorizedAPIUser(req)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || user.ID != 7 {
		t.Fatalf("authorized user = (%+v, %v), want user 7 authorized", user, ok)
	}

	req.Header.Set("Authorization", "Bearer other")
	_, ok, err = h.authorizedAPIUser(req)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unknown bearer token authorized")
	}
}

func TestAuthorizedAPIUserAllowsExplicitUnauthenticatedMode(t *testing.T) {
	h := New(Options{AllowUnauthenticated: true, Users: authUsers{}})
	req := httptest.NewRequest("POST", "/v1/uploads/archive", nil)

	user, ok, err := h.authorizedAPIUser(req)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || user.ID != 0 {
		t.Fatalf("anonymous authorization = (%+v, %v), want zero user authorized", user, ok)
	}
}
