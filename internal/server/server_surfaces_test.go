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

func TestAdminSurfaceServesPrometheusMetrics(t *testing.T) {
	db := &fakeDatabase{
		adminUsers: []domain.AdminUser{
			{ID: 1, Username: "admin", AdminPriv: "admin:*"},
			{ID: 2, Username: "alice", AdminPriv: "user"},
		},
		sites: []domain.PublishedSite{
			{Site: "alpha", SiteSHA: "sha-alpha", VersionCount: 2, FileCount: 3, ByteCount: 123, LiveState: "live"},
		},
	}
	srv := New("", "", "token", fakeStorage{}, db, DefaultOptions())

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want %d", rootRec.Code, http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content-type = %q, want prometheus text", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE quack_up gauge",
		"quack_users_total 2",
		"quack_sites_total 1",
		`quack_site_storage_bytes{live_state="live",site="alpha",site_sha="sha-alpha"} 246`,
		`quack_http_requests_total{method="GET",status="200",surface="admin"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
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

	tests := map[string]struct {
		method string
		path   string
		body   string
	}{
		"login check":     {method: http.MethodPost, path: protocol.LoginCheckPath},
		"upload archive":  {method: http.MethodPost, path: protocol.UploadArchivePath},
		"settings update": {method: http.MethodPost, path: protocol.SettingsDefaultSitePath, body: `{"default_site":"home"}`},
		"site management": {method: http.MethodDelete, path: protocol.DeleteSitePathPrefix + "foo"},
		"site revisions":  {method: http.MethodGet, path: protocol.DeleteSitePathPrefix + "foo" + protocol.SiteRevisionPathSuffix},
		"site rollback":   {method: http.MethodPost, path: protocol.DeleteSitePathPrefix + "foo" + protocol.SiteRollbackPathSuffix},
		"site unpublish":  {method: http.MethodPost, path: protocol.DeleteSitePathPrefix + "foo" + protocol.SiteUnpublishPathSuffix},
		"site publish":    {method: http.MethodPost, path: protocol.DeleteSitePathPrefix + "foo" + protocol.SitePublishPathSuffix},
		"site list":       {method: http.MethodGet, path: protocol.SitesPath},
	}

	for name, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		srv.Public.Handler.ServeHTTP(rec, req)

		if rec.Code >= 200 && rec.Code < 300 {
			t.Fatalf("%s: status = %d, want non-success from public surface; body=%s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminControlRoutesDoNotDependOnPublicHostRouting(t *testing.T) {
	srv := New("", "", "token", fakeStorage{}, &fakeDatabase{}, DefaultOptions())

	tests := map[string]struct {
		method string
		path   string
		host   string
		status int
	}{
		"login check on site host": {method: http.MethodPost, path: protocol.LoginCheckPath, host: "foo.example.com", status: http.StatusOK},
		"site list on site host":   {method: http.MethodGet, path: protocol.SitesPath, host: "foo.example.com", status: http.StatusOK},
	}

	for name, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Host = tc.host
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		srv.Admin.Handler.ServeHTTP(rec, req)

		if rec.Code != tc.status {
			t.Fatalf("%s: status = %d, want %d; body=%s", name, rec.Code, tc.status, rec.Body.String())
		}
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
