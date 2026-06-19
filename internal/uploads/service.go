package uploads

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/protocol"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

type UploadRepository interface {
	BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error)
	FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error
	PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error)
	LinkUserSite(ctx context.Context, userID int64, siteSHA string) error
}

type Service interface {
	UploadArchive(ctx context.Context, req Request) (protocol.UploadArchiveResponse, error)
}

type Request struct {
	Site   string
	User   domain.AdminUser
	Policy domain.UploadPolicy
	Body   io.Reader
}

type service struct {
	db    UploadRepository
	store appstorage.Storage
	read  sites.SiteReadService
	write sites.SiteWriteService
}

func NewService(db UploadRepository, store appstorage.Storage, read sites.SiteReadService, write sites.SiteWriteService) Service {
	return service{
		db:    db,
		store: store,
		read:  read,
		write: write,
	}
}

func (s service) UploadArchive(ctx context.Context, req Request) (protocol.UploadArchiveResponse, error) {
	siteSHA := sites.HashName(req.Site)

	upload, err := s.db.BeginUpload(ctx, req.Site, siteSHA, req.User.ID, req.User.IsAdmin())
	if err != nil {
		return protocol.UploadArchiveResponse{}, fmt.Errorf("begin upload: %w", err)
	}
	slog.WarnContext(ctx, "upload started", "site", upload.Site, "version", upload.Version)

	files, bytes, manifest, err := s.acceptArchive(ctx, req.Body, &upload, req.Policy)
	if err != nil {
		s.markUploadFailed(ctx, upload, err)
		slog.WarnContext(ctx, "upload failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, err
	}

	if err := s.read.ValidateUploadManifest(ctx, req.User, req.Site, manifest); err != nil {
		s.markUploadFailed(ctx, upload, err)
		slog.WarnContext(ctx, "upload rejected by policy", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, err
	}
	if err := s.write.SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, ManifestSettings(manifest)); err != nil {
		s.markUploadFailed(ctx, upload, err)
		return protocol.UploadArchiveResponse{}, fmt.Errorf("save upload settings: %w", err)
	}

	if err := s.write.FinishUpload(ctx, upload); err != nil {
		s.markUploadFailed(ctx, upload, err)
		slog.ErrorContext(ctx, "finish upload metadata failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, fmt.Errorf("finish upload metadata: %w", err)
	}
	if err := s.pruneRetainedVersions(ctx, upload, req.Policy.MaxRetainedVersions.Value); err != nil {
		return protocol.UploadArchiveResponse{}, err
	}
	if req.User.ID > 0 {
		if err := s.db.LinkUserSite(ctx, req.User.ID, upload.SiteSHA); err != nil {
			slog.ErrorContext(ctx, "link user site failed", "site", upload.Site, "version", upload.Version, "username", req.User.Username, "error", err)
			return protocol.UploadArchiveResponse{}, fmt.Errorf("link user site: %w", err)
		}
	}

	slog.WarnContext(ctx, "upload finished", "site", upload.Site, "version", upload.Version, "files", files, "bytes", bytes)
	return protocol.UploadArchiveResponse{
		OK:      true,
		Site:    upload.Site,
		Version: upload.Version,
		Files:   files,
		Bytes:   bytes,
	}, nil
}

func (s service) markUploadFailed(ctx context.Context, upload domain.UploadRecord, uploadErr error) {
	if markErr := s.db.FailUpload(ctx, upload, uploadErr.Error()); markErr != nil {
		slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", uploadErr, "error", markErr)
	}
}

func (s service) pruneRetainedVersions(ctx context.Context, upload domain.UploadRecord, maxRetainedVersions int64) error {
	if maxRetainedVersions <= 0 {
		return nil
	}
	versions, err := s.db.PruneSiteVersions(ctx, upload.SiteSHA, maxRetainedVersions)
	if err != nil {
		return fmt.Errorf("prune site versions: %w", err)
	}
	for _, version := range versions {
		if err := s.store.DeleteSiteVersion(ctx, upload.SiteSHA, version); err != nil {
			return fmt.Errorf("delete pruned site version blobs: site=%s version=%d: %w", upload.Site, version, err)
		}
		slog.WarnContext(ctx, "site version pruned", "site", upload.Site, "version", version, "max_retained_versions", maxRetainedVersions)
	}
	return nil
}

func (s service) acceptArchive(ctx context.Context, body io.Reader, upload *domain.UploadRecord, policy domain.UploadPolicy) (int64, int64, manifest.Manifest, error) {
	tr := tar.NewReader(body)

	siteManifest := manifest.Default()
	var files, bytes int64
	for {
		header, err := tr.Next()
		switch {
		case errors.Is(err, io.EOF):
			return files, bytes, siteManifest, nil
		case err != nil:
			return 0, 0, siteManifest, BadArchiveError{err: fmt.Errorf("read tar archive: %w", err)}
		}

		if protocol.IsSiteManifestArchiveEntry(header) {
			parsed, err := manifest.Parse(tr, header.Size)
			if err != nil {
				return 0, 0, siteManifest, BadArchiveError{err: err}
			}
			siteManifest = parsed
			continue
		}

		rec, n, err := s.acceptArchiveEntry(ctx, tr, header, upload.SiteSHA, upload.Version, files, policy)
		if err != nil {
			return 0, 0, siteManifest, err
		}
		if rec == nil {
			continue
		}

		upload.Files = append(upload.Files, *rec)
		files++
		bytes += n
	}
}

func (s service) acceptArchiveEntry(
	ctx context.Context,
	body io.Reader,
	header *tar.Header,
	siteSHA string,
	version, files int64,
	policy domain.UploadPolicy,
) (*domain.UploadFileRecord, int64, error) {
	if err := protocol.ValidateArchivePath(header.Name); err != nil {
		return nil, 0, BadArchiveError{err: err}
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return nil, 0, nil
	case tar.TypeReg, tar.TypeRegA:
		return s.acceptRegularFile(ctx, body, header, siteSHA, version, files, policy)
	default:
		return nil, 0, BadArchiveError{err: fmt.Errorf("unsupported archive entry type for %s", header.Name)}
	}
}

func (s service) acceptRegularFile(
	ctx context.Context,
	body io.Reader,
	header *tar.Header,
	siteSHA string,
	version, files int64,
	policy domain.UploadPolicy,
) (*domain.UploadFileRecord, int64, error) {
	if policy.MaxUploadFiles.Value > 0 && files >= policy.MaxUploadFiles.Value {
		return nil, 0, LimitError{err: fmt.Errorf("upload exceeds maximum file count: %d", policy.MaxUploadFiles.Value)}
	}

	path, err := sanitizeServingPath(header.Name)
	if err != nil {
		return nil, 0, err
	}

	result, err := s.store.AcceptFile(ctx, appstorage.StoredFile{
		SiteSHA: siteSHA, Version: version, RelativePath: path,
		Mode: header.Mode, Size: header.Size, Body: body,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("accept file %s: %w", header.Name, err)
	}

	return &domain.UploadFileRecord{
		RelativePath: path,
		BlobPath:     result.BlobPath,
		FileSHA:      result.FileSHA,
		Bytes:        result.Bytes,
	}, result.Bytes, nil
}

func sanitizeServingPath(name string) (string, error) {
	out, err := protocol.SanitizeServingPath(name)
	if err != nil {
		return "", BadArchiveError{err: err}
	}
	return out, nil
}

func ManifestSettings(manifest manifest.Manifest) map[string]string {
	settings := map[string]string{
		appsettings.SettingDatabaseFeature:         boolSetting(manifest.Features.Database.Enabled),
		appsettings.SettingDatabaseFeatureRequired: boolSetting(manifest.Features.Database.Required),
	}
	if len(manifest.Routes) > 0 {
		data, _ := json.Marshal(manifest.Routes)
		settings[appsettings.SettingRoutes] = string(data)
	}
	return settings
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

type BadArchiveError struct {
	err error
}

func (e BadArchiveError) Error() string {
	return e.err.Error()
}

func (e BadArchiveError) Unwrap() error {
	return e.err
}

type LimitError struct {
	err error
}

func (e LimitError) Error() string {
	return e.err.Error()
}

func (e LimitError) Unwrap() error {
	return e.err
}
