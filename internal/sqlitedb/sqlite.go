package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"quack/internal/server"
)

type Database struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Database, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	out := &Database{db: db}
	if err := out.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return out, nil
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) init(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS sites (
			site_sha TEXT PRIMARY KEY,
			site TEXT NOT NULL,
			current_version INTEGER NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			site_sha TEXT NOT NULL,
			site TEXT NOT NULL,
			version INTEGER NOT NULL,
			files INTEGER NOT NULL,
			bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(site_sha, version)
		)`,
		`CREATE TABLE IF NOT EXISTS upload_files (
			upload_id INTEGER NOT NULL,
			relative_path TEXT NOT NULL,
			blob_path TEXT NOT NULL,
			file_sha TEXT NOT NULL,
			bytes INTEGER NOT NULL,
			PRIMARY KEY(upload_id, relative_path),
			FOREIGN KEY(upload_id) REFERENCES uploads(id) ON DELETE CASCADE
		)`,
	}

	for _, statement := range statements {
		if _, err := d.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite database: %w", err)
		}
	}
	return nil
}

func (d *Database) AllocateVersion(ctx context.Context, site string, siteSHA string) (int64, error) {
	if site == "" {
		return 0, fmt.Errorf("site is required")
	}
	if siteSHA == "" {
		return 0, fmt.Errorf("site sha is required")
	}

	var version int64
	if err := d.db.QueryRowContext(ctx, `
		INSERT INTO sites (site_sha, site, current_version, updated_at)
		VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			current_version = current_version + 1,
			updated_at = CURRENT_TIMESTAMP
		RETURNING current_version
	`, siteSHA, site).Scan(&version); err != nil {
		return 0, fmt.Errorf("allocate upload version: %w", err)
	}
	return version, nil
}

func (d *Database) SaveUpload(ctx context.Context, upload server.UploadRecord) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin upload transaction: %w", err)
	}
	defer tx.Rollback()

	totalBytes := int64(0)
	for _, file := range upload.Files {
		totalBytes += file.Bytes
	}

	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sites (site_sha, site, current_version, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			current_version = MAX(current_version, excluded.current_version),
			updated_at = CURRENT_TIMESTAMP
	`, upload.SiteSHA, upload.Site, upload.Version); err != nil {
		return fmt.Errorf("save site: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO uploads (site_sha, site, version, files, bytes)
		VALUES (?, ?, ?, ?, ?)
	`, upload.SiteSHA, upload.Site, upload.Version, len(upload.Files), totalBytes)
	if err != nil {
		return fmt.Errorf("save upload: %w", err)
	}

	uploadID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get upload id: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO upload_files (upload_id, relative_path, blob_path, file_sha, bytes)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare upload file insert: %w", err)
	}
	defer stmt.Close()

	for _, file := range upload.Files {
		if _, err := stmt.ExecContext(ctx, uploadID, file.RelativePath, file.BlobPath, file.FileSHA, file.Bytes); err != nil {
			return fmt.Errorf("save upload file %s: %w", file.RelativePath, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upload transaction: %w", err)
	}
	return nil
}
