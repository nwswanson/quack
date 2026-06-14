package server

import (
	"net/http"
	"strings"
)

func authorized(r *http.Request, token string, allowUnauthenticated bool) bool {
	if token == "" {
		return allowUnauthenticated
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return strings.TrimPrefix(auth, prefix) == token
}
