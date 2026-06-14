package server

import (
	"net/http"
	"strings"
)

func authorized(r *http.Request, token string, allowUnauthenticated bool) bool {
	requestToken, ok := bearerToken(r)
	if token != "" && ok && requestToken == token {
		return true
	}
	return token == "" && allowUnauthenticated
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func (h *handler) authorizedAPIUser(r *http.Request) (AdminUser, bool, error) {
	requestToken, hasBearerToken := bearerToken(r)
	if h.token != "" && hasBearerToken && requestToken == h.token {
		return AdminUser{}, true, nil
	}
	if hasBearerToken {
		user, ok, err := h.db.FindUserByToken(r.Context(), requestToken)
		if err != nil || ok {
			return user, ok, err
		}
	}
	if h.token == "" && h.allowUnauthenticated {
		return AdminUser{}, true, nil
	}
	if authorized(r, h.token, h.allowUnauthenticated) {
		return AdminUser{}, true, nil
	}
	return AdminUser{}, false, nil
}

func (h *handler) authorizedAPI(r *http.Request) (bool, error) {
	_, ok, err := h.authorizedAPIUser(r)
	return ok, err
}
