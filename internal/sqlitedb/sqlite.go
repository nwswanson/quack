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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"quack/internal/domain"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
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
			live_state TEXT NOT NULL DEFAULT 'live',
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
			source TEXT NOT NULL DEFAULT 'code_default',
			locked INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			scope_type TEXT NOT NULL CHECK (scope_type IN ('system', 'user', 'site', 'upload')),
			scope_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			source TEXT NOT NULL CHECK (source IN ('admin_ui', 'user_ui', 'site_yaml', 'code_default')),
			updated_by_user_id INTEGER,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (scope_type, scope_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS policies (
			scope_type TEXT NOT NULL CHECK (scope_type IN ('system', 'user', 'site')),
			scope_id TEXT NOT NULL,
			key TEXT NOT NULL,
			mode TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			updated_by_user_id INTEGER,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (scope_type, scope_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS upload_settings (
			site_sha TEXT NOT NULL,
			upload_version INTEGER NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'site_yaml',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (site_sha, upload_version, key)
		)`,
		`CREATE TABLE IF NOT EXISTS site_policy_violations (
			site_sha TEXT NOT NULL,
			upload_version INTEGER NOT NULL,
			key TEXT NOT NULL,
			requested_value TEXT NOT NULL,
			policy_value TEXT NOT NULL,
			severity TEXT NOT NULL CHECK (severity IN ('degraded', 'suspended')),
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			resolved_at TEXT,
			PRIMARY KEY (site_sha, upload_version, key)
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_routes (
			site_sha TEXT NOT NULL,
			upload_version INTEGER NOT NULL,
			route_path TEXT NOT NULL,
			route_kind TEXT NOT NULL CHECK (route_kind IN ('http', 'websocket')),
			runtime_kind TEXT NOT NULL DEFAULT 'disabled',
			entrypoint TEXT NOT NULL DEFAULT '',
			bundle_object_key TEXT NOT NULL DEFAULT '',
			methods_json TEXT NOT NULL DEFAULT '[]',
			required_capabilities_json TEXT NOT NULL DEFAULT '[]',
			resource_limits_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (site_sha, upload_version, route_path, route_kind),
			FOREIGN KEY(site_sha, upload_version) REFERENCES uploads(site_sha, version) ON DELETE CASCADE
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
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE sites ADD COLUMN live_state TEXT NOT NULL DEFAULT 'live'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite site live state: %w", err)
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
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE server_settings ADD COLUMN source TEXT NOT NULL DEFAULT 'code_default'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite server setting source: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `ALTER TABLE server_settings ADD COLUMN locked INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate sqlite server setting locked: %w", err)
	}
	if _, err := d.writeDB.ExecContext(ctx, `UPDATE sites SET next_version = current_version + 1 WHERE next_version <= current_version`); err != nil {
		return fmt.Errorf("repair sqlite version counter: %w", err)
	}
	return nil
}

func (d *Database) FindUserByToken(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	if token == "" {
		return domain.AdminUser{}, false, nil
	}
	var user domain.AdminUser
	err := d.readDB.QueryRowContext(ctx, `
		SELECT id, username, admin_priv
		FROM users
		WHERE token_hash = ?
	`, hashToken(token)).Scan(&user.ID, &user.Username, &user.AdminPriv)
	if err == sql.ErrNoRows {
		return domain.AdminUser{}, false, nil
	}
	if err != nil {
		return domain.AdminUser{}, false, fmt.Errorf("find user by token: %w", err)
	}
	return user, true, nil
}

func (d *Database) CreateUser(ctx context.Context, username string, adminPriv string) (domain.CreatedUser, error) {
	username = strings.TrimSpace(username)
	adminPriv = strings.TrimSpace(adminPriv)
	if username == "" {
		return domain.CreatedUser{}, fmt.Errorf("username is required")
	}
	if adminPriv == "" {
		adminPriv = "user"
	}

	password, err := randomSecret(24)
	if err != nil {
		return domain.CreatedUser{}, fmt.Errorf("generate user password: %w", err)
	}
	token, err := randomSecret(32)
	if err != nil {
		return domain.CreatedUser{}, fmt.Errorf("generate user token: %w", err)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return domain.CreatedUser{}, fmt.Errorf("hash user password: %w", err)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	result, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, admin_priv, token_hash)
		VALUES (?, ?, ?, ?)
	`, username, passwordHash, adminPriv, hashToken(token))
	if err != nil {
		return domain.CreatedUser{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.CreatedUser{}, fmt.Errorf("created user id: %w", err)
	}
	return domain.CreatedUser{
		User: domain.AdminUser{
			ID:        id,
			Username:  username,
			AdminPriv: adminPriv,
		},
		Password: password,
		Token:    token,
	}, nil
}

func (d *Database) ListUserSites(ctx context.Context, userID int64) ([]domain.PublishedSite, error) {
	return d.listPublishedSites(ctx, userID, false)
}

func (d *Database) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	return d.listPublishedSites(ctx, userID, includeAll)
}

func (d *Database) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, nil
	}
	var userID int64
	err := d.readDB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user for site list: %w", err)
	}
	return d.listPublishedSites(ctx, userID, false)
}

func (d *Database) listPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
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
			s.updated_at,
			s.live_state
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
	args := []any{string(domain.UploadStateFinished), string(domain.UploadStateFinished)}
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

func scanPublishedSites(rows *sql.Rows) ([]domain.PublishedSite, error) {
	var sites []domain.PublishedSite
	for rows.Next() {
		var site domain.PublishedSite
		if err := rows.Scan(&site.Site, &site.SiteSHA, &site.PublishedBy, &site.CurrentVersion, &site.VersionCount, &site.FileCount, &site.ByteCount, &site.UpdatedAt, &site.LiveState); err != nil {
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

func (d *Database) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	settings := domain.ServerSettings{
		MaxUploadBytes:      appsettings.DefaultMaxUploadBytes,
		MaxUploadFiles:      appsettings.DefaultMaxUploadFiles,
		MaxRetainedVersions: 0,
		DefaultSite:         "",
		LogLevel:            "warn",
		Locked:              map[string]bool{},
	}
	rows, err := d.readDB.QueryContext(ctx, `SELECT key, value, locked FROM server_settings`)
	if err != nil {
		return domain.ServerSettings{}, fmt.Errorf("get server settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		var locked int
		if err := rows.Scan(&key, &value, &locked); err != nil {
			return domain.ServerSettings{}, fmt.Errorf("scan server setting: %w", err)
		}
		if err := appsettings.Validate(key, value); err != nil {
			return domain.ServerSettings{}, err
		}
		if locked != 0 {
			settings.Locked[key] = true
		}
		switch key {
		case "max_upload_bytes":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return domain.ServerSettings{}, fmt.Errorf("parse server setting %s: %w", key, err)
			}
			settings.MaxUploadBytes = n
		case "max_upload_files":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return domain.ServerSettings{}, fmt.Errorf("parse server setting %s: %w", key, err)
			}
			settings.MaxUploadFiles = n
		case "max_retained_versions":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return domain.ServerSettings{}, fmt.Errorf("parse server setting %s: %w", key, err)
			}
			settings.MaxRetainedVersions = n
		case "default_site":
			settings.DefaultSite = value
		case "log_level":
			settings.LogLevel = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if err := rows.Err(); err != nil {
		return domain.ServerSettings{}, fmt.Errorf("iterate server settings: %w", err)
	}
	return settings, nil
}

func (d *Database) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	if settings.MaxUploadBytes < 0 {
		return fmt.Errorf("max upload bytes must be >= 0")
	}
	if settings.MaxUploadFiles < 0 {
		return fmt.Errorf("max upload files must be >= 0")
	}
	if settings.MaxRetainedVersions < 0 {
		return fmt.Errorf("max retained versions must be >= 0")
	}
	if settings.LogLevel == "" {
		settings.LogLevel = "warn"
	}
	if err := appsettings.Validate("log_level", settings.LogLevel); err != nil {
		return err
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save server settings transaction: %w", err)
	}
	defer tx.Rollback()

	values := map[string]string{
		"max_upload_bytes":      strconv.FormatInt(settings.MaxUploadBytes, 10),
		"max_upload_files":      strconv.FormatInt(settings.MaxUploadFiles, 10),
		"max_retained_versions": strconv.FormatInt(settings.MaxRetainedVersions, 10),
		"default_site":          strings.TrimSpace(settings.DefaultSite),
		"log_level":             settings.LogLevel,
	}
	for key, value := range values {
		var locked int
		if err := tx.QueryRowContext(ctx, `SELECT locked FROM server_settings WHERE key = ?`, key).Scan(&locked); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check locked server setting %s: %w", key, err)
		}
		if locked != 0 {
			return fmt.Errorf("%s is locked and cannot be edited", key)
		}
		if err := appsettings.Validate(key, value); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO server_settings (key, value, source, updated_at)
			VALUES (?, ?, 'admin_ui', CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, source = 'admin_ui', updated_at = CURRENT_TIMESTAMP
		`, key, value); err != nil {
			return fmt.Errorf("save server setting %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save server settings: %w", err)
	}
	return nil
}

func (d *Database) InitializeServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	if settings.MaxUploadBytes < 0 {
		return fmt.Errorf("max upload bytes must be >= 0")
	}
	if settings.MaxUploadFiles < 0 {
		return fmt.Errorf("max upload files must be >= 0")
	}
	if settings.MaxRetainedVersions < 0 {
		return fmt.Errorf("max retained versions must be >= 0")
	}
	if settings.LogLevel == "" {
		settings.LogLevel = "warn"
	}
	if err := appsettings.Validate("log_level", settings.LogLevel); err != nil {
		return err
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	for key, value := range map[string]string{
		"max_upload_bytes":      strconv.FormatInt(settings.MaxUploadBytes, 10),
		"max_upload_files":      strconv.FormatInt(settings.MaxUploadFiles, 10),
		"max_retained_versions": strconv.FormatInt(settings.MaxRetainedVersions, 10),
		"default_site":          strings.TrimSpace(settings.DefaultSite),
		"log_level":             settings.LogLevel,
	} {
		if err := appsettings.Validate(key, value); err != nil {
			return err
		}
		if _, err := d.writeDB.ExecContext(ctx, `
			INSERT INTO server_settings (key, value, source, updated_at)
			VALUES (?, ?, 'code_default', CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO NOTHING
		`, key, value); err != nil {
			return fmt.Errorf("initialize server setting %s: %w", key, err)
		}
	}
	return nil
}

func (d *Database) PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error) {
	if siteSHA == "" || maxRetainedVersions <= 0 {
		return nil, nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin prune site versions transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT version
		FROM uploads
		WHERE site_sha = ? AND state = ?
		ORDER BY version DESC
		LIMIT -1 OFFSET ?
	`, siteSHA, string(domain.UploadStateFinished), maxRetainedVersions)
	if err != nil {
		return nil, fmt.Errorf("list prunable site versions: %w", err)
	}
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan prunable site version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate prunable site versions: %w", err)
	}
	rows.Close()

	for _, version := range versions {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM upload_settings
			WHERE site_sha = ? AND upload_version = ?
		`, siteSHA, version); err != nil {
			return nil, fmt.Errorf("delete upload settings for pruned version %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM site_policy_violations
			WHERE site_sha = ? AND upload_version = ?
		`, siteSHA, version); err != nil {
			return nil, fmt.Errorf("delete policy violations for pruned version %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM runtime_routes
			WHERE site_sha = ? AND upload_version = ?
		`, siteSHA, version); err != nil {
			return nil, fmt.Errorf("delete runtime routes for pruned version %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM uploads
			WHERE site_sha = ? AND version = ? AND state = ?
		`, siteSHA, version, string(domain.UploadStateFinished)); err != nil {
			return nil, fmt.Errorf("delete pruned upload version %d: %w", version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune site versions: %w", err)
	}
	return versions, nil
}

func (d *Database) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	var out []domain.PolicyRecord
	for _, scope := range scopes {
		rows, err := d.readDB.QueryContext(ctx, `
			SELECT scope_type, scope_id, key, mode, value, reason, COALESCE(updated_by_user_id, 0)
			FROM policies
			WHERE scope_type = ? AND scope_id = ?
		`, string(scope.Type), scope.ID)
		if err != nil {
			return nil, fmt.Errorf("load policies: %w", err)
		}
		for rows.Next() {
			var p domain.PolicyRecord
			var scopeType string
			if err := rows.Scan(&scopeType, &p.ScopeID, &p.Key, &p.Mode, &p.Value, &p.Reason, &p.UpdatedByUserID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan policy: %w", err)
			}
			p.ScopeType = domain.ScopeType(scopeType)
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate policies: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

func (d *Database) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	if policy.ScopeType == "" {
		policy.ScopeType = domain.ScopeSystem
	}
	if policy.Mode == "" {
		policy.Mode = "inherit"
	}
	if policy.Key == "" {
		return fmt.Errorf("policy key is required")
	}
	switch policy.Mode {
	case "inherit", "allow", "deny", "force_on", "force_off", "cap", "allow_list", "force_value":
	default:
		return fmt.Errorf("unsupported policy mode: %s", policy.Mode)
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if policy.Mode == "inherit" {
		if _, err := d.writeDB.ExecContext(ctx, `DELETE FROM policies WHERE scope_type = ? AND scope_id = ? AND key = ?`, string(policy.ScopeType), policy.ScopeID, policy.Key); err != nil {
			return fmt.Errorf("delete inherited policy: %w", err)
		}
		return nil
	}
	if _, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO policies (scope_type, scope_id, key, mode, value, reason, updated_by_user_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, 0), CURRENT_TIMESTAMP)
		ON CONFLICT(scope_type, scope_id, key) DO UPDATE SET
			mode = excluded.mode,
			value = excluded.value,
			reason = excluded.reason,
			updated_by_user_id = excluded.updated_by_user_id,
			updated_at = CURRENT_TIMESTAMP
	`, string(policy.ScopeType), policy.ScopeID, policy.Key, policy.Mode, policy.Value, policy.Reason, policy.UpdatedByUserID); err != nil {
		return fmt.Errorf("save policy: %w", err)
	}
	return nil
}

func (d *Database) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	rows, err := d.readDB.QueryContext(ctx, `SELECT key, value FROM upload_settings WHERE site_sha = ? AND upload_version = ?`, siteSHA, version)
	if err != nil {
		return nil, fmt.Errorf("load upload settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan upload setting: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (d *Database) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	if siteSHA == "" || version <= 0 {
		return fmt.Errorf("upload identity is required")
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	for key, value := range settings {
		if err := appsettings.Validate(key, value); err != nil {
			return err
		}
		if _, err := d.writeDB.ExecContext(ctx, `
			INSERT INTO upload_settings (site_sha, upload_version, key, value, source)
			VALUES (?, ?, ?, ?, 'site_yaml')
			ON CONFLICT(site_sha, upload_version, key) DO UPDATE SET value = excluded.value, source = 'site_yaml'
		`, siteSHA, version, key, value); err != nil {
			return fmt.Errorf("save upload setting %s: %w", key, err)
		}
	}
	return nil
}

func (d *Database) SaveRuntimeRoutes(ctx context.Context, siteSHA string, version int64, routes []appruntime.RouteMetadata) error {
	if siteSHA == "" || version <= 0 {
		return fmt.Errorf("upload identity is required")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save runtime routes transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_routes WHERE site_sha = ? AND upload_version = ?`, siteSHA, version); err != nil {
		return fmt.Errorf("clear runtime routes: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO runtime_routes (
			site_sha, upload_version, route_path, route_kind, runtime_kind,
			entrypoint, bundle_object_key, methods_json, required_capabilities_json, resource_limits_json
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare runtime route insert: %w", err)
	}
	defer stmt.Close()

	for _, route := range routes {
		if route.RoutePath == "" {
			return fmt.Errorf("runtime route path is required")
		}
		switch route.RouteKind {
		case appruntime.RouteHTTP, appruntime.RouteWebSocket:
		default:
			return fmt.Errorf("unsupported runtime route kind: %s", route.RouteKind)
		}
		if route.RuntimeKind == "" {
			route.RuntimeKind = appruntime.RuntimeDisabled
		}
		// Validate the basic executable metadata before persistence. Runtime
		// service still fails closed at invocation time because older releases or
		// manual database edits may contain malformed rows.
		methodsJSON, err := json.Marshal(route.Methods)
		if err != nil {
			return fmt.Errorf("marshal runtime route methods: %w", err)
		}
		capabilitiesJSON, err := json.Marshal(route.RequiredCapabilities)
		if err != nil {
			return fmt.Errorf("marshal runtime route capabilities: %w", err)
		}
		limitsJSON, err := json.Marshal(route.ResourceLimits)
		if err != nil {
			return fmt.Errorf("marshal runtime route limits: %w", err)
		}
		if _, err := stmt.ExecContext(ctx,
			siteSHA,
			version,
			route.RoutePath,
			string(route.RouteKind),
			string(route.RuntimeKind),
			route.Entrypoint,
			route.BundleObjectKey,
			string(methodsJSON),
			string(capabilitiesJSON),
			string(limitsJSON),
		); err != nil {
			return fmt.Errorf("save runtime route %s: %w", route.RoutePath, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save runtime routes: %w", err)
	}
	return nil
}

func (d *Database) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	// Keep this narrow: route metadata by release identity. If execution needs
	// blob contents, secrets, or environment, add separate interfaces instead of
	// growing this into a catch-all runtime database API.
	rows, err := d.readDB.QueryContext(ctx, `
		SELECT COALESCE(s.site, ''),
			rr.site_sha,
			rr.upload_version,
			rr.route_path,
			rr.route_kind,
			rr.runtime_kind,
			rr.entrypoint,
			rr.bundle_object_key,
			rr.methods_json,
			rr.required_capabilities_json,
			rr.resource_limits_json,
			rr.created_at
		FROM runtime_routes rr
		LEFT JOIN sites s ON s.site_sha = rr.site_sha
		WHERE rr.site_sha = ? AND rr.upload_version = ?
		ORDER BY rr.route_path ASC, rr.route_kind ASC
	`, siteSHA, version)
	if err != nil {
		return nil, fmt.Errorf("list runtime routes: %w", err)
	}
	defer rows.Close()
	return scanRuntimeRoutes(rows)
}

func (d *Database) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	// Public routing can cache this current-release view, but the executor path
	// re-checks route metadata and policy at invocation time so
	// publish/rollback/policy changes fail closed.
	rows, err := d.readDB.QueryContext(ctx, `
		SELECT s.site,
			rr.site_sha,
			rr.upload_version,
			rr.route_path,
			rr.route_kind,
			rr.runtime_kind,
			rr.entrypoint,
			rr.bundle_object_key,
			rr.methods_json,
			rr.required_capabilities_json,
			rr.resource_limits_json,
			rr.created_at
		FROM sites s
		JOIN runtime_routes rr
			ON rr.site_sha = s.site_sha
			AND rr.upload_version = s.current_version
		JOIN uploads u
			ON u.site_sha = rr.site_sha
			AND u.version = rr.upload_version
			AND u.state = ?
		WHERE s.current_version > 0 AND s.live_state = 'live'
		ORDER BY s.site ASC, rr.route_path ASC, rr.route_kind ASC
	`, string(domain.UploadStateFinished))
	if err != nil {
		return nil, fmt.Errorf("list current runtime routes: %w", err)
	}
	defer rows.Close()
	return scanRuntimeRoutes(rows)
}

func scanRuntimeRoutes(rows *sql.Rows) ([]appruntime.RouteMetadata, error) {
	var out []appruntime.RouteMetadata
	for rows.Next() {
		var route appruntime.RouteMetadata
		var routeKind string
		var runtimeKind string
		var methodsJSON string
		var capabilitiesJSON string
		var limitsJSON string
		if err := rows.Scan(
			&route.Site,
			&route.SiteSHA,
			&route.Version,
			&route.RoutePath,
			&routeKind,
			&runtimeKind,
			&route.Entrypoint,
			&route.BundleObjectKey,
			&methodsJSON,
			&capabilitiesJSON,
			&limitsJSON,
			&route.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan runtime route: %w", err)
		}
		route.RouteKind = appruntime.RouteKind(routeKind)
		route.RuntimeKind = appruntime.RuntimeKind(runtimeKind)
		if err := json.Unmarshal([]byte(methodsJSON), &route.Methods); err != nil {
			return nil, fmt.Errorf("decode runtime route methods: %w", err)
		}
		if err := json.Unmarshal([]byte(capabilitiesJSON), &route.RequiredCapabilities); err != nil {
			return nil, fmt.Errorf("decode runtime route capabilities: %w", err)
		}
		if err := json.Unmarshal([]byte(limitsJSON), &route.ResourceLimits); err != nil {
			return nil, fmt.Errorf("decode runtime route limits: %w", err)
		}
		out = append(out, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime routes: %w", err)
	}
	return out, nil
}

func (d *Database) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	rows, err := d.readDB.QueryContext(ctx, `
		SELECT site, site_sha, current_version
		FROM sites
		WHERE current_version > 0
	`)
	if err != nil {
		return nil, fmt.Errorf("list current sites: %w", err)
	}

	var out []domain.CurrentSiteManifest
	for rows.Next() {
		var m domain.CurrentSiteManifest
		if err := rows.Scan(&m.Site, &m.SiteSHA, &m.Version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan current site: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate current sites: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close current sites: %w", err)
	}

	for i := range out {
		m := &out[i]
		settings, err := d.LoadUploadSettings(ctx, m.SiteSHA, m.Version)
		if err != nil {
			return nil, err
		}
		if _, ok := settings[appsettings.SettingDatabaseFeature]; !ok {
			settings[appsettings.SettingDatabaseFeature] = "false"
		}
		if _, ok := settings[appsettings.SettingDatabaseFeatureRequired]; !ok {
			settings[appsettings.SettingDatabaseFeatureRequired] = "false"
		}
		m.Settings = settings
	}
	return out, nil
}

func (d *Database) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	rows, err := d.readDB.QueryContext(ctx, `
		SELECT site_sha, upload_version, key, requested_value, policy_value, severity, reason
		FROM site_policy_violations
		WHERE site_sha = ? AND upload_version = ? AND resolved_at IS NULL
	`, siteSHA, version)
	if err != nil {
		return nil, fmt.Errorf("list policy violations: %w", err)
	}
	defer rows.Close()
	var out []domain.PolicyViolation
	for rows.Next() {
		var v domain.PolicyViolation
		if err := rows.Scan(&v.SiteSHA, &v.UploadVersion, &v.Key, &v.RequestedValue, &v.PolicyValue, &v.Severity, &v.Reason); err != nil {
			return nil, fmt.Errorf("scan policy violation: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *Database) SavePolicyViolation(ctx context.Context, violation domain.PolicyViolation) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if _, err := d.writeDB.ExecContext(ctx, `
		INSERT INTO site_policy_violations (site_sha, upload_version, key, requested_value, policy_value, severity, reason, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(site_sha, upload_version, key) DO UPDATE SET
			requested_value = excluded.requested_value,
			policy_value = excluded.policy_value,
			severity = excluded.severity,
			reason = excluded.reason,
			resolved_at = NULL
	`, violation.SiteSHA, violation.UploadVersion, violation.Key, violation.RequestedValue, violation.PolicyValue, violation.Severity, violation.Reason); err != nil {
		return fmt.Errorf("save policy violation: %w", err)
	}
	return nil
}

func (d *Database) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if _, err := d.writeDB.ExecContext(ctx, `
		UPDATE site_policy_violations
		SET resolved_at = CURRENT_TIMESTAMP
		WHERE site_sha = ? AND upload_version = ? AND key = ? AND resolved_at IS NULL
	`, siteSHA, version, key); err != nil {
		return fmt.Errorf("resolve policy violation: %w", err)
	}
	return nil
}

func (d *Database) AuthenticateAdmin(ctx context.Context, username string, password string) (domain.AdminUser, bool, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return domain.AdminUser{}, false, nil
	}

	var user domain.AdminUser
	var passwordHash string
	err := d.readDB.QueryRowContext(ctx, `
		SELECT id, username, admin_priv, password_hash
		FROM users
		WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.AdminPriv, &passwordHash)
	if err == sql.ErrNoRows {
		return domain.AdminUser{}, false, nil
	}
	if err != nil {
		return domain.AdminUser{}, false, fmt.Errorf("lookup admin user: %w", err)
	}
	ok, err := verifyPassword(password, passwordHash)
	if err != nil {
		return domain.AdminUser{}, false, fmt.Errorf("verify admin password: %w", err)
	}
	if !ok {
		return domain.AdminUser{}, false, nil
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

func (d *Database) FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	if token == "" {
		return domain.AdminUser{}, false, nil
	}
	var user domain.AdminUser
	err := d.readDB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.admin_priv
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?
			AND s.expires_at > CURRENT_TIMESTAMP
	`, hashToken(token)).Scan(&user.ID, &user.Username, &user.AdminPriv)
	if err == sql.ErrNoRows {
		return domain.AdminUser{}, false, nil
	}
	if err != nil {
		return domain.AdminUser{}, false, fmt.Errorf("find admin session: %w", err)
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

func (d *Database) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error) {
	if site == "" {
		return domain.UploadRecord{}, fmt.Errorf("site is required")
	}
	if siteSHA == "" {
		return domain.UploadRecord{}, fmt.Errorf("site sha is required")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.UploadRecord{}, fmt.Errorf("begin upload transaction: %w", err)
	}
	defer tx.Rollback()

	if publisherUserID > 0 && !publisherIsAdmin {
		var siteExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE site_sha = ?`, siteSHA).Scan(&siteExists); err != nil {
			return domain.UploadRecord{}, fmt.Errorf("check site ownership: %w", err)
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
				return domain.UploadRecord{}, fmt.Errorf("check site owner: %w", err)
			}
			if owned == 0 {
				return domain.UploadRecord{}, domain.ErrSiteOwnership
			}
		}
	}

	var version int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO sites (site_sha, site, current_version, next_version, live_state, updated_at)
		VALUES (?, ?, 0, 2, 'live', CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			next_version = MAX(next_version, current_version + 1) + 1,
			updated_at = CURRENT_TIMESTAMP
		RETURNING next_version - 1
	`, siteSHA, site).Scan(&version); err != nil {
		return domain.UploadRecord{}, fmt.Errorf("allocate upload version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO uploads (site_sha, site, version, publisher_user_id, files, bytes, state)
		VALUES (?, ?, ?, NULLIF(?, 0), 0, 0, ?)
	`, siteSHA, site, version, publisherUserID, string(domain.UploadStateUploading)); err != nil {
		return domain.UploadRecord{}, fmt.Errorf("create uploading record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.UploadRecord{}, fmt.Errorf("commit begin upload: %w", err)
	}

	return domain.UploadRecord{
		Site:    site,
		SiteSHA: siteSHA,
		Version: version,
		State:   domain.UploadStateUploading,
	}, nil
}

func (d *Database) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
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
	`, len(upload.Files), totalBytes, string(domain.UploadStateFinished), uploadID, string(domain.UploadStateUploading))
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
		INSERT INTO sites (site_sha, site, current_version, next_version, live_state, updated_at)
		VALUES (?, ?, ?, ?, 'live', CURRENT_TIMESTAMP)
		ON CONFLICT(site_sha) DO UPDATE SET
			site = excluded.site,
			current_version = MAX(current_version, excluded.current_version),
			next_version = MAX(next_version, excluded.next_version),
			live_state = 'live',
			updated_at = CURRENT_TIMESTAMP
	`, upload.SiteSHA, upload.Site, upload.Version, upload.Version+1); err != nil {
		return fmt.Errorf("publish site version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish upload transaction: %w", err)
	}
	return nil
}

func (d *Database) FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error {
	if upload.SiteSHA == "" || upload.Version <= 0 {
		return nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.writeDB.ExecContext(ctx, `
		UPDATE uploads
		SET state = ?, error = ?
		WHERE site_sha = ? AND version = ? AND state = ?
	`, string(domain.UploadStateError), reason, upload.SiteSHA, upload.Version, string(domain.UploadStateUploading))
	if err != nil {
		return fmt.Errorf("mark upload error: %w", err)
	}
	return nil
}

func (d *Database) FindCurrentFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, error) {
	file, fileOK, _, err := d.FindCurrentSiteFile(ctx, site, relativePath)
	return file, fileOK, err
}

func (d *Database) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	var currentVersion int64
	err := d.readDB.QueryRowContext(ctx, `
		SELECT current_version
		FROM sites
		WHERE site = ? AND live_state = 'live' AND current_version > 0
	`, site).Scan(&currentVersion)
	if err == sql.ErrNoRows {
		return domain.UploadFileRecord{}, false, false, nil
	}
	if err != nil {
		return domain.UploadFileRecord{}, false, false, fmt.Errorf("find current site: %w", err)
	}

	var file domain.UploadFileRecord
	err = d.readDB.QueryRowContext(ctx, `
		SELECT uf.relative_path, uf.blob_path, uf.file_sha, uf.bytes
		FROM uploads u
		JOIN upload_files uf ON uf.upload_id = u.id
		WHERE u.site = ?
			AND u.version = ?
			AND u.state = ?
			AND uf.relative_path = ?
	`, site, currentVersion, string(domain.UploadStateFinished), relativePath).Scan(&file.RelativePath, &file.BlobPath, &file.FileSHA, &file.Bytes)
	if err == nil {
		return file, true, true, nil
	}
	if err == sql.ErrNoRows {
		return domain.UploadFileRecord{}, false, true, nil
	}
	return domain.UploadFileRecord{}, false, true, fmt.Errorf("find current file: %w", err)
}

func (d *Database) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	var currentVersion int64
	err := d.readDB.QueryRowContext(ctx, `
		SELECT current_version
		FROM sites
		WHERE site = ? AND live_state = 'live' AND current_version > 0
	`, site).Scan(&currentVersion)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find current site: %w", err)
	}

	rows, err := d.readDB.QueryContext(ctx, `
		SELECT uf.relative_path, uf.blob_path, uf.file_sha, uf.bytes
		FROM uploads u
		JOIN upload_files uf ON uf.upload_id = u.id
		WHERE u.site = ?
			AND u.version = ?
			AND u.state = ?
	`, site, currentVersion, string(domain.UploadStateFinished))
	if err != nil {
		return nil, true, fmt.Errorf("list current files: %w", err)
	}
	defer rows.Close()

	var out []domain.UploadFileRecord
	for rows.Next() {
		var file domain.UploadFileRecord
		if err := rows.Scan(&file.RelativePath, &file.BlobPath, &file.FileSHA, &file.Bytes); err != nil {
			return nil, true, fmt.Errorf("scan current file: %w", err)
		}
		out = append(out, file)
	}
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("iterate current files: %w", err)
	}
	return out, true, nil
}

func (d *Database) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	if siteSHA == "" {
		return nil, nil
	}
	if !user.IsAdmin() {
		allowed, err := d.userCanAccessSite(ctx, d.readDB, user.ID, siteSHA)
		if err != nil {
			return nil, err
		}
		if !allowed {
			exists, err := d.siteExists(ctx, d.readDB, siteSHA)
			if err != nil {
				return nil, err
			}
			if exists {
				return nil, domain.ErrSiteOwnership
			}
			return nil, nil
		}
	}

	rows, err := d.readDB.QueryContext(ctx, `
		SELECT u.version,
			u.version = s.current_version AS current,
			u.files,
			u.bytes,
			COALESCE(pub.username, '') AS published_by,
			u.created_at,
			COALESCE(u.finished_at, '') AS finished_at
		FROM uploads u
		JOIN sites s ON s.site_sha = u.site_sha
		LEFT JOIN users pub ON pub.id = u.publisher_user_id
		WHERE u.site_sha = ? AND u.state = ?
		ORDER BY u.version DESC
	`, siteSHA, string(domain.UploadStateFinished))
	if err != nil {
		return nil, fmt.Errorf("list site revisions: %w", err)
	}
	defer rows.Close()

	var revisions []domain.RevisionRecord
	for rows.Next() {
		var rev domain.RevisionRecord
		var current int
		if err := rows.Scan(&rev.Version, &current, &rev.Files, &rev.Bytes, &rev.PublishedBy, &rev.CreatedAt, &rev.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan site revision: %w", err)
		}
		rev.Current = current != 0
		revisions = append(revisions, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate site revisions: %w", err)
	}
	return revisions, nil
}

func (d *Database) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	if siteSHA == "" {
		return domain.RollbackRecord{Warning: "no older revisions available"}, nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("begin rollback transaction: %w", err)
	}
	defer tx.Rollback()

	if !user.IsAdmin() {
		allowed, err := d.userCanAccessSite(ctx, tx, user.ID, siteSHA)
		if err != nil {
			return domain.RollbackRecord{}, err
		}
		if !allowed {
			exists, err := d.siteExists(ctx, tx, siteSHA)
			if err != nil {
				return domain.RollbackRecord{}, err
			}
			if exists {
				return domain.RollbackRecord{}, domain.ErrSiteOwnership
			}
			return domain.RollbackRecord{Warning: "no older revisions available"}, nil
		}
	}

	var currentVersion int64
	err = tx.QueryRowContext(ctx, `SELECT current_version FROM sites WHERE site_sha = ?`, siteSHA).Scan(&currentVersion)
	if err == sql.ErrNoRows {
		return domain.RollbackRecord{Warning: "no older revisions available"}, nil
	}
	if err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("load current site version: %w", err)
	}

	var previousVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT version
		FROM uploads
		WHERE site_sha = ? AND state = ? AND version < ?
		ORDER BY version DESC
		LIMIT 1
	`, siteSHA, string(domain.UploadStateFinished), currentVersion).Scan(&previousVersion)
	if err == sql.ErrNoRows {
		return domain.RollbackRecord{CurrentVersion: currentVersion, Warning: "no older revisions available"}, nil
	}
	if err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("find previous site revision: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE sites
		SET current_version = ?, updated_at = CURRENT_TIMESTAMP
		WHERE site_sha = ? AND current_version = ?
	`, previousVersion, siteSHA, currentVersion)
	if err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("rollback site version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("rollback rows affected: %w", err)
	}
	if affected != 1 {
		return domain.RollbackRecord{}, fmt.Errorf("site version changed during rollback")
	}
	if err := tx.Commit(); err != nil {
		return domain.RollbackRecord{}, fmt.Errorf("commit rollback transaction: %w", err)
	}
	return domain.RollbackRecord{
		RolledBack:      true,
		PreviousVersion: currentVersion,
		CurrentVersion:  previousVersion,
	}, nil
}

func (d *Database) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	if siteSHA == "" {
		return domain.UnpublishRecord{LiveState: "unpublished"}, nil
	}

	changed, err := d.setSiteLiveState(ctx, user, site, siteSHA, "unpublished")
	if err != nil {
		return domain.UnpublishRecord{}, err
	}
	return domain.UnpublishRecord{
		Unpublished: changed,
		LiveState:   "unpublished",
	}, nil
}

func (d *Database) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	if siteSHA == "" {
		return domain.PublishRecord{LiveState: "live"}, nil
	}

	changed, err := d.setSiteLiveState(ctx, user, site, siteSHA, "live")
	if err != nil {
		return domain.PublishRecord{}, err
	}
	return domain.PublishRecord{
		Published: changed,
		LiveState: "live",
	}, nil
}

func (d *Database) setSiteLiveState(ctx context.Context, user domain.AdminUser, site string, siteSHA string, liveState string) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin site state transaction: %w", err)
	}
	defer tx.Rollback()

	if !user.IsAdmin() {
		allowed, err := d.userCanAccessSite(ctx, tx, user.ID, siteSHA)
		if err != nil {
			return false, err
		}
		if !allowed {
			exists, err := d.siteExists(ctx, tx, siteSHA)
			if err != nil {
				return false, err
			}
			if exists {
				return false, domain.ErrSiteOwnership
			}
			return false, nil
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE sites
		SET live_state = ?, updated_at = CURRENT_TIMESTAMP
		WHERE site_sha = ? AND site = ?
	`, liveState, siteSHA, site)
	if err != nil {
		return false, fmt.Errorf("set site live state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("site state rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit site state transaction: %w", err)
	}
	return affected > 0, nil
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_routes WHERE site_sha = ?`, siteSHA); err != nil {
		return false, fmt.Errorf("delete runtime routes: %w", err)
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

func uploadID(ctx context.Context, tx *sql.Tx, upload domain.UploadRecord) (int64, error) {
	var uploadID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM uploads
		WHERE site_sha = ? AND version = ?
	`, upload.SiteSHA, upload.Version).Scan(&uploadID); err != nil {
		return 0, fmt.Errorf("lookup upload id: %w", err)
	}
	return uploadID, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (d *Database) siteExists(ctx context.Context, q rowQuerier, siteSHA string) (bool, error) {
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE site_sha = ?)`, siteSHA).Scan(&exists); err != nil {
		return false, fmt.Errorf("check site exists: %w", err)
	}
	return exists != 0, nil
}

func (d *Database) userCanAccessSite(ctx context.Context, q rowQuerier, userID int64, siteSHA string) (bool, error) {
	if userID <= 0 || siteSHA == "" {
		return false, nil
	}
	var owned int
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_sites WHERE site_sha = ? AND user_id = ?
			UNION
			SELECT 1 FROM uploads WHERE site_sha = ? AND publisher_user_id = ?
		)
	`, siteSHA, userID, siteSHA, userID).Scan(&owned); err != nil {
		return false, fmt.Errorf("check site owner: %w", err)
	}
	return owned != 0, nil
}
