package server

import "context"

type Database interface {
	AllocateVersion(ctx context.Context, site string, siteSHA string) (int64, error)
	SaveUpload(ctx context.Context, upload UploadRecord) error
	Close() error
}
