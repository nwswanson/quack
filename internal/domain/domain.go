package domain

import "errors"

var ErrSiteOwnership = errors.New("site is owned by another user")

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

type ScopeType string

const (
	ScopeSystem ScopeType = "system"
	ScopeUser   ScopeType = "user"
	ScopeSite   ScopeType = "site"
	ScopeUpload ScopeType = "upload"
)

type SiteRuntimeStatus string

const (
	SiteRuntimeActive            SiteRuntimeStatus = "active"
	SiteRuntimeDegraded          SiteRuntimeStatus = "degraded"
	SiteRuntimeSuspendedByPolicy SiteRuntimeStatus = "suspended_by_policy"
)

type SiteRuntimeDecision struct {
	Status SiteRuntimeStatus
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
	RuntimeStatus  SiteRuntimeStatus
	PolicyReason   string
}

type ServerSettings struct {
	MaxUploadBytes      int64
	MaxUploadFiles      int64
	MaxRetainedVersions int64
	DefaultSite         string
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
