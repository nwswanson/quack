package sqlitedb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"quack/internal/server"
)

func TestSaveUploadPersistsMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quack.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	upload := server.UploadRecord{
		Site:    "example.com",
		SiteSHA: "site-sha",
		Version: 1,
		Files: []server.UploadFileRecord{
			{
				RelativePath: "index.html",
				BlobPath:     "blobs/site:site-sha/1/file:file-sha",
				FileSHA:      "file-sha",
				Bytes:        12,
			},
		},
	}
	if err := db.SaveUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	var site string
	var version int64
	if err := raw.QueryRowContext(ctx, `
		SELECT site, current_version FROM sites WHERE site_sha = ?
	`, upload.SiteSHA).Scan(&site, &version); err != nil {
		t.Fatal(err)
	}
	if site != upload.Site || version != upload.Version {
		t.Fatalf("site row = (%q, %d), want (%q, %d)", site, version, upload.Site, upload.Version)
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

func TestAllocateVersionIncrementsAndRetainsUploads(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := db.AllocateVersion(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("initial version = %d, want 1", version)
	}

	if err := db.SaveUpload(ctx, server.UploadRecord{
		Site:    "example.com",
		SiteSHA: "site-sha",
		Version: version,
	}); err != nil {
		t.Fatal(err)
	}

	version, err = db.AllocateVersion(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("second version = %d, want 2", version)
	}

	if err := db.SaveUpload(ctx, server.UploadRecord{
		Site:    "example.com",
		SiteSHA: "site-sha",
		Version: version,
	}); err != nil {
		t.Fatal(err)
	}

	version, err = db.AllocateVersion(ctx, "example.com", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("third version = %d, want 3", version)
	}

	var count int
	if err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM uploads WHERE site_sha = ?
	`, "site-sha").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("upload rows = %d, want 2", count)
	}
}
