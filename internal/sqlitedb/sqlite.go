package sqlitedb

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"quack/internal/server"
)

const (
	adminUsername   = "admin"
	adminPermission = "admin:*"
	pbkdf2Iters     = 210000
	hashBytes       = 32
	saltBytes       = 16
	sessionBytes    = 32
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
			publisher_user_id INTEGER,
			files INTEGER NOT NULL,
			bytes INTEGER NOT NULL,
			state TEXT NOT NULL DEFAULT 'finished' CHECK (state IN ('uploading', 'finished', 'error')),
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at TEXT,
			FOREIGN KEY(publisher_user_id) REFERENCES users(id) ON DELETE SET NULL,
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
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			admin_priv TEXT NOT NULL DEFAULT '',
			token_hash TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_sites (
			user_id INTEGER NOT NULL,
			site_sha TEXT NOT NULL,
			uploaded_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(user_id, site_sha),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY(site_sha) REFERENCES sites(site_sha) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS user_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS server_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
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
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE uploads ADD COLUMN publisher_user_id INTEGER`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite upload publisher: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `UPDATE sites SET next_version = current_version + 1 WHERE next_version <= current_version`); err != nil {
		return fmt.Errorf("repair sqlite version counter: %w", err)
	}
	return nil
}

func (d *Database) FindUserByToken(ctx context.Context, token string) (server.AdminUser, bool, error) {
	if token == "" {
		return server.AdminUser{}, false, nil
	}
	var user server.AdminUser
	err := d.readDB.QueryRowContext(ctx, `
		SELECT id, username, admin_priv
		FROM users
		WHERE token_hash = ?
	`, hashToken(token)).Scan(&user.ID, &user.Username, &user.AdminPriv)
	if err == sql.ErrNoRows {
		return server.AdminUser{}, false, nil
	}
	if err != nil {
		return server.AdminUser{}, false, fmt.Errorf("find user by token: %w", err)
	}
	return user, true, nil
}

func (d *Database) CreateUser(ctx context.Context, username string, adminPriv string) (server.CreatedUser, error) {
	username = strings.TrimSpace(username)
	adminPriv = strings.TrimSpace(adminPriv)
	if username == "" {
		return server.CreatedUser{}, fmt.Errorf("username is required")
	}
	if adminPriv == "" {
		adminPriv = "user"
	}

	password, err := randomSecret(24)
	if err != nil {
		return server.CreatedUser{}, fmt.Errorf("generate user password: %w", err)
	}
	token, err := randomSecret(32)
	if err != nil {
		return server.CreatedUser{}, fmt.Errorf("generate user token: %w", err)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return server.CreatedUser{}, fmt.Errorf("hash user password: %w", err)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	result, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, admin_priv, token_hash)
		VALUES (?, ?, ?, ?)
	`, username, passwordHash, adminPriv, hashToken(token))
	if err != nil {
		return server.CreatedUser{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return server.CreatedUser{}, fmt.Errorf("created user id: %w", err)
	}
	return server.CreatedUser{
		User: server.AdminUser{
			ID:        id,
			Username:  username,
			AdminPriv: adminPriv,
		},
		Password: password,
		Token:    token,
	}, nil
}

func (d *Database) ListUserSites(ctx context.Context, userID int64) ([]server.PublishedSite, error) {
	return d.listPublishedSites(ctx, userID, false)
}

func (d *Database) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]server.PublishedSite, error) {
	return d.listPublishedSites(ctx, userID, includeAll)
}

func (d *Database) listPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]server.PublishedSite, error) {
	if !includeAll && userID <= 0 {
		return nil, nil
	}

	query := `
		SELECT s.site,
			s.site_sha,
			COALESCE(pub.username, legacy.username, '') AS published_by,
			s.current_version,
			(SELECT COUNT(*) FROM uploads u2 WHERE u2.site_sha = s.site_sha AND u2.state = ?) AS version_count,
			COALESCE(cur.files, 0) AS file_count,
			COALESCE(cur.bytes, 0) AS byte_count,
			s.updated_at
		FROM sites s
		JOIN uploads cur
			ON cur.site_sha = s.site_sha
			AND cur.version = s.current_version
			AND cur.state = ?
		LEFT JOIN users pub ON pub.id = cur.publisher_user_id
		LEFT JOIN (
			SELECT us.site_sha, MIN(u.username) AS username
			FROM user_sites us
			JOIN users u ON u.id = us.user_id
			GROUP BY us.site_sha
		) legacy ON legacy.site_sha = s.site_sha
	`
	args := []any{string(server.UploadStateFinished), string(server.UploadStateFinished)}
	if !includeAll {
		query += `
		WHERE cur.publisher_user_id = ? OR EXISTS (
			SELECT 1 FROM user_sites us
			WHERE us.site_sha = s.site_sha AND us.user_id = ?
		)
		`
		args = append(args, userID, userID)
	}
	query += ` ORDER BY s.updated_at DESC, s.site ASC`

	rows, err := d.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list published sites: %w", err)
	}
	defer rows.Close()
	return scanPublishedSites(rows)
}

func scanPublishedSites(rows *sql.Rows) ([]server.PublishedSite, error) {
	var sites []server.PublishedSite
	for rows.Next() {
		var site server.PublishedSite
		if err := rows.Scan(&site.Site, &site.SiteSHA, &site.PublishedBy, &site.CurrentVersion, &site.VersionCount, &site.FileCount, &site.ByteCount, &site.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan published site: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published sites: %w", err)
	}
	return sites, nil
}

func (d *Database) LinkUserSite(ctx context.Context, userID int64, siteSHA string) error {
	if userID <= 0 || siteSHA == "" {
		return nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if _, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO user_sites (user_id, site_sha, uploaded_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, site_sha) DO UPDATE SET uploaded_at = CURRENT_TIMESTAMP
	`, userID, siteSHA); err != nil {
		return fmt.Errorf("link user site: %w", err)
	}
	return nil
}

func (d *Database) GetServerSettings(ctx context.Context) (server.ServerSettings, error) {
	settings := server.ServerSettings{
		MaxUploadBytes: server.DefaultMaxUploadBytes,
		MaxUploadFiles: server.DefaultMaxUploadFiles,
	}
	rows, err := d.readDB.QueryContext(ctx, `SELECT key, value FROM server_settings`)
	if err != nil {
		return server.ServerSettings{}, fmt.Errorf("get server settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return server.ServerSettings{}, fmt.Errorf("scan server setting: %w", err)
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return server.ServerSettings{}, fmt.Errorf("parse server setting %s: %w", key, err)
		}
		switch key {
		case "max_upload_bytes":
			settings.MaxUploadBytes = n
		case "max_upload_files":
			settings.MaxUploadFiles = n
		}
	}
	if err := rows.Err(); err != nil {
		return server.ServerSettings{}, fmt.Errorf("iterate server settings: %w", err)
	}
	return settings, nil
}

func (d *Database) SaveServerSettings(ctx context.Context, settings server.ServerSettings) error {
	if settings.MaxUploadBytes < 0 {
		return fmt.Errorf("max upload bytes must be >= 0")
	}
	if settings.MaxUploadFiles < 0 {
		return fmt.Errorf("max upload files must be >= 0")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save server settings transaction: %w", err)
	}
	defer tx.Rollback()

	for key, value := range map[string]int64{
		"max_upload_bytes": settings.MaxUploadBytes,
		"max_upload_files": settings.MaxUploadFiles,
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO server_settings (key, value, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
		`, key, strconv.FormatInt(value, 10)); err != nil {
			return fmt.Errorf("save server setting %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save server settings: %w", err)
	}
	return nil
}

func (d *Database) InitializeServerSettings(ctx context.Context, settings server.ServerSettings) error {
	if settings.MaxUploadBytes < 0 {
		return fmt.Errorf("max upload bytes must be >= 0")
	}
	if settings.MaxUploadFiles < 0 {
		return fmt.Errorf("max upload files must be >= 0")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	for key, value := range map[string]int64{
		"max_upload_bytes": settings.MaxUploadBytes,
		"max_upload_files": settings.MaxUploadFiles,
	} {
		if _, err := d.writeDB.ExecContext(ctx, `
			INSERT INTO server_settings (key, value, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO NOTHING
		`, key, strconv.FormatInt(value, 10)); err != nil {
			return fmt.Errorf("initialize server setting %s: %w", key, err)
		}
	}
	return nil
}

func (d *Database) AuthenticateAdmin(ctx context.Context, username string, password string) (server.AdminUser, bool, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return server.AdminUser{}, false, nil
	}

	var user server.AdminUser
	var passwordHash string
	err := d.readDB.QueryRowContext(ctx, `
		SELECT id, username, admin_priv, password_hash
		FROM users
		WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.AdminPriv, &passwordHash)
	if err == sql.ErrNoRows {
		return server.AdminUser{}, false, nil
	}
	if err != nil {
		return server.AdminUser{}, false, fmt.Errorf("lookup admin user: %w", err)
	}
	ok, err := verifyPassword(password, passwordHash)
	if err != nil {
		return server.AdminUser{}, false, fmt.Errorf("verify admin password: %w", err)
	}
	if !ok {
		return server.AdminUser{}, false, nil
	}
	return user, true, nil
}

func (d *Database) CreateAdminSession(ctx context.Context, userID int64) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("user id is required")
	}
	token, err := randomSecret(sessionBytes)
	if err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if _, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO user_sessions (user_id, token_hash, expires_at)
		VALUES (?, ?, datetime('now', '+7 days'))
	`, userID, hashToken(token)); err != nil {
		return "", fmt.Errorf("create admin session: %w", err)
	}
	return token, nil
}

func (d *Database) FindAdminSession(ctx context.Context, token string) (server.AdminUser, bool, error) {
	if token == "" {
		return server.AdminUser{}, false, nil
	}
	var user server.AdminUser
	err := d.readDB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.admin_priv
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?
			AND s.expires_at > CURRENT_TIMESTAMP
	`, hashToken(token)).Scan(&user.ID, &user.Username, &user.AdminPriv)
	if err == sql.ErrNoRows {
		return server.AdminUser{}, false, nil
	}
	if err != nil {
		return server.AdminUser{}, false, fmt.Errorf("find admin session: %w", err)
	}
	return user, true, nil
}

func (d *Database) DeleteAdminSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if _, err := d.writeDB.ExecContext(ctx, `
		DELETE FROM user_sessions
		WHERE token_hash = ?
	`, hashToken(token)); err != nil {
		return fmt.Errorf("delete admin session: %w", err)
	}
	return nil
}

type BootstrapAdminResult struct {
	Created  bool
	Username string
	Password string
	Token    string
}

func (d *Database) BootstrapAdmin(ctx context.Context) (BootstrapAdminResult, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("begin bootstrap admin transaction: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return BootstrapAdminResult{}, nil
	}

	password, err := randomSecret(24)
	if err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("generate admin password: %w", err)
	}
	token, err := randomSecret(32)
	if err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("generate admin token: %w", err)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("hash admin password: %w", err)
	}
	tokenHash := hashToken(token)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, admin_priv, token_hash)
		VALUES (?, ?, ?, ?)
	`, adminUsername, passwordHash, adminPermission, tokenHash); err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("create bootstrap admin user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("commit bootstrap admin transaction: %w", err)
	}

	return BootstrapAdminResult{
		Created:  true,
		Username: adminUsername,
		Password: password,
		Token:    token,
	}, nil
}

func (d *Database) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (server.UploadRecord, error) {
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

	if publisherUserID > 0 && !publisherIsAdmin {
		var siteExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE site_sha = ?`, siteSHA).Scan(&siteExists); err != nil {
			return server.UploadRecord{}, fmt.Errorf("check site ownership: %w", err)
		}
		if siteExists > 0 {
			var owned int
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM user_sites WHERE site_sha = ? AND user_id = ?
					UNION
					SELECT 1 FROM uploads WHERE site_sha = ? AND publisher_user_id = ?
				)
			`, siteSHA, publisherUserID, siteSHA, publisherUserID).Scan(&owned); err != nil {
				return server.UploadRecord{}, fmt.Errorf("check site owner: %w", err)
			}
			if owned == 0 {
				return server.UploadRecord{}, server.ErrSiteOwnership
			}
		}
	}

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
		INSERT INTO uploads (site_sha, site, version, publisher_user_id, files, bytes, state)
		VALUES (?, ?, ?, NULLIF(?, 0), 0, 0, ?)
	`, siteSHA, site, version, publisherUserID, string(server.UploadStateUploading)); err != nil {
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
		WHERE s.site = ?
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

func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iters, hashBytes)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"pbkdf2-sha256$%d$%s$%s",
		pbkdf2Iters,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(password string, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false, fmt.Errorf("unsupported password hash")
	}
	iters, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, fmt.Errorf("parse password hash iterations: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Errorf("decode password salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("decode password hash: %w", err)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iters, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
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
