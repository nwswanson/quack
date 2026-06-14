package server

import (
	"context"
	"errors"
)

var ErrSiteOwnership = errors.New("site is owned by another user")

type Database interface {
	BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (UploadRecord, error)
	FinishUpload(ctx context.Context, upload UploadRecord) error
	FailUpload(ctx context.Context, upload UploadRecord, reason string) error
	FindCurrentFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, error)
	DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error)
	AuthenticateAdmin(ctx context.Context, username string, password string) (AdminUser, bool, error)
	FindUserByToken(ctx context.Context, token string) (AdminUser, bool, error)
	CreateAdminSession(ctx context.Context, userID int64) (string, error)
	FindAdminSession(ctx context.Context, token string) (AdminUser, bool, error)
	DeleteAdminSession(ctx context.Context, token string) error
	CreateUser(ctx context.Context, username string, adminPriv string) (CreatedUser, error)
	ListUserSites(ctx context.Context, userID int64) ([]PublishedSite, error)
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]PublishedSite, error)
	LinkUserSite(ctx context.Context, userID int64, siteSHA string) error
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	SaveServerSettings(ctx context.Context, settings ServerSettings) error
	PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error)
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	SavePolicy(ctx context.Context, policy PolicyRecord) error
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	SavePolicyViolation(ctx context.Context, violation PolicyViolation) error
	ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error
	Close() error
}

type AdminUser struct {
	ID        int64
	Username  string
	AdminPriv string
}

func (u AdminUser) IsAdmin() bool {
	return u.AdminPriv == "admin:*"
}

type CreatedUser struct {
	User     AdminUser
	Password string
	Token    string
}

type PublishedSite struct {
	Site           string
	SiteSHA        string
	PublishedBy    string
	CurrentVersion int64
	VersionCount   int64
	FileCount      int64
	ByteCount      int64
	UpdatedAt      string
	RuntimeStatus  SiteRuntimeStatus
	PolicyReason   string
}

type ServerSettings struct {
	MaxUploadBytes      int64
	MaxUploadFiles      int64
	MaxRetainedVersions int64
	LogLevel            string
	Locked              map[string]bool
}

type PolicyScope struct {
	Type ScopeType
	ID   string
}

type PolicyRecord struct {
	ScopeType       ScopeType
	ScopeID         string
	Key             string
	Mode            string
	Value           string
	Reason          string
	UpdatedByUserID int64
}

type CurrentSiteManifest struct {
	Site     string
	SiteSHA  string
	Version  int64
	Settings map[string]string
}

type PolicyViolation struct {
	SiteSHA        string
	UploadVersion  int64
	Key            string
	RequestedValue string
	PolicyValue    string
	Severity       string
	Reason         string
}
