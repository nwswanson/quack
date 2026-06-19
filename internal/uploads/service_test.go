package uploads

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"

	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

func TestServiceUploadArchiveFinishesAndPrunes(t *testing.T) {
	db := &uploadServiceDB{prunedVersions: []int64{1, 2}}
	store := &uploadServiceStore{}
	write := &uploadServiceWrite{}
	service := NewService(db, store, uploadServiceRead{}, write)

	resp, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		User: domain.AdminUser{ID: 7, Username: "alice", AdminPriv: "user"},
		Policy: domain.UploadPolicy{
			MaxUploadFiles:      domain.EffectiveValue[int64]{Value: 10},
			MaxRetainedVersions: domain.EffectiveValue[int64]{Value: 3},
		},
		Body: tarArchive(t, map[string]string{
			"index.html": "hello",
			"site.yaml":  "features:\n  database:\n    enabled: true\n",
		}),
	})
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}
	if !resp.OK || resp.Files != 1 || resp.Bytes != int64(len("hello")) {
		t.Fatalf("response = %#v, want one uploaded file", resp)
	}
	if !write.finished {
		t.Fatal("upload was not finished")
	}
	if got, want := write.settings["features.database.enabled"], "true"; got != want {
		t.Fatalf("manifest setting = %q, want %q", got, want)
	}
	if db.linkedUserID != 7 || db.linkedSiteSHA == "" {
		t.Fatalf("linked site = (%d, %q), want user 7 and site sha", db.linkedUserID, db.linkedSiteSHA)
	}
	if got, want := store.deletedVersions, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted versions = %#v, want %#v", got, want)
	}
}

func TestServiceUploadArchiveMarksFailedWhenManifestRejected(t *testing.T) {
	db := &uploadServiceDB{}
	service := NewService(db, &uploadServiceStore{}, uploadServiceRead{validateErr: errRejectedManifest}, &uploadServiceWrite{})

	_, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		Policy: domain.UploadPolicy{
			MaxUploadFiles: domain.EffectiveValue[int64]{Value: 10},
		},
		Body: tarArchive(t, map[string]string{"index.html": "hello"}),
	})
	if !errors.Is(err, errRejectedManifest) {
		t.Fatalf("UploadArchive error = %v, want rejected manifest", err)
	}
	if db.failedReason == "" {
		t.Fatal("failed upload was not marked failed")
	}
}

var errRejectedManifest = errors.New("manifest rejected")

type uploadServiceDB struct {
	prunedVersions []int64
	failedReason   string
	linkedUserID   int64
	linkedSiteSHA  string
}

func (db *uploadServiceDB) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error) {
	return domain.UploadRecord{Site: site, SiteSHA: siteSHA, Version: 4, State: domain.UploadStateUploading}, nil
}

func (db *uploadServiceDB) FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error {
	db.failedReason = reason
	return nil
}

func (db *uploadServiceDB) PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error) {
	return db.prunedVersions, nil
}

func (db *uploadServiceDB) LinkUserSite(ctx context.Context, userID int64, siteSHA string) error {
	db.linkedUserID = userID
	db.linkedSiteSHA = siteSHA
	return nil
}

type uploadServiceStore struct {
	deletedVersions []int64
}

func (s *uploadServiceStore) AcceptFile(ctx context.Context, file appstorage.StoredFile) (appstorage.StoredFileResult, error) {
	n, err := io.Copy(io.Discard, file.Body)
	if err != nil {
		return appstorage.StoredFileResult{}, err
	}
	return appstorage.StoredFileResult{BlobPath: "blob", FileSHA: "sha", Bytes: n}, nil
}

func (s *uploadServiceStore) OpenBlob(ctx context.Context, blobPath string) (file *os.File, err error) {
	return nil, errors.New("not implemented")
}

func (s *uploadServiceStore) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	s.deletedVersions = append(s.deletedVersions, version)
	return nil
}

func (s *uploadServiceStore) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

type uploadServiceRead struct {
	validateErr error
}

func (r uploadServiceRead) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return domain.ServerSettings{}, nil
}

func (r uploadServiceRead) UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error) {
	return domain.UploadPolicy{}, nil
}

func (r uploadServiceRead) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, manifest protocol.SiteManifest) error {
	return r.validateErr
}

func (r uploadServiceRead) CurrentSiteRuntime(ctx context.Context, site string) (domain.SiteRuntimeDecision, error) {
	return domain.SiteRuntimeDecision{}, nil
}

func (r uploadServiceRead) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return domain.UploadFileRecord{}, false, false, nil
}

func (r uploadServiceRead) ServeSiteFile(ctx context.Context, site string, urlPath string) (sites.ServeSiteFileDecision, error) {
	return sites.ServeSiteFileDecision{}, nil
}

func (r uploadServiceRead) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

type uploadServiceWrite struct {
	finished bool
	settings map[string]string
}

func (w *uploadServiceWrite) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	return nil
}

func (w *uploadServiceWrite) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	return nil
}

func (w *uploadServiceWrite) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	w.settings = settings
	return nil
}

func (w *uploadServiceWrite) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
	w.finished = true
	return nil
}

func (w *uploadServiceWrite) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, nil
}

func (w *uploadServiceWrite) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	return domain.UnpublishRecord{}, nil
}

func (w *uploadServiceWrite) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	return domain.PublishRecord{}, nil
}

func (w *uploadServiceWrite) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return false, nil
}

func (w *uploadServiceWrite) ReconcilePolicyViolations(ctx context.Context) error {
	return nil
}

func tarArchive(t *testing.T, files map[string]string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}
