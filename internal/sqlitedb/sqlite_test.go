package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"quack/internal/server"
)

func TestFinishUploadPersistsMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quack.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	upload, err := db.BeginUpload(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []server.UploadFileRecord{
		{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        12,
		},
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	var site string
	var version int64
	var state string
	if err := raw.QueryRowContext(ctx, `
		SELECT s.site, s.current_version, u.state
		FROM sites s
		JOIN uploads u ON u.site_sha = s.site_sha AND u.version = s.current_version
		WHERE s.site_sha = ?
	`, upload.SiteSHA).Scan(&site, &version, &state); err != nil {
		t.Fatal(err)
	}
	if site != upload.Site || version != upload.Version {
		t.Fatalf("site row = (%q, %d), want (%q, %d)", site, version, upload.Site, upload.Version)
	}
	if state != string(server.UploadStateFinished) {
		t.Fatalf("upload state = %q, want %q", state, server.UploadStateFinished)
	}

	var relativePath string
	var blobPath string
	if err := raw.QueryRowContext(ctx, `
		SELECT uf.relative_path, uf.blob_path
		FROM upload_files uf
		JOIN uploads u ON u.id = uf.upload_id
		WHERE u.site_sha = ? AND u.version = ?
	`, upload.SiteSHA, upload.Version).Scan(&relativePath, &blobPath); err != nil {
		t.Fatal(err)
	}
	if relativePath != upload.Files[0].RelativePath || blobPath != upload.Files[0].BlobPath {
		t.Fatalf("file row = (%q, %q), want (%q, %q)", relativePath, blobPath, upload.Files[0].RelativePath, upload.Files[0].BlobPath)
	}
}

func TestBeginUploadIncrementsAndRetainsUploads(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	upload, err := db.BeginUpload(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 1 {
		t.Fatalf("initial version = %d, want 1", upload.Version)
	}

	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	upload, err = db.BeginUpload(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 2 {
		t.Fatalf("second version = %d, want 2", upload.Version)
	}

	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	upload, err = db.BeginUpload(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 3 {
		t.Fatalf("third version = %d, want 3", upload.Version)
	}

	var count int
	if err := db.readDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM uploads WHERE site_sha = ?
	`, "site-sha").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("upload rows = %d, want 3", count)
	}
}

func TestConcurrentBeginUploadAllocatesUniqueVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const uploads = 20
	versions := make(chan int64, uploads)
	errs := make(chan error, uploads)

	var wg sync.WaitGroup
	for i := 0; i < uploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			upload, err := db.BeginUpload(ctx, "example.com", "site-sha")
			if err != nil {
				errs <- err
				return
			}
			versions <- upload.Version
		}()
	}
	wg.Wait()
	close(versions)
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}

	seen := make(map[int64]bool)
	for version := range versions {
		seen[version] = true
	}
	for version := int64(1); version <= uploads; version++ {
		if !seen[version] {
			t.Fatalf("missing allocated version %d; got %#v", version, seen)
		}
	}
}

func TestFindCurrentFileUsesPublishedCurrentVersion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v1, err := db.BeginUpload(ctx, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	v1.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}
	if err := db.FinishUpload(ctx, v1); err != nil {
		t.Fatal(err)
	}

	v2, err := db.BeginUpload(ctx, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 {
		t.Fatalf("second version = %d, want 2", v2.Version)
	}

	file, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected current file")
	}
	if file.BlobPath != "blobs/site:foo-sha/1/file:v1" {
		t.Fatalf("blob path before publish = %q, want v1 blob", file.BlobPath)
	}

	v2.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/2/file:v2", FileSHA: "v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, v2); err != nil {
		t.Fatal(err)
	}

	file, ok, err = db.FindCurrentFile(ctx, "FOO", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected current file after publish")
	}
	if file.BlobPath != "blobs/site:foo-sha/2/file:v2" {
		t.Fatalf("blob path after publish = %q, want v2 blob", file.BlobPath)
	}
}

func TestConcurrentUploadsForDifferentSitesServeIndependently(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type siteUpload struct {
		site    string
		siteSHA string
		blob    string
	}
	initial := []siteUpload{
		{site: "site-a", siteSHA: "site-a-sha", blob: "blobs/site:site-a-sha/1/file:a-v1"},
		{site: "site-b", siteSHA: "site-b-sha", blob: "blobs/site:site-b-sha/1/file:b-v1"},
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(initial))
	for _, item := range initial {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			upload, err := db.BeginUpload(ctx, item.site, item.siteSHA)
			if err != nil {
				errs <- err
				return
			}
			if upload.Version != 1 {
				errs <- fmt.Errorf("%s version = %d, want 1", item.site, upload.Version)
				return
			}
			upload.Files = []server.UploadFileRecord{
				{RelativePath: "index.html", BlobPath: item.blob, FileSHA: item.blob, Bytes: 1},
			}
			if err := db.FinishUpload(ctx, upload); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/1/file:b-v1")

	a2, err := db.BeginUpload(ctx, "site-a", "site-a-sha")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := db.BeginUpload(ctx, "site-b", "site-b-sha")
	if err != nil {
		t.Fatal(err)
	}
	if a2.Version != 2 || b2.Version != 2 {
		t.Fatalf("second versions = (%d, %d), want (2, 2)", a2.Version, b2.Version)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/1/file:b-v1")

	b2.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:site-b-sha/2/file:b-v2", FileSHA: "b-v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, b2); err != nil {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/2/file:b-v2")

	a2.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:site-a-sha/2/file:a-v2", FileSHA: "a-v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, a2); err != nil {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/2/file:a-v2")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/2/file:b-v2")
}

func assertCurrentBlob(t *testing.T, ctx context.Context, db *Database, site string, want string) {
	t.Helper()
	file, ok, err := db.FindCurrentFile(ctx, site, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("site %s has no current index.html", site)
	}
	if file.BlobPath != want {
		t.Fatalf("site %s blob = %q, want %q", site, file.BlobPath, want)
	}
}

func TestFindCurrentFileIgnoresUploadingAndErrorVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	upload, err := db.BeginUpload(ctx, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}

	_, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("uploading version should not serve")
	}

	if err := db.FailUpload(ctx, upload, "test failure"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("error version should not serve")
	}
}

func TestDeleteSiteRemovesMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	upload, err := db.BeginUpload(ctx, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []server.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.DeleteSite(ctx, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("deleted = false, want true")
	}

	_, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("current file still exists after delete")
	}

	var siteCount int
	if err := db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE site_sha = ?`, "foo-sha").Scan(&siteCount); err != nil {
		t.Fatal(err)
	}
	if siteCount != 0 {
		t.Fatalf("site count = %d, want 0", siteCount)
	}
}
