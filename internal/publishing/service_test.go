package publishing

import (
	"context"
	"testing"

	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/uploads"
)

func TestServiceDelegatesArchiveUpload(t *testing.T) {
	delegate := &recordingUploader{}
	service := NewService(delegate)

	resp, err := service.UploadArchive(context.Background(), Request{Site: "foo", User: domain.AdminUser{ID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || delegate.request.Site != "foo" || delegate.request.User.ID != 7 {
		t.Fatalf("resp = %+v request = %+v, want delegated upload", resp, delegate.request)
	}
}

type recordingUploader struct {
	request uploads.Request
}

func (u *recordingUploader) UploadArchive(ctx context.Context, req uploads.Request) (protocol.UploadArchiveResponse, error) {
	u.request = req
	return protocol.UploadArchiveResponse{OK: true, Site: req.Site}, nil
}
