package server

import (
	"net/http"
	"net/http/httptest"
	"quack/internal/domain"
	"quack/internal/protocol"
	"strings"
	"testing"
)

func TestAdminSurfaceServesAPIAndUI(t *testing.T) {
	srv := New("", "", "token", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	apiReq := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
	apiReq.Header.Set("Authorization", "Bearer token")
	apiRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("api status = %d, want %d; body=%s", apiRec.Code, http.StatusOK, apiRec.Body.String())
	}

	uiReq := httptest.NewRequest(http.MethodGet, "/", nil)
	uiRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(uiRec, uiReq)
	if uiRec.Code != http.StatusOK {
		t.Fatalf("ui status = %d, want %d; body=%s", uiRec.Code, http.StatusOK, uiRec.Body.String())
	}
	if !strings.Contains(uiRec.Body.String(), "Quack Admin") {
		t.Fatalf("ui body = %q, want admin page", uiRec.Body.String())
	}
}

func TestServerAddressDefaultsAndOverrides(t *testing.T) {
	defaults := New("", "", "", fakeStorage{}, &fakeDatabase{}, DefaultOptions())
	if defaults.Admin.Addr != ":8081" {
		t.Fatalf("default admin addr = %q, want :8081", defaults.Admin.Addr)
	}
	if defaults.Public.Addr != ":8080" {
		t.Fatalf("default public addr = %q, want :8080", defaults.Public.Addr)
	}

	overrides := New(":9000", ":9001", "", fakeStorage{}, &fakeDatabase{}, DefaultOptions())
	if overrides.Admin.Addr != ":9000" {
		t.Fatalf("override admin addr = %q, want :9000", overrides.Admin.Addr)
	}
	if overrides.Public.Addr != ":9001" {
		t.Fatalf("override public addr = %q, want :9001", overrides.Public.Addr)
	}
}

func TestPublicSurfaceDoesNotServeAPI(t *testing.T) {
	srv := New("", "", "token", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.LoginCheckPath, nil)
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestPublicSurfaceServesSiteRoot(t *testing.T) {
	root := t.TempDir()
	writeTestBlob(t, root, "index", "site index")
	srv := New("", "", "", fakeStorage{root: root}, &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {
				RelativePath: "index.html",
				BlobPath:     "index",
			},
		},
	}, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "site index" {
		t.Fatalf("body = %q, want site index", rec.Body.String())
	}
}
