package server

import "context"

type Database interface {
	BeginUpload(ctx context.Context, site string, siteSHA string) (UploadRecord, error)
	FinishUpload(ctx context.Context, upload UploadRecord) error
	FailUpload(ctx context.Context, upload UploadRecord, reason string) error
	FindCurrentFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, error)
	DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error)
	AuthenticateAdmin(ctx context.Context, username string, password string) (AdminUser, bool, error)
	CreateAdminSession(ctx context.Context, userID int64) (string, error)
	FindAdminSession(ctx context.Context, token string) (AdminUser, bool, error)
	DeleteAdminSession(ctx context.Context, token string) error
	Close() error
}

type AdminUser struct {
	ID        int64
	Username  string
	AdminPriv string
}
