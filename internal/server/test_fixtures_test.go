package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"quack/internal/domain"
	appruntime "quack/internal/runtime"
	"quack/internal/storage"
	"strconv"
	"strings"
	"testing"
)

type fakeStorage struct {
	root            string
	deletedVersions *[]int64
}

func (fakeStorage) AcceptFile(ctx context.Context, file storage.StoredFile) (storage.StoredFileResult, error) {
	n, err := io.Copy(io.Discard, file.Body)
	if err != nil {
		return storage.StoredFileResult{}, err
	}
	return storage.StoredFileResult{
		BlobPath: "blobs/site:fake/1/file:fake",
		FileSHA:  "fake",
		Bytes:    n,
	}, nil
}

func (s fakeStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	if s.root != "" {
		return os.Open(filepath.Join(s.root, blobPath))
	}
	return nil, os.ErrNotExist
}

func (fakeStorage) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

func (s fakeStorage) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	if s.deletedVersions != nil {
		*s.deletedVersions = append(*s.deletedVersions, version)
	}
	return nil
}

type fakeDatabase struct {
	files                map[string]domain.UploadFileRecord
	adminUser            domain.AdminUser
	usersByToken         map[string]domain.AdminUser
	sessions             map[string]domain.AdminUser
	settings             domain.ServerSettings
	policies             []domain.PolicyRecord
	uploadSettings       map[string]map[string]string
	runtimeRoutes        map[string][]appruntime.RouteMetadata
	violations           map[string][]domain.PolicyViolation
	prunedVersions       []int64
	revisions            []domain.RevisionRecord
	rollback             domain.RollbackRecord
	unpublish            domain.UnpublishRecord
	publish              domain.PublishRecord
	sites                []domain.PublishedSite
	lastPublisherUserID  int64
	lastPublisherIsAdmin bool
	linkedUserID         int64
	linkedSiteSHA        string
}

func (db *fakeDatabase) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error) {
	db.lastPublisherUserID = publisherUserID
	db.lastPublisherIsAdmin = publisherIsAdmin
	return domain.UploadRecord{
		Site:    site,
		SiteSHA: siteSHA,
		Version: 1,
		State:   domain.UploadStateUploading,
	}, nil
}

func (fakeDatabase) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
	return nil
}

func (fakeDatabase) FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error {
	return nil
}

func (db fakeDatabase) FindCurrentFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, error) {
	file, fileOK, _, err := db.FindCurrentSiteFile(ctx, site, relativePath)
	return file, fileOK, err
}

func (db fakeDatabase) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	file, ok := db.files[fileKey(site, relativePath)]
	if ok {
		return file, true, true, nil
	}
	for key := range db.files {
		if strings.HasPrefix(key, site+"\x00") {
			return domain.UploadFileRecord{}, false, true, nil
		}
	}
	return domain.UploadFileRecord{}, false, false, nil
}

func (db fakeDatabase) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	var out []domain.UploadFileRecord
	for key, file := range db.files {
		if strings.HasPrefix(key, site+"\x00") {
			out = append(out, file)
		}
	}
	return out, len(out) > 0, nil
}

func (db *fakeDatabase) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	return db.revisions, nil
}

func (db *fakeDatabase) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	return db.rollback, nil
}

func (db *fakeDatabase) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	return db.unpublish, nil
}

func (db *fakeDatabase) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	return db.publish, nil
}

func (fakeDatabase) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return true, nil
}

func (db *fakeDatabase) AuthenticateAdmin(ctx context.Context, username string, password string) (domain.AdminUser, bool, error) {
	if db.adminUser.ID == 0 || username != db.adminUser.Username || password != "secret" {
		return domain.AdminUser{}, false, nil
	}
	return db.adminUser, true, nil
}

func (db *fakeDatabase) FindUserByToken(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	if db.usersByToken != nil {
		user, ok := db.usersByToken[token]
		return user, ok, nil
	}
	if token == "user-token" && db.adminUser.ID > 0 {
		return db.adminUser, true, nil
	}
	return domain.AdminUser{}, false, nil
}

func (db *fakeDatabase) CreateAdminSession(ctx context.Context, userID int64) (string, error) {
	if db.sessions == nil {
		db.sessions = map[string]domain.AdminUser{}
	}
	token := "test-session-token"
	db.sessions[token] = db.adminUser
	return token, nil
}

func (db *fakeDatabase) FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	user, ok := db.sessions[token]
	return user, ok, nil
}

func (db *fakeDatabase) DeleteAdminSession(ctx context.Context, token string) error {
	delete(db.sessions, token)
	return nil
}

func (db *fakeDatabase) CreateUser(ctx context.Context, username string, adminPriv string) (domain.CreatedUser, error) {
	return domain.CreatedUser{
		User:     domain.AdminUser{ID: 99, Username: username, AdminPriv: adminPriv},
		Password: "generated-password",
		Token:    "generated-token",
	}, nil
}

func (db *fakeDatabase) ListUserSites(ctx context.Context, userID int64) ([]domain.PublishedSite, error) {
	return db.sites, nil
}

func (db *fakeDatabase) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	if includeAll {
		return db.sites, nil
	}
	return db.sites, nil
}

func (db *fakeDatabase) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	return db.sites, nil
}

func (db *fakeDatabase) LinkUserSite(ctx context.Context, userID int64, siteSHA string) error {
	db.linkedUserID = userID
	db.linkedSiteSHA = siteSHA
	return nil
}

func (db *fakeDatabase) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	if db.settings.MaxUploadBytes == 0 && db.settings.MaxUploadFiles == 0 && db.settings.LogLevel == "" {
		return domain.ServerSettings{MaxUploadBytes: DefaultMaxUploadBytes, MaxUploadFiles: DefaultMaxUploadFiles, LogLevel: "warn"}, nil
	}
	if db.settings.LogLevel == "" {
		db.settings.LogLevel = "warn"
	}
	return db.settings, nil
}

func (db *fakeDatabase) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	db.settings = settings
	return nil
}

func (db *fakeDatabase) PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error) {
	return db.prunedVersions, nil
}

func (db *fakeDatabase) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, policy := range db.policies {
		for _, scope := range scopes {
			if policy.ScopeType == scope.Type && policy.ScopeID == scope.ID {
				out = append(out, policy)
			}
		}
	}
	return out, nil
}

func (db *fakeDatabase) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	if policy.ScopeType == "" {
		policy.ScopeType = domain.ScopeSystem
	}
	for i := range db.policies {
		if db.policies[i].ScopeType == policy.ScopeType && db.policies[i].ScopeID == policy.ScopeID && db.policies[i].Key == policy.Key {
			if policy.Mode == "inherit" {
				db.policies = append(db.policies[:i], db.policies[i+1:]...)
				return nil
			}
			db.policies[i] = policy
			return nil
		}
	}
	if policy.Mode != "inherit" {
		db.policies = append(db.policies, policy)
	}
	return nil
}

func (db *fakeDatabase) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	if db.uploadSettings == nil {
		return map[string]string{}, nil
	}
	settings := db.uploadSettings[siteSHA+":"+strconv.FormatInt(version, 10)]
	out := map[string]string{}
	for k, v := range settings {
		out[k] = v
	}
	return out, nil
}

func (db *fakeDatabase) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	if db.uploadSettings == nil {
		db.uploadSettings = map[string]map[string]string{}
	}
	key := siteSHA + ":" + strconv.FormatInt(version, 10)
	db.uploadSettings[key] = settings
	return nil
}

func (db *fakeDatabase) SaveRuntimeRoutes(ctx context.Context, siteSHA string, version int64, routes []appruntime.RouteMetadata) error {
	if db.runtimeRoutes == nil {
		db.runtimeRoutes = map[string][]appruntime.RouteMetadata{}
	}
	key := siteSHA + ":" + strconv.FormatInt(version, 10)
	db.runtimeRoutes[key] = append([]appruntime.RouteMetadata(nil), routes...)
	return nil
}

func (db *fakeDatabase) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	if db.runtimeRoutes == nil {
		return nil, nil
	}
	key := siteSHA + ":" + strconv.FormatInt(version, 10)
	return append([]appruntime.RouteMetadata(nil), db.runtimeRoutes[key]...), nil
}

func (db *fakeDatabase) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	var out []appruntime.RouteMetadata
	for _, site := range db.sites {
		routes, _ := db.ListRuntimeRoutes(ctx, site.SiteSHA, site.CurrentVersion)
		for _, route := range routes {
			route.Site = site.Site
			route.SiteSHA = site.SiteSHA
			route.Version = site.CurrentVersion
			out = append(out, route)
		}
	}
	return out, nil
}

func (db *fakeDatabase) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	var out []domain.CurrentSiteManifest
	for _, site := range db.sites {
		settings, _ := db.LoadUploadSettings(ctx, site.SiteSHA, site.CurrentVersion)
		out = append(out, domain.CurrentSiteManifest{Site: site.Site, SiteSHA: site.SiteSHA, Version: site.CurrentVersion, Settings: settings})
	}
	return out, nil
}

func (db *fakeDatabase) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	if db.violations == nil {
		return nil, nil
	}
	return db.violations[siteSHA+":"+strconv.FormatInt(version, 10)], nil
}

func (db *fakeDatabase) SavePolicyViolation(ctx context.Context, violation domain.PolicyViolation) error {
	if db.violations == nil {
		db.violations = map[string][]domain.PolicyViolation{}
	}
	key := violation.SiteSHA + ":" + strconv.FormatInt(violation.UploadVersion, 10)
	db.violations[key] = []domain.PolicyViolation{violation}
	return nil
}

func (db *fakeDatabase) ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error {
	if db.violations != nil {
		delete(db.violations, siteSHA+":"+strconv.FormatInt(version, 10))
	}
	return nil
}

func (fakeDatabase) Close() error {
	return nil
}

func fileKey(site string, relativePath string) string {
	return site + "\x00" + relativePath
}

func writeTestBlob(t *testing.T, root string, name string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
