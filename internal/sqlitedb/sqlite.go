package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"quack/internal/server"
)

type Database struct {
	// SQLite permits many concurrent readers but only one writer. Keep those paths
	// separate so serving can use the read pool while all writes go through one
	// connection guarded by writeMu.
	readDB  *sql.DB
	writeDB *sql.DB
	writeMu sync.Mutex
}

func Open(ctx context.Context, path string) (*Database, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	writeDB, err := openSQLite(path, 1)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	readDB, err := openSQLite(path, 8)
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open sqlite read database: %w", err)
	}

	out := &Database{
		readDB:  readDB,
		writeDB: writeDB,
	}
	if err := out.init(ctx); err != nil {
		_ = writeDB.Close()
		_ = readDB.Close()
		return nil, err
	}
	return out, nil
}

func (d *Database) Close() error {
	readErr := d.readDB.Close()
	writeErr := d.writeDB.Close()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

func openSQLite(path string, maxOpenConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpenConns)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (d *Database) init(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS sites (
			site_sha TEXT PRIMARY KEY,
			site TEXT NOT NULL,
			current_version INTEGER NOT NULL,
			next_version INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			site_sha TEXT NOT NULL,
			site TEXT NOT NULL,
			version INTEGER NOT NULL,
			files INTEGER NOT NULL,
			bytes INTEGER NOT NULL,
			state TEXT NOT NULL DEFAULT 'finished' CHECK (state IN ('uploading', 'finished', 'error')),
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at TEXT,
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
		if _, err := d.writeDB.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite database: %w", err)
		}
	}
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE sites ADD COLUMN next_version INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite database: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE uploads ADD COLUMN state TEXT NOT NULL DEFAULT 'finished' CHECK (state IN ('uploading', 'finished', 'error'))`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite upload state: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE uploads ADD COLUMN error TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite upload error: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE uploads ADD COLUMN finished_at TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite upload finished_at: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `UPDATE sites SET next_version = current_version + 1 WHERE next_version <= current_version`); err != nil {
		return fmt.Errorf("repair sqlite version counter: %w", err)
	}
	return nil
}

func (d *Database) BeginUpload(ctx context.Context, site string, siteSHA string) (server.UploadRecord, error) {
	if site == "" {
		return server.UploadRecord{}, fmt.Errorf("site is required")
	}
	if siteSHA == "" {
		return server.UploadRecord{}, fmt.Errorf("site sha is required")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return server.UploadRecord{}, fmt.Errorf("begin upload transaction: %w", err)
	}
	defer tx.Rollback()

	var version int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO sites (site_sha, site, current_version, next_version, updated_at)
		VALUES (?, ?, 0, 2, CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			next_version = MAX(next_version, current_version + 1) + 1,
			updated_at = CURRENT_TIMESTAMP
		RETURNING next_version - 1
	`, siteSHA, site).Scan(&version); err != nil {
		return server.UploadRecord{}, fmt.Errorf("allocate upload version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO uploads (site_sha, site, version, files, bytes, state)
		VALUES (?, ?, ?, 0, 0, ?)
	`, siteSHA, site, version, string(server.UploadStateUploading)); err != nil {
		return server.UploadRecord{}, fmt.Errorf("create uploading record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return server.UploadRecord{}, fmt.Errorf("commit begin upload: %w", err)
	}

	return server.UploadRecord{
		Site:    site,
		SiteSHA: siteSHA,
		Version: version,
		State:   server.UploadStateUploading,
	}, nil
}

func (d *Database) FinishUpload(ctx context.Context, upload server.UploadRecord) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish upload transaction: %w", err)
	}
	defer tx.Rollback()

	totalBytes := int64(0)
	for _, file := range upload.Files {
		totalBytes += file.Bytes
	}

	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	uploadID, err := uploadID(ctx, tx, upload)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upload_files WHERE upload_id = ?`, uploadID); err != nil {
		return fmt.Errorf("clear upload files: %w", err)
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
	result, err := tx.ExecContext(ctx, `
		UPDATE uploads
		SET files = ?, bytes = ?, state = ?, error = '', finished_at = CURRENT_TIMESTAMP
		WHERE id = ? AND state = ?
	`, len(upload.Files), totalBytes, string(server.UploadStateFinished), uploadID, string(server.UploadStateUploading))
	if err != nil {
		return fmt.Errorf("mark upload finished: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finished upload rows affected: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("upload is not in uploading state: site=%s version=%d", upload.Site, upload.Version)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sites (site_sha, site, current_version, next_version, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			current_version = MAX(current_version, excluded.current_version),
			next_version = MAX(next_version, excluded.next_version),
			updated_at = CURRENT_TIMESTAMP
	`, upload.SiteSHA, upload.Site, upload.Version, upload.Version+1); err != nil {
		return fmt.Errorf("publish site version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish upload transaction: %w", err)
	}
	return nil
}

func (d *Database) FailUpload(ctx context.Context, upload server.UploadRecord, reason string) error {
	if upload.SiteSHA == "" || upload.Version <= 0 {
		return nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.writeDB.ExecContext(ctx, `
		UPDATE uploads
		SET state = ?, error = ?
		WHERE site_sha = ? AND version = ? AND state = ?
	`, string(server.UploadStateError), reason, upload.SiteSHA, upload.Version, string(server.UploadStateUploading))
	if err != nil {
		return fmt.Errorf("mark upload error: %w", err)
	}
	return nil
}

func (d *Database) FindCurrentFile(ctx context.Context, site string, relativePath string) (server.UploadFileRecord, bool, error) {
	var file server.UploadFileRecord
	err := d.readDB.QueryRowContext(ctx, `
		SELECT uf.relative_path, uf.blob_path, uf.file_sha, uf.bytes
		FROM sites s
		JOIN uploads u
			ON u.site_sha = s.site_sha
			AND u.version = s.current_version
			AND u.state = ?
		JOIN upload_files uf
			ON uf.upload_id = u.id
		WHERE LOWER(s.site) = LOWER(?)
			AND uf.relative_path = ?
	`, string(server.UploadStateFinished), site, relativePath).Scan(&file.RelativePath, &file.BlobPath, &file.FileSHA, &file.Bytes)
	if err == nil {
		return file, true, nil
	}
	if err == sql.ErrNoRows {
		return server.UploadFileRecord{}, false, nil
	}
	return server.UploadFileRecord{}, false, fmt.Errorf("find current file: %w", err)
}

func (d *Database) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete site transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return false, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id FROM uploads WHERE site_sha = ?`, siteSHA)
	if err != nil {
		return false, fmt.Errorf("list site uploads: %w", err)
	}
	var uploadIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan upload id: %w", err)
		}
		uploadIDs = append(uploadIDs, id)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close upload id rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate upload ids: %w", err)
	}

	for _, id := range uploadIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM upload_files WHERE upload_id = ?`, id); err != nil {
			return false, fmt.Errorf("delete upload files: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE site_sha = ?`, siteSHA); err != nil {
		return false, fmt.Errorf("delete uploads: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM sites WHERE site_sha = ? AND site = ?`, siteSHA, site)
	if err != nil {
		return false, fmt.Errorf("delete site: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("site delete rows affected: %w", err)
	}
	if affected == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete site transaction: %w", err)
	}
	return true, nil
}

func uploadID(ctx context.Context, tx *sql.Tx, upload server.UploadRecord) (int64, error) {
	var uploadID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM uploads
		WHERE site_sha = ? AND version = ?
	`, upload.SiteSHA, upload.Version).Scan(&uploadID); err != nil {
		return 0, fmt.Errorf("lookup upload id: %w", err)
	}
	return uploadID, nil
}
