package server

import "context"

type Database interface {
	BeginUpload(ctx context.Context, site string, siteSHA string) (UploadRecord, error)
	FinishUpload(ctx context.Context, upload UploadRecord) error
	FailUpload(ctx context.Context, upload UploadRecord, reason string) error
	FindCurrentFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, error)
	DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error)
	Close() error
}
