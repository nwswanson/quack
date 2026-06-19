package server

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

type handler struct {
	token                string
	store                appstorage.Storage
	db                   Database
	read                 SiteReadService
	write                SiteWriteService
	allowUnauthenticated bool
}

func (h *handler) siteReadService() SiteReadService {
	if h.read != nil {
		return h.read
	}
	return NewSiteReadService(NewPassthroughHotDataReader(h.db))
}

func (h *handler) siteWriteService() SiteWriteService {
	if h.write != nil {
		return h.write
	}
	hot := NewPassthroughHotDataReader(h.db)
	return NewSiteWriteService(h.db, hot, nil)
}

func (h *handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
	site, user, policy, ok := h.validUploadRequest(w, r)
	if !ok {
		return
	}

	resp, err := h.uploadArchive(r, site, user, policy)
	if err != nil {
		h.writeUploadError(w, err)
		return
	}

	protocol.WriteJSON(w, http.StatusOK, resp)
}

func (h *handler) validUploadRequest(w http.ResponseWriter, r *http.Request) (string, AdminUser, UploadPolicy, bool) {
	var user AdminUser
	var policy UploadPolicy
	switch {
	case r.Method != http.MethodPost:
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return "", user, policy, false
	}
	var ok bool
	var err error
	user, ok, err = h.authorizedUploadUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "upload authorization lookup failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return "", user, policy, false
	}
	switch {
	case !ok:
		protocol.WriteError(w, http.StatusUnauthorized, "unauthorized")
	case r.Header.Get("Content-Type") != protocol.ContentTypeTar:
		protocol.WriteError(w, http.StatusBadRequest, "content type must be application/x-tar")
	case strings.TrimSpace(r.Header.Get(protocol.HeaderSite)) == "":
		protocol.WriteError(w, http.StatusBadRequest, "site is required")
	default:
		site, err := canonicalSiteName(r.Header.Get(protocol.HeaderSite))
		if err != nil {
			protocol.WriteError(w, http.StatusBadRequest, err.Error())
			return "", user, policy, false
		}
		policy, err = h.siteReadService().UploadPolicy(r.Context(), user, site)
		if err != nil {
			slog.ErrorContext(r.Context(), "resolve upload policy failed", "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return "", user, policy, false
		}
		if policy.MaxUploadBytes.Value > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, policy.MaxUploadBytes.Value)
		}
		return site, user, policy, true
	}
	return "", user, policy, false
}

func (h *handler) writeUploadError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	var limitErr uploadLimitError
	var badRequest badArchiveError
	var forbidden forbiddenPolicyError

	switch {
	case errors.As(err, &maxBytesErr):
		protocol.WriteError(w, http.StatusRequestEntityTooLarge, "upload exceeds maximum size")
	case errors.As(err, &limitErr):
		protocol.WriteError(w, http.StatusRequestEntityTooLarge, limitErr.Error())
	case errors.As(err, &badRequest):
		protocol.WriteError(w, http.StatusBadRequest, badRequest.Error())
	case errors.As(err, &forbidden):
		protocol.WriteError(w, http.StatusForbidden, forbidden.Error())
	case errors.Is(err, ErrSiteOwnership):
		protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
	default:
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h *handler) authorizedUploadUser(r *http.Request) (AdminUser, bool, error) {
	return h.authorizedAPIUser(r)
}

func (h *handler) uploadArchive(r *http.Request, site string, user AdminUser, policy UploadPolicy) (protocol.UploadArchiveResponse, error) {
	ctx := r.Context()
	siteSHA := sha256Hex(site)

	upload, err := h.db.BeginUpload(ctx, site, siteSHA, user.ID, user.IsAdmin())
	if err != nil {
		return protocol.UploadArchiveResponse{}, fmt.Errorf("begin upload: %w", err)
	}
	slog.WarnContext(ctx, "upload started", "site", upload.Site, "version", upload.Version)

	files, bytes, manifest, err := h.acceptArchive(r, &upload, policy)
	if err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		slog.WarnContext(ctx, "upload failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, err
	}

	if err := h.siteReadService().ValidateUploadManifest(ctx, user, site, manifest); err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		slog.WarnContext(ctx, "upload rejected by policy", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, err
	}
	if err := h.siteWriteService().SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, ManifestSettings(manifest)); err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		return protocol.UploadArchiveResponse{}, fmt.Errorf("save upload settings: %w", err)
	}

	if err := h.siteWriteService().FinishUpload(ctx, upload); err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		slog.ErrorContext(ctx, "finish upload metadata failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, fmt.Errorf("finish upload metadata: %w", err)
	}
	if err := h.pruneRetainedVersions(ctx, upload, policy.MaxRetainedVersions.Value); err != nil {
		return protocol.UploadArchiveResponse{}, err
	}
	if user.ID > 0 {
		if err := h.db.LinkUserSite(ctx, user.ID, upload.SiteSHA); err != nil {
			slog.ErrorContext(ctx, "link user site failed", "site", upload.Site, "version", upload.Version, "username", user.Username, "error", err)
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

func (h *handler) pruneRetainedVersions(ctx context.Context, upload UploadRecord, maxRetainedVersions int64) error {
	if maxRetainedVersions <= 0 {
		return nil
	}
	versions, err := h.db.PruneSiteVersions(ctx, upload.SiteSHA, maxRetainedVersions)
	if err != nil {
		return fmt.Errorf("prune site versions: %w", err)
	}
	for _, version := range versions {
		if err := h.store.DeleteSiteVersion(ctx, upload.SiteSHA, version); err != nil {
			return fmt.Errorf("delete pruned site version blobs: site=%s version=%d: %w", upload.Site, version, err)
		}
		slog.WarnContext(ctx, "site version pruned", "site", upload.Site, "version", version, "max_retained_versions", maxRetainedVersions)
	}
	return nil
}

func (h *handler) acceptArchive(r *http.Request, upload *UploadRecord, policy UploadPolicy) (int64, int64, SiteManifest, error) {
	ctx := r.Context()
	tr := tar.NewReader(r.Body)

	manifest := protocol.DefaultSiteManifest()
	var files, bytes int64
	for {
		header, err := tr.Next()
		switch {
		case errors.Is(err, io.EOF):
			return files, bytes, manifest, nil
		case err != nil:
			return 0, 0, manifest, badArchiveError{err: fmt.Errorf("read tar archive: %w", err)}
		}

		if protocol.IsSiteManifestArchiveEntry(header) {
			parsed, err := protocol.ParseSiteManifest(tr, header.Size)
			if err != nil {
				return 0, 0, manifest, badArchiveError{err: err}
			}
			manifest = parsed
			continue
		}

		rec, n, err := h.acceptArchiveEntry(ctx, tr, header, upload.SiteSHA, upload.Version, files, policy)
		if err != nil {
			return 0, 0, manifest, err
		}
		if rec == nil {
			continue
		}

		upload.Files = append(upload.Files, *rec)
		files++
		bytes += n
	}
}

func (h *handler) acceptArchiveEntry(
	ctx context.Context,
	body io.Reader,
	header *tar.Header,
	siteSHA string,
	version, files int64,
	policy UploadPolicy,
) (*UploadFileRecord, int64, error) {
	if err := protocol.ValidateArchivePath(header.Name); err != nil {
		return nil, 0, badArchiveError{err: err}
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return nil, 0, nil
	case tar.TypeReg, tar.TypeRegA:
		return h.acceptRegularFile(ctx, body, header, siteSHA, version, files, policy)
	default:
		return nil, 0, badArchiveError{err: fmt.Errorf("unsupported archive entry type for %s", header.Name)}
	}
}

func (h *handler) acceptRegularFile(
	ctx context.Context,
	body io.Reader,
	header *tar.Header,
	siteSHA string,
	version, files int64,
	policy UploadPolicy,
) (*UploadFileRecord, int64, error) {
	if policy.MaxUploadFiles.Value > 0 && files >= policy.MaxUploadFiles.Value {
		return nil, 0, uploadLimitError{err: fmt.Errorf("upload exceeds maximum file count: %d", policy.MaxUploadFiles.Value)}
	}

	path, err := sanitizeServingPath(header.Name)
	if err != nil {
		return nil, 0, err
	}

	result, err := h.store.AcceptFile(ctx, StoredFile{
		SiteSHA: siteSHA, Version: version, RelativePath: path,
		Mode: header.Mode, Size: header.Size, Body: body,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("accept file %s: %w", header.Name, err)
	}

	return &UploadFileRecord{
		RelativePath: path,
		BlobPath:     result.BlobPath,
		FileSHA:      result.FileSHA,
		Bytes:        result.Bytes,
	}, result.Bytes, nil
}

func siteFromHost(host string) string {
	return sites.NameFromHost(host)
}

func canonicalSiteName(value string) (string, error) {
	return sites.CanonicalName(value)
}

func requestedRelativePath(urlPath string) (string, bool) {
	return sites.RequestedRelativePath(urlPath)
}

func shouldTryDirectoryIndex(urlPath string, relativePath string, wantsIndex bool) bool {
	return sites.ShouldTryDirectoryIndex(urlPath, relativePath, wantsIndex)
}

func directoryRedirectPath(r *http.Request, prefix string, urlPath string) string {
	return sites.DirectoryRedirectPath(r, prefix, urlPath)
}

func siteAndPathFromServePath(urlPath string) (string, string, bool) {
	return sites.SiteAndPathFromServePath(urlPath)
}

func siteFromDeletePath(urlPath string) (string, bool) {
	return sites.SiteFromDeletePath(urlPath)
}

func siteFromSuffixedSitePath(urlPath string, suffix string) (string, bool) {
	return sites.SiteFromSuffixedSitePath(urlPath, suffix)
}

func sha256Hex(value string) string {
	return sites.HashName(value)
}

func sanitizeServingPath(name string) (string, error) {
	out, err := protocol.SanitizeServingPath(name)
	if err != nil {
		return "", badArchiveError{err: err}
	}
	return out, nil
}

type badArchiveError struct {
	err error
}

func (e badArchiveError) Error() string {
	return e.err.Error()
}

func (e badArchiveError) Unwrap() error {
	return e.err
}

type uploadLimitError struct {
	err error
}

func (e uploadLimitError) Error() string {
	return e.err.Error()
}

func (e uploadLimitError) Unwrap() error {
	return e.err
}
