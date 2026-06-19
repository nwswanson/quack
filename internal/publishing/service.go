package publishing

import (
	"context"

	"quack/internal/protocol"
	"quack/internal/uploads"
)

type Request = uploads.Request
type LimitError = uploads.LimitError
type BadArchiveError = uploads.BadArchiveError

type ArchiveUploader interface {
	UploadArchive(ctx context.Context, req uploads.Request) (protocol.UploadArchiveResponse, error)
}

type Service interface {
	UploadArchive(ctx context.Context, req Request) (protocol.UploadArchiveResponse, error)
}

type service struct {
	uploads ArchiveUploader
}

func NewService(uploads ArchiveUploader) Service {
	return service{uploads: uploads}
}

func (s service) UploadArchive(ctx context.Context, req Request) (protocol.UploadArchiveResponse, error) {
	return s.uploads.UploadArchive(ctx, uploads.Request(req))
}
