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
