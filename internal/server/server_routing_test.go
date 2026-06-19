package server

import (
	"net/http"
	"net/http/httptest"
	"quack/internal/protocol"
	"testing"
)

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
