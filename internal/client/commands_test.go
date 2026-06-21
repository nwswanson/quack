package client

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quack/internal/protocol"
)

func TestUploadDirectoryReportsJSONServerError(t *testing.T) {
	dir := testUploadDir(t)
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		drainRequestBody(t, req)
		return response(req, http.StatusUnauthorized, `{"ok":false,"error":"unauthorized"}`), nil
	}))

	resp, err := UploadDirectory(context.Background(), "http://example.test", "bad-token", "foo", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || resp.Error != "unauthorized" {
		t.Fatalf("response error = %#v, want unauthorized", resp)
	}
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) {
		t.Fatalf("error type = %T, want *UploadError", err)
	}
	if uploadErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", uploadErr.StatusCode, http.StatusUnauthorized)
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("error = %q, want unauthorized detail", err.Error())
	}
}

func TestUploadDirectoryReportsPlainTextServerError(t *testing.T) {
	dir := testUploadDir(t)
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		drainRequestBody(t, req)
		return response(req, http.StatusRequestEntityTooLarge, "request body too large\n"), nil
	}))

	resp, err := UploadDirectory(context.Background(), "http://example.test", "token", "foo", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || !strings.Contains(resp.Error, "request body too large") {
		t.Fatalf("response error = %#v, want raw body detail", resp)
	}
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) {
		t.Fatalf("error type = %T, want *UploadError", err)
	}
	if uploadErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status code = %d, want %d", uploadErr.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func testUploadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUploadDirectorySuccessStillDecodesResponse(t *testing.T) {
	dir := testUploadDir(t)
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		drainRequestBody(t, req)
		if got := req.Header.Get(protocol.HeaderSite); got != "foo" {
			t.Fatalf("site header = %q, want foo", got)
		}
		return response(req, http.StatusOK, `{"ok":true,"site":"foo","version":1,"files":1,"bytes":5}`), nil
	}))

	resp, err := UploadDirectory(context.Background(), "http://example.test", "token", "foo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || resp.Site != "foo" || resp.Files != 1 {
		t.Fatalf("response = %#v, want successful upload response", resp)
	}
}

func TestUploadDirectoryAppliesSiteExclusionsBeforeUpload(t *testing.T) {
	dir := t.TempDir()
	writeUploadTestFile(t, dir, "site.yml", "exclude:\n  - \"*.swp\"\n  - \"node_modules\"\n")
	writeUploadTestFile(t, dir, "index.html", "hello")
	writeUploadTestFile(t, dir, "nested/file.swp", "swap")
	writeUploadTestFile(t, dir, "node_modules/pkg/index.js", "module")

	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		names := requestTarEntryNames(t, req)
		if got, want := strings.Join(names, ","), "index.html,nested,site.yml"; got != want {
			t.Fatalf("uploaded tar entries = %q, want %q", got, want)
		}
		return response(req, http.StatusOK, `{"ok":true,"site":"foo","version":1,"files":1,"bytes":5}`), nil
	}))

	if _, err := UploadDirectory(context.Background(), "http://example.test", "token", "foo", dir); err != nil {
		t.Fatal(err)
	}
}

func TestUploadDirectoryRejectsInvalidSiteExclusions(t *testing.T) {
	dir := t.TempDir()
	writeUploadTestFile(t, dir, "site.yml", "exclude:\n  - \"../secret\"\n")
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatal("upload request should not be sent")
		return nil, nil
	}))

	if _, err := UploadDirectory(context.Background(), "http://example.test", "token", "foo", dir); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteSiteSendsDeleteRequest(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", req.Method)
		}
		if req.URL.Path != "/v1/sites/foo" {
			t.Fatalf("path = %s, want /v1/sites/foo", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return response(req, http.StatusOK, `{"ok":true,"site":"foo","deleted":true}`), nil
	}))

	resp, err := DeleteSite(context.Background(), "http://example.test", "token", "foo")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || !resp.Deleted || resp.Site != "foo" {
		t.Fatalf("response = %#v, want deleted foo", resp)
	}
}

func TestDeleteSiteReportsServerError(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusUnauthorized, `{"ok":false,"error":"unauthorized"}`), nil
	}))

	resp, err := DeleteSite(context.Background(), "http://example.test", "bad", "foo")
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || resp.Error != "unauthorized" {
		t.Fatalf("response error = %#v, want unauthorized", resp)
	}
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) {
		t.Fatalf("error type = %T, want *UploadError", err)
	}
}

func TestUnpublishSiteSendsRequest(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/v1/sites/foo/unpublish" {
			t.Fatalf("path = %s, want /v1/sites/foo/unpublish", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return response(req, http.StatusOK, `{"ok":true,"site":"foo","unpublished":true,"live_state":"unpublished"}`), nil
	}))

	resp, err := UnpublishSite(context.Background(), "http://example.test", "token", "foo")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || !resp.Unpublished || resp.LiveState != "unpublished" {
		t.Fatalf("response = %#v, want unpublished foo", resp)
	}
}

func TestPublishSiteSendsRequest(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/v1/sites/foo/publish" {
			t.Fatalf("path = %s, want /v1/sites/foo/publish", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return response(req, http.StatusOK, `{"ok":true,"site":"foo","published":true,"live_state":"live"}`), nil
	}))

	resp, err := PublishSite(context.Background(), "http://example.test", "token", "foo")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || !resp.Published || resp.LiveState != "live" {
		t.Fatalf("response = %#v, want published foo", resp)
	}
}

func TestSetDefaultSiteSendsRequest(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != protocol.SettingsDefaultSitePath {
			t.Fatalf("path = %s, want %s", req.URL.Path, protocol.SettingsDefaultSitePath)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["default_site"] != "home" {
			t.Fatalf("body = %#v, want home default site", body)
		}
		return response(req, http.StatusOK, `{"ok":true,"default_site":"home"}`), nil
	}))

	resp, err := SetDefaultSite(context.Background(), "http://example.test", "token", "home")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || resp.DefaultSite != "home" {
		t.Fatalf("response = %#v, want home default site", resp)
	}
}

func TestListSitesSendsQuery(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != protocol.SitesPath {
			t.Fatalf("path = %s, want %s", req.URL.Path, protocol.SitesPath)
		}
		if got := req.URL.Query().Get("user"); got != "alice" {
			t.Fatalf("user query = %q, want alice", got)
		}
		if got := req.URL.Query().Get("all"); got != "true" {
			t.Fatalf("all query = %q, want true", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return response(req, http.StatusOK, `{"ok":true,"sites":[{"site":"foo","current_version":2}]}`), nil
	}))

	resp, err := ListSites(context.Background(), "http://example.test", "token", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK || len(resp.Sites) != 1 || resp.Sites[0].Site != "foo" {
		t.Fatalf("response = %#v, want foo site", resp)
	}
}

func TestCheckLoginSendsLoginCheckRequest(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != protocol.LoginCheckPath {
			t.Fatalf("path = %s, want %s", req.URL.Path, protocol.LoginCheckPath)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return response(req, http.StatusOK, `{"ok":true}`), nil
	}))

	resp, err := CheckLogin(context.Background(), "http://example.test", "token")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.OK {
		t.Fatalf("response = %#v, want ok", resp)
	}
}

func TestCheckLoginReportsUnauthorized(t *testing.T) {
	withHTTPClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusUnauthorized, `{"ok":false,"error":"unauthorized"}`), nil
	}))

	resp, err := CheckLogin(context.Background(), "http://example.test", "bad")
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || resp.Error != "unauthorized" {
		t.Fatalf("response error = %#v, want unauthorized", resp)
	}
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) {
		t.Fatalf("error type = %T, want *UploadError", err)
	}
	if uploadErr.Operation != "login check" {
		t.Fatalf("operation = %q, want login check", uploadErr.Operation)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func withHTTPClient(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	previous := httpClient
	httpClient = &http.Client{Transport: transport}
	t.Cleanup(func() {
		httpClient = previous
	})
}

func response(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func drainRequestBody(t *testing.T, req *http.Request) {
	t.Helper()
	if req.Body == nil {
		return
	}
	if _, err := io.Copy(io.Discard, req.Body); err != nil {
		t.Fatal(err)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeUploadTestFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requestTarEntryNames(t *testing.T, req *http.Request) []string {
	t.Helper()
	defer req.Body.Close()
	tr := tar.NewReader(req.Body)
	var names []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	return names
}
