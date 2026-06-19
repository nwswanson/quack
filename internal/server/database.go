package server

import (
	"context"
	"quack/internal/sites"

	"quack/internal/domain"
)

var ErrSiteOwnership = domain.ErrSiteOwnership

type UploadRepository interface {
	BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error)
	FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error
	PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error)
	LinkUserSite(ctx context.Context, userID int64, siteSHA string) error
}

type SiteReadRepository interface {
	FindCurrentFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
}

type UserRepository interface {
	AuthenticateAdmin(ctx context.Context, username string, password string) (AdminUser, bool, error)
	FindUserByToken(ctx context.Context, token string) (AdminUser, bool, error)
	CreateUser(ctx context.Context, username string, adminPriv string) (CreatedUser, error)
	ListUserSites(ctx context.Context, userID int64) ([]PublishedSite, error)
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]PublishedSite, error)
	ListPublishedSitesByUsername(ctx context.Context, username string) ([]PublishedSite, error)
}

type SessionRepository interface {
	CreateAdminSession(ctx context.Context, userID int64) (string, error)
	FindAdminSession(ctx context.Context, token string) (AdminUser, bool, error)
	DeleteAdminSession(ctx context.Context, token string) error
}

type SettingsRepository interface {
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	SaveServerSettings(ctx context.Context, settings ServerSettings) error
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
}

type PolicyRepository interface {
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	SavePolicy(ctx context.Context, policy PolicyRecord) error
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	SavePolicyViolation(ctx context.Context, violation PolicyViolation) error
	ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error
}

type RevisionRepository interface {
	ListSiteRevisions(ctx context.Context, user AdminUser, site string, siteSHA string) ([]RevisionRecord, error)
}

type Database interface {
	UploadRepository
	SiteReadRepository
	sites.SiteWriteRepository
	UserRepository
	SessionRepository
	SettingsRepository
	PolicyRepository
	RevisionRepository
	Close() error
}

type AdminUser = domain.AdminUser
type CreatedUser = domain.CreatedUser
type PublishedSite = domain.PublishedSite
type ServerSettings = domain.ServerSettings
type PolicyScope = domain.PolicyScope
type PolicyRecord = domain.PolicyRecord
type CurrentSiteManifest = domain.CurrentSiteManifest
type RevisionRecord = domain.RevisionRecord
type RollbackRecord = domain.RollbackRecord
type UnpublishRecord = domain.UnpublishRecord
type PublishRecord = domain.PublishRecord
type PolicyViolation = domain.PolicyViolation
