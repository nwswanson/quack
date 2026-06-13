package server

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"quack/internal/protocol"
)

func TestSiteFromHost(t *testing.T) {
	tests := map[string]string{
		"foo.bar.domain.com": "foo",
		"domain.com":         "domain",
		"foo.domain.com":     "foo",
		"foo.example.com:80": "foo",
		"LOCALHOST:8080":     "localhost",
	}

	for input, want := range tests {
		got := siteFromHost(input)
		if got != want {
			t.Fatalf("siteFromHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRequestedRelativePath(t *testing.T) {
	tests := map[string]struct {
		path       string
		want       string
		wantsIndex bool
	}{
		"root":      {path: "/", want: "index.html", wantsIndex: true},
		"file":      {path: "/file.js", want: "file.js", wantsIndex: false},
		"nested":    {path: "/assets/app.js", want: "assets/app.js", wantsIndex: false},
		"directory": {path: "/docs/", want: "docs/index.html", wantsIndex: false},
		"sanitized": {path: "/My File!.html", want: "My_File_.html", wantsIndex: false},
		"traversal": {path: "/../file.js", want: "file.js", wantsIndex: false},
	}

	for name, tc := range tests {
		got, wantsIndex := requestedRelativePath(tc.path)
		if got != tc.want || wantsIndex != tc.wantsIndex {
			t.Fatalf("%s: requestedRelativePath(%q) = (%q, %v), want (%q, %v)", name, tc.path, got, wantsIndex, tc.want, tc.wantsIndex)
		}
	}
}

func TestSiteAndPathFromServePath(t *testing.T) {
	tests := map[string]struct {
		path     string
		site     string
		filePath string
		ok       bool
	}{
		"missing site": {path: "/serve/", ok: false},
		"site root":    {path: "/serve/foo", site: "foo", filePath: "/", ok: true},
		"site slash":   {path: "/serve/foo/", site: "foo", filePath: "/", ok: true},
		"site file":    {path: "/serve/foo/file.js", site: "foo", filePath: "/file.js", ok: true},
		"nested file":  {path: "/serve/foo/assets/app.js", site: "foo", filePath: "/assets/app.js", ok: true},
		"escaped site": {path: "/serve/foo%20bar/file.js", site: "foo bar", filePath: "/file.js", ok: true},
	}

	for name, tc := range tests {
		site, filePath, ok := siteAndPathFromServePath(tc.path)
		if site != tc.site || filePath != tc.filePath || ok != tc.ok {
			t.Fatalf("%s: siteAndPathFromServePath(%q) = (%q, %q, %v), want (%q, %q, %v)", name, tc.path, site, filePath, ok, tc.site, tc.filePath, tc.ok)
		}
	}
}

func TestUploadRejectsTooManyFiles(t *testing.T) {
	srv := New("", "", fakeStorage{}, &fakeDatabase{}, Options{
		MaxUploadBytes: 0,
		MaxUploadFiles: 1,
	})

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"one.txt": "one",
		"two.txt": "two",
	}))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestUploadRejectsTooManyBytes(t *testing.T) {
	srv := New("", "", fakeStorage{}, &fakeDatabase{}, Options{
		MaxUploadBytes: 128,
		MaxUploadFiles: 0,
	})

	req := uploadRequest(t, tarArchive(t, map[string]string{
		"large.txt": "this content is intentionally long enough to push the tar request over the tiny test limit",
	}))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
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

type fakeStorage struct{}

func (fakeStorage) AcceptFile(ctx context.Context, file StoredFile) (StoredFileResult, error) {
	n, err := io.Copy(io.Discard, file.Body)
	if err != nil {
		return StoredFileResult{}, err
	}
	return StoredFileResult{
		BlobPath: "blobs/site:fake/1/file:fake",
		FileSHA:  "fake",
		Bytes:    n,
	}, nil
}

func (fakeStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	return nil, os.ErrNotExist
}

func (fakeStorage) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

type fakeDatabase struct{}

func (fakeDatabase) AllocateVersion(ctx context.Context, site string, siteSHA string) (int64, error) {
	return 1, nil
}

func (fakeDatabase) SaveUpload(ctx context.Context, upload UploadRecord) error {
	return nil
}

func (fakeDatabase) FindCurrentFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, error) {
	return UploadFileRecord{}, false, nil
}

func (fakeDatabase) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return true, nil
}

func (fakeDatabase) Close() error {
	return nil
}
