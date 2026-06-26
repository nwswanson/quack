package server

import (
	"archive/tar"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"quack/internal/domain"
	"quack/internal/protocol"
	"reflect"
	"strings"
	"testing"
)

func TestLoginCheck(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"token": {ID: 1, Username: "admin", AdminPriv: "admin:*"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	t.Run("authorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		srv.Admin.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if rec.Body.String() != "{\"ok\":true}\n" {
			t.Fatalf("body = %q, want ok", rec.Body.String())
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
		req.Header.Set("Authorization", "Bearer bad")
		rec := httptest.NewRecorder()
		srv.Admin.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		if rec.Body.String() != "{\"ok\":false,\"error\":\"unauthorized\"}\n" {
			t.Fatalf("body = %q, want unauthorized", rec.Body.String())
		}
	})
}

func TestLogsEndpointIncludesPublicSiteAccess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blob"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := &fakeDatabase{
		files: map[string]domain.UploadFileRecord{
			fileKey("foo", "index.html"): {RelativePath: "index.html", BlobPath: "blob", Bytes: 5},
		},
		usersByToken: map[string]domain.AdminUser{
			"admin-token": {ID: 1, Username: "admin", AdminPriv: "admin:*"},
		},
		settings: domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn", LogBufferCount: 20, AllowedHosts: []string{"*.example.com"}},
	}
	srv := New("", "", fakeStorage{root: root}, db, DefaultOptions())

	publicReq := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	publicReq.Host = "foo.example.com"
	publicRec := httptest.NewRecorder()
	srv.Public.Handler.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusOK {
		t.Fatalf("public status = %d, want %d; body=%s", publicRec.Code, http.StatusOK, publicRec.Body.String())
	}

	logReq := httptest.NewRequest(http.MethodGet, protocol.LogsPath+"?site=foo", nil)
	logReq.Header.Set("Authorization", "Bearer admin-token")
	logRec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(logRec, logReq)
	if logRec.Code != http.StatusOK {
		t.Fatalf("logs status = %d, want %d; body=%s", logRec.Code, http.StatusOK, logRec.Body.String())
	}
	body := logRec.Body.String()
	if !strings.Contains(body, `"source":"access"`) || !strings.Contains(body, `"site":"foo"`) || !strings.Contains(body, `"path":"/index.html"`) {
		t.Fatalf("logs body = %s, want public access event for foo", body)
	}
}

func TestLoginCheckAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.LoginCheckPath, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("body = %q, want ok", rec.Body.String())
	}
}

func TestUploadRejectsTooManyFiles(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: domain.ServerSettings{
			MaxUploadBytes: DefaultMaxUploadBytes,
			MaxUploadFiles: 1,
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"one.txt": "one",
		"two.txt": "two",
	}))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestUploadRejectsTooManyBytes(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: domain.ServerSettings{
			MaxUploadBytes: 128,
			MaxUploadFiles: DefaultMaxUploadFiles,
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"large.txt": "this content is intentionally long enough to push the tar request over the tiny test limit",
	}))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestUploadAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: domain.ServerSettings{
			MaxUploadBytes: DefaultMaxUploadBytes,
			MaxUploadFiles: DefaultMaxUploadFiles,
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := uploadRequest(t, tarArchive(t, map[string]string{"index.html": "hello"}))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if db.lastPublisherUserID != 7 {
		t.Fatalf("publisher user id = %d, want 7", db.lastPublisherUserID)
	}
	if db.linkedUserID != 7 || db.linkedSiteSHA == "" {
		t.Fatalf("linked site = (%d, %q), want user 7 and site sha", db.linkedUserID, db.linkedSiteSHA)
	}
}

func TestUploadRejectsUnauthenticatedModePublisher(t *testing.T) {
	db := &fakeDatabase{
		settings: domain.ServerSettings{
			MaxUploadBytes: DefaultMaxUploadBytes,
			MaxUploadFiles: DefaultMaxUploadFiles,
		},
	}
	srv := New("", "", fakeStorage{}, db, Options{AllowUnauthenticated: true})

	req := uploadRequest(t, tarArchive(t, map[string]string{"index.html": "hello"}))
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if db.lastPublisherUserID != 0 {
		t.Fatalf("publisher user id = %d, want 0", db.lastPublisherUserID)
	}
}

func TestUploadPrunesVersionsWhenRetentionOverflows(t *testing.T) {
	deletedVersions := []int64{}
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		prunedVersions: []int64{1, 2},
		settings: domain.ServerSettings{
			MaxUploadBytes:      DefaultMaxUploadBytes,
			MaxUploadFiles:      DefaultMaxUploadFiles,
			MaxRetainedVersions: 3,
			LogLevel:            "warn",
		},
	}
	srv := New("", "", fakeStorage{deletedVersions: &deletedVersions}, db, DefaultOptions())

	req := uploadRequest(t, tarArchive(t, map[string]string{"index.html": "hello"}))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got, want := deletedVersions, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted versions = %#v, want %#v", got, want)
	}
}

func TestRevisionListReturnsWarningWithoutOlderRevisions(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		revisions: []domain.RevisionRecord{{Version: 3, Current: true, Files: 1, Bytes: 5}},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteRevisionPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"warning":"no older revisions available"`) {
		t.Fatalf("body = %s, want warning", rec.Body.String())
	}
}

func TestRollbackReturnsWarningWithoutOlderRevisions(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		rollback: domain.RollbackRecord{CurrentVersion: 3, Warning: "no older revisions available"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteRollbackPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"rolled_back":false`) || !strings.Contains(rec.Body.String(), `"warning":"no older revisions available"`) {
		t.Fatalf("body = %s, want no-op rollback warning", rec.Body.String())
	}
}

func TestUnpublishSite(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		unpublish: domain.UnpublishRecord{Unpublished: true, LiveState: "unpublished"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SiteUnpublishPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unpublished":true`) || !strings.Contains(rec.Body.String(), `"live_state":"unpublished"`) {
		t.Fatalf("body = %s, want unpublished response", rec.Body.String())
	}
}

func TestPublishSite(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		publish: domain.PublishRecord{Published: true, LiveState: "live"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodPost, protocol.DeleteSitePathPrefix+"foo"+protocol.SitePublishPathSuffix, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"published":true`) || !strings.Contains(rec.Body.String(), `"live_state":"live"`) {
		t.Fatalf("body = %s, want publish response", rec.Body.String())
	}
}

func TestListSitesReturnsSiteSummaries(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		sites: []domain.PublishedSite{{
			Site: "foo", SiteSHA: "foo-sha", PublishedBy: "alice",
			CurrentVersion: 2, VersionCount: 3, FileCount: 4, ByteCount: 512, UpdatedAt: "2026-06-16T12:00:00Z",
		}},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.SitesPath, nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"site":"foo"`, `"current_version":2`, `"runtime_status":"active"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

func TestListSitesAllRequiresAdmin(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodGet, protocol.SitesPath+"?all=true", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestSetDefaultSiteRequiresAdminAndSaves(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"admin-token": {ID: 1, Username: "admin", AdminPriv: "admin:*"},
			"user-token":  {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		settings: domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	userReq := httptest.NewRequest(http.MethodPost, protocol.SettingsDefaultSitePath, strings.NewReader(`{"default_site":"home"}`))
	userReq.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, userReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, protocol.SettingsDefaultSitePath, strings.NewReader(`{"default_site":"home"}`))
	adminReq.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, adminReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if db.settings.DefaultSite != "home" {
		t.Fatalf("default site = %q, want home", db.settings.DefaultSite)
	}
}

func TestDeleteAcceptsUserTokenWithoutLegacyUploadToken(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodDelete, protocol.DeleteSitePathPrefix+"foo", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestDeleteSiteRejectsCrossUserOwnership(t *testing.T) {
	deletedSites := []string{}
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		deleteErr: domain.ErrSiteOwnership,
	}
	srv := New("", "", fakeStorage{deletedSites: &deletedSites}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodDelete, protocol.DeleteSitePathPrefix+"bob-site", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "site is owned by another user") {
		t.Fatalf("body = %s, want ownership error", rec.Body.String())
	}
	if len(deletedSites) != 0 {
		t.Fatalf("deleted sites = %#v, want none", deletedSites)
	}
}

func TestDeleteSiteReportsNonOwnershipErrors(t *testing.T) {
	db := &fakeDatabase{
		usersByToken: map[string]domain.AdminUser{
			"user-token": {ID: 7, Username: "alice", AdminPriv: "user"},
		},
		deleteErr: errors.New("database unavailable"),
	}
	srv := New("", "", fakeStorage{}, db, DefaultOptions())

	req := httptest.NewRequest(http.MethodDelete, protocol.DeleteSitePathPrefix+"foo", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.Admin.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func uploadRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, protocol.UploadArchivePath, bytes.NewReader(body))
	req.Header.Set("Content-Type", protocol.ContentTypeTar)
	req.Header.Set(protocol.HeaderSite, "foo")
	return req
}

func tarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
