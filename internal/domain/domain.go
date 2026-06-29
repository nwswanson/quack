package domain

import (
	"errors"
	"net/netip"
)

var ErrSiteOwnership = errors.New("site is owned by another user")
var ErrAuthenticatedUserRequired = errors.New("authenticated user is required")
var ErrSecretsLocked = errors.New("secrets are locked")

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

type SecretScope string

const (
	SecretScopeSite SecretScope = "site"
	SecretScopeUser SecretScope = "user"
)

type ScopeType string

const (
	ScopeSystem ScopeType = "system"
	ScopeUser   ScopeType = "user"
	ScopeSite   ScopeType = "site"
	ScopeUpload ScopeType = "upload"
)

type SiteServingStatus string

const (
	SiteServingActive            SiteServingStatus = "active"
	SiteServingDegraded          SiteServingStatus = "degraded"
	SiteServingSuspendedByPolicy SiteServingStatus = "suspended_by_policy"
)

// SiteServingDecision describes whether a static site release may be served.
// It is not related to executing user-provided runtime code.
type SiteServingDecision struct {
	Status SiteServingStatus
	Reason string
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
	LiveState      string
	ServingStatus  SiteServingStatus
	PolicyReason   string
}

type ServerSettings struct {
	MaxUploadBytes                 int64
	MaxUploadFiles                 int64
	MaxRetainedVersions            int64
	MaxRuntimeDurationMillis       int64
	HTTPClientMaxBytes             int64
	HTTPClientMaxTimeoutMS         int64
	HTTPClientAllowedCIDRs         []netip.Prefix
	HTTPClientAllowInsecureSSL     bool
	MaxWebSocketConnections        int64
	MaxWebSocketConnectionsPerSite int64
	MaxPipesPerSite                int64
	MaxTopicsPerSite               int64
	MaxRetainedEventsPerSite       int64
	MaxRetainedBytesPerSite        int64
	HTTPCacheMode                  string
	HTTPCacheMaxAgeSeconds         int64
	MemoryPersistenceMode          string
	MemorySnapshotSave             string
	MemorySnapshotMinIntervalMS    int64
	MemorySnapshotMaxConcurrency   int64
	MemoryShutdownFlushTimeoutMS   int64
	DefaultSite                    string
	AllowedHosts                   []string
	LogLevel                       string
	LogBufferCount                 int64
	Locked                         map[string]bool
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

type RevisionRecord struct {
	Version     int64
	Current     bool
	Files       int64
	Bytes       int64
	PublishedBy string
	CreatedAt   string
	FinishedAt  string
}

type RollbackRecord struct {
	RolledBack      bool
	PreviousVersion int64
	CurrentVersion  int64
	Warning         string
}

type UnpublishRecord struct {
	Unpublished bool
	LiveState   string
}

type PublishRecord struct {
	Published bool
	LiveState string
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

type UploadRecord struct {
	Site    string
	SiteSHA string
	Version int64
	State   UploadState
	Files   []UploadFileRecord
}

type UploadState string

const (
	UploadStateUploading UploadState = "uploading"
	UploadStateFinished  UploadState = "finished"
	UploadStateError     UploadState = "error"
)

type UploadFileRecord struct {
	RelativePath string
	BlobPath     string
	FileSHA      string
	Bytes        int64
}

type MetricsSnapshot struct {
	UserCount                      int64
	SiteCount                      int64
	LiveSiteCount                  int64
	UnpublishedSiteCount           int64
	UploadCount                    int64
	FinishedUploadCount            int64
	UploadingUploadCount           int64
	FailedUploadCount              int64
	UploadBytes                    int64
	CurrentSiteBytes               int64
	UploadFileCount                int64
	RuntimeRouteCount              int64
	CurrentRuntimeRouteCount       int64
	RuntimeHTTPRouteCount          int64
	RuntimeWebSocketRouteCount     int64
	PolicyViolationCount           int64
	UnresolvedPolicyViolationCount int64
	Users                          []UserMetrics
	Sites                          []SiteMetrics
}

type UserMetrics struct {
	ID           int64
	Username     string
	SiteCount    int64
	VersionCount int64
	Bytes        int64
}

type SiteMetrics struct {
	Site          string
	SiteSHA       string
	LiveState     string
	VersionCount  int64
	UploadBytes   int64
	CurrentBytes  int64
	CurrentFiles  int64
	RuntimeRoutes int64
}

type EffectiveValue[T any] struct {
	Value    T
	Source   string
	Editable bool
	Reason   string
}

type UploadPolicy struct {
	MaxUploadBytes      EffectiveValue[int64]
	MaxUploadFiles      EffectiveValue[int64]
	MaxRetainedVersions EffectiveValue[int64]
}
