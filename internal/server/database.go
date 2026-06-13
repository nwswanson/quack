package server

import "context"

type Database interface {
	BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64) (UploadRecord, error)
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
}

type ServerSettings struct {
	MaxUploadBytes int64
	MaxUploadFiles int64
}
