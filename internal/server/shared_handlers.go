package server

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"quack/internal/protocol"
)

type handler struct {
	token                string
	store                Storage
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
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	} else if strings.Count(host, ":") == 1 {
		host = strings.Split(host, ":")[0]
	}
	host = strings.Trim(host, ".")
	if host == "" {
		return ""
	}
	host = strings.TrimPrefix(host, "www.")
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	site, err := canonicalSiteName(host)
	if err != nil {
		return ""
	}
	return site
}

func canonicalSiteName(value string) (string, error) {
	site := strings.TrimSpace(strings.ToLower(value))
	site = strings.Trim(site, ".")
	if site == "" {
		return "", fmt.Errorf("site is required")
	}
	if len(site) > 63 {
		return "", fmt.Errorf("site must be 63 characters or fewer")
	}
	if strings.Contains(site, ".") {
		return "", fmt.Errorf("site must be a single DNS label")
	}
	if strings.HasPrefix(site, "-") || strings.HasSuffix(site, "-") {
		return "", fmt.Errorf("site cannot start or end with hyphen")
	}
	switch site {
	case "v1", "serve":
		return "", fmt.Errorf("site name is reserved")
	}
	for _, r := range site {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return "", fmt.Errorf("site must contain only lowercase letters, numbers, and hyphens")
		}
	}
	return site, nil
}

func requestedRelativePath(urlPath string) (string, bool) {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if clean == "/" || clean == "." {
		return "index.html", true
	}
	relative := strings.TrimPrefix(clean, "/")
	if strings.HasSuffix(urlPath, "/") {
		relative = path.Join(relative, "index.html")
	}
	sanitized, err := sanitizeServingPath(relative)
	if err != nil {
		return "index.html", true
	}
	return sanitized, sanitized == "index.html"
}

func shouldTryDirectoryIndex(urlPath string, relativePath string, wantsIndex bool) bool {
	if wantsIndex || strings.HasSuffix(urlPath, "/") {
		return false
	}
	return path.Base(relativePath) != "index.html"
}

func directoryRedirectPath(r *http.Request, prefix string, urlPath string) string {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if clean == "/" {
		clean = ""
	}
	target := prefix + clean + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target
}

func siteAndPathFromServePath(urlPath string) (string, string, bool) {
	rest := strings.TrimPrefix(urlPath, "/serve/")
	if rest == urlPath || rest == "" {
		return "", "", false
	}

	site, filePath, found := strings.Cut(rest, "/")
	if site == "" {
		return "", "", false
	}

	site, err := url.PathUnescape(site)
	if err != nil {
		return "", "", false
	}
	site = strings.TrimSpace(site)
	if site == "" {
		return "", "", false
	}

	if !found || filePath == "" {
		return site, "/", true
	}
	return site, "/" + filePath, true
}

func siteFromDeletePath(urlPath string) (string, bool) {
	site := strings.TrimPrefix(urlPath, protocol.DeleteSitePathPrefix)
	if site == urlPath || site == "" || strings.Contains(site, "/") {
		return "", false
	}
	site, err := url.PathUnescape(site)
	if err != nil {
		return "", false
	}
	site = strings.TrimSpace(site)
	site, err = canonicalSiteName(site)
	if err != nil {
		return "", false
	}
	return site, true
}

func siteFromSuffixedSitePath(urlPath string, suffix string) (string, bool) {
	withoutSuffix, ok := strings.CutSuffix(urlPath, suffix)
	if !ok {
		return "", false
	}
	site := strings.TrimPrefix(withoutSuffix, protocol.DeleteSitePathPrefix)
	if site == withoutSuffix || site == "" || strings.Contains(site, "/") {
		return "", false
	}
	site, err := url.PathUnescape(site)
	if err != nil {
		return "", false
	}
	site = strings.TrimSpace(site)
	site, err = canonicalSiteName(site)
	if err != nil {
		return "", false
	}
	return site, true
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
