package server

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"quack/internal/protocol"
)

const adminSessionCookieName = "quack_admin_session"

var adminTemplates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

type handler struct {
	token          string
	store          Storage
	db             Database
	maxUploadBytes int64
	maxUploadFiles int64
}

func (h *handler) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleAdminLoginPage)
	mux.HandleFunc("/login", h.handleAdminLogin)
	mux.HandleFunc("/logout", h.handleAdminLogout)
	mux.HandleFunc("/users", h.handleAdminCreateUser)
	mux.HandleFunc("/settings", h.handleAdminSettings)
	mux.HandleFunc(protocol.LoginCheckPath, h.handleLoginCheck)
	mux.HandleFunc(protocol.UploadArchivePath, h.handleUploadArchive)
	mux.HandleFunc(protocol.DeleteSitePathPrefix, h.handleDeleteSite)
}

func (h *handler) siteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/serve", h.handleServeDisabled)
	mux.HandleFunc("/serve/", h.handleServeDisabled)
	mux.HandleFunc("/", h.handleServeFile)
}

func (h *handler) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	user, loggedIn, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data := adminPageData{User: user}
	if loggedIn {
		data, err = h.adminPageData(r, user)
		if err != nil {
			slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	h.renderAdminPage(w, r, data)
}

func (h *handler) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAdminPage(w, r, adminPageData{Error: "Unable to read login form."})
		return
	}

	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	user, ok, err := h.db.AuthenticateAdmin(r.Context(), username, password)
	if err != nil {
		slog.ErrorContext(r.Context(), "admin login failed", "username", username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		slog.WarnContext(r.Context(), "admin login rejected", "username", username)
		h.renderAdminPage(w, r, adminPageData{Error: "Invalid username or password."})
		return
	}

	token, err := h.db.CreateAdminSession(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "create admin session failed", "username", user.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	http.SetCookie(w, adminSessionCookie(r, token, 86400))
	slog.WarnContext(r.Context(), "admin login accepted", "username", user.Username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err == nil && cookie.Value != "" {
		if err := h.db.DeleteAdminSession(r.Context(), cookie.Value); err != nil {
			slog.ErrorContext(r.Context(), "delete admin session failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	http.SetCookie(w, adminSessionCookie(r, "", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAdminPageWithMessage(w, r, user, "Unable to read user form.", "")
		return
	}
	created, err := h.db.CreateUser(r.Context(), r.Form.Get("username"), r.Form.Get("admin_priv"))
	if err != nil {
		slog.WarnContext(r.Context(), "create admin user failed", "username", r.Form.Get("username"), "error", err)
		h.renderAdminPageWithMessage(w, r, user, err.Error(), "")
		return
	}
	slog.WarnContext(r.Context(), "admin user created", "created_username", created.User.Username, "created_by", user.Username)
	data, err := h.adminPageData(r, user)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.CreatedUser = created
	data.Message = "User created."
	h.renderAdminPage(w, r, data)
}

func (h *handler) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAdminPageWithMessage(w, r, user, "Unable to read settings form.", "")
		return
	}
	settings, err := parseServerSettingsForm(r)
	if err != nil {
		h.renderAdminPageWithMessage(w, r, user, err.Error(), "")
		return
	}
	if err := h.db.SaveServerSettings(r.Context(), settings); err != nil {
		slog.ErrorContext(r.Context(), "save server settings failed", "username", user.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	slog.WarnContext(r.Context(), "server settings updated", "username", user.Username, "max_upload_bytes", settings.MaxUploadBytes, "max_upload_files", settings.MaxUploadFiles)
	h.renderAdminPageWithMessage(w, r, user, "", "Settings saved.")
}

type adminPageData struct {
	User        AdminUser
	Error       string
	Message     string
	Sites       []PublishedSite
	Settings    ServerSettings
	CreatedUser CreatedUser
}

func (d adminPageData) LoggedIn() bool {
	return d.User.ID > 0
}

func (d adminPageData) IsAdmin() bool {
	return d.User.IsAdmin()
}

func (d adminPageData) HasCreatedUser() bool {
	return d.CreatedUser.User.ID > 0
}

func (h *handler) adminPageData(r *http.Request, user AdminUser) (adminPageData, error) {
	sites, err := h.db.ListUserSites(r.Context(), user.ID)
	if err != nil {
		return adminPageData{}, err
	}
	settings, err := h.db.GetServerSettings(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	return adminPageData{
		User:     user,
		Sites:    sites,
		Settings: settings,
	}, nil
}

func (h *handler) renderAdminPageWithMessage(w http.ResponseWriter, r *http.Request, user AdminUser, errorMessage string, message string) {
	data, err := h.adminPageData(r, user)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Error = errorMessage
	data.Message = message
	h.renderAdminPage(w, r, data)
}

func (h *handler) renderAdminPage(w http.ResponseWriter, r *http.Request, data adminPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := adminTemplates.ExecuteTemplate(w, "admin.html", data); err != nil {
		slog.ErrorContext(r.Context(), "render admin page failed", "error", err)
	}
}

func (h *handler) currentAdminUser(r *http.Request) (AdminUser, bool, error) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || cookie.Value == "" {
		return AdminUser{}, false, nil
	}
	return h.db.FindAdminSession(r.Context(), cookie.Value)
}

func adminSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	}
}

func (h *handler) handleLoginCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLoginCheckError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !authorized(r, h.token) {
		writeLoginCheckError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, protocol.LoginCheckResponse{OK: true})
}

func (h *handler) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !authorized(r, h.token) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	site, ok := siteFromDeletePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "site is required")
		return
	}
	siteSHA := sha256Hex(site)
	deleted, err := h.db.DeleteSite(r.Context(), site, siteSHA)
	if err != nil {
		slog.ErrorContext(r.Context(), "delete site metadata failed", "site", site, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if deleted {
		if err := h.store.DeleteSite(r.Context(), siteSHA); err != nil {
			slog.ErrorContext(r.Context(), "delete site blobs failed", "site", site, "site_sha", siteSHA, "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	slog.WarnContext(r.Context(), "site delete completed", "site", site, "deleted", deleted)
	writeJSON(w, http.StatusOK, protocol.DeleteSiteResponse{
		OK:      true,
		Site:    site,
		Deleted: deleted,
	})
}

func (h *handler) handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site := siteFromHost(r.Host)
	if site == "" {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, r.URL.Path, "")
}

func (h *handler) handleServeExplicitSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site, filePath, ok := siteAndPathFromServePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, filePath, "/serve/"+url.PathEscape(site))
}

func (h *handler) handleServeDisabled(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (h *handler) serveSiteFile(w http.ResponseWriter, r *http.Request, site string, urlPath string, redirectPrefix string) {
	relativePath, wantsIndex := requestedRelativePath(urlPath)
	file, ok, err := h.db.FindCurrentFile(r.Context(), site, relativePath)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup file failed", "site", site, "path", relativePath, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if ok {
		h.serveBlob(w, r, site, relativePath, file)
		return
	}

	if shouldTryDirectoryIndex(urlPath, relativePath, wantsIndex) {
		indexPath := path.Join(relativePath, "index.html")
		_, ok, err := h.db.FindCurrentFile(r.Context(), site, indexPath)
		if err != nil {
			slog.ErrorContext(r.Context(), "lookup directory index failed", "site", site, "path", indexPath, "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if ok {
			http.Redirect(w, r, directoryRedirectPath(r, redirectPrefix, urlPath), http.StatusMovedPermanently)
			return
		}
	}

	if wantsIndex {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.NotFound(w, r)
}

func (h *handler) serveBlob(w http.ResponseWriter, r *http.Request, site string, relativePath string, file UploadFileRecord) {
	blob, err := h.store.OpenBlob(r.Context(), file.BlobPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(r.Context(), "blob missing for current file", "site", site, "path", relativePath, "blob_path", file.BlobPath)
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(r.Context(), "open blob failed", "site", site, "path", relativePath, "blob_path", file.BlobPath, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer blob.Close()

	http.ServeContent(w, r, relativePath, time.Time{}, blob)
}

func (h *handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
	site, user, settings, ok := h.validUploadRequest(w, r)
	if !ok {
		return
	}

	resp, err := h.uploadArchive(r, site, user, settings)
	if err != nil {
		h.writeUploadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) validUploadRequest(w http.ResponseWriter, r *http.Request) (string, AdminUser, ServerSettings, bool) {
	var user AdminUser
	settings := ServerSettings{MaxUploadBytes: h.maxUploadBytes, MaxUploadFiles: h.maxUploadFiles}
	switch {
	case r.Method != http.MethodPost:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return "", user, settings, false
	}
	var ok bool
	var err error
	user, ok, err = h.authorizedUploadUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "upload authorization lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return "", user, settings, false
	}
	switch {
	case !ok:
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case r.Header.Get("Content-Type") != protocol.ContentTypeTar:
		writeError(w, http.StatusBadRequest, "content type must be application/x-tar")
	case strings.TrimSpace(r.Header.Get(protocol.HeaderSite)) == "":
		writeError(w, http.StatusBadRequest, "site is required")
	default:
		settings, err = h.db.GetServerSettings(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "load upload settings failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return "", user, settings, false
		}
		if settings.MaxUploadBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, settings.MaxUploadBytes)
		}
		return strings.TrimSpace(r.Header.Get(protocol.HeaderSite)), user, settings, true
	}
	return "", user, settings, false
}

func (h *handler) writeUploadError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	var limitErr uploadLimitError
	var badRequest badArchiveError

	switch {
	case errors.As(err, &maxBytesErr):
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds maximum size")
	case errors.As(err, &limitErr):
		writeError(w, http.StatusRequestEntityTooLarge, limitErr.Error())
	case errors.As(err, &badRequest):
		writeError(w, http.StatusBadRequest, badRequest.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h *handler) authorizedUploadUser(r *http.Request) (AdminUser, bool, error) {
	if authorized(r, h.token) {
		return AdminUser{}, true, nil
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return AdminUser{}, false, nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return AdminUser{}, false, nil
	}
	return h.db.FindUserByToken(r.Context(), token)
}

func parseServerSettingsForm(r *http.Request) (ServerSettings, error) {
	maxUploadBytes, err := parseNonNegativeInt64(r.Form.Get("max_upload_bytes"), "max upload bytes")
	if err != nil {
		return ServerSettings{}, err
	}
	maxUploadFiles, err := parseNonNegativeInt64(r.Form.Get("max_upload_files"), "max upload files")
	if err != nil {
		return ServerSettings{}, err
	}
	return ServerSettings{
		MaxUploadBytes: maxUploadBytes,
		MaxUploadFiles: maxUploadFiles,
	}, nil
}

func parseNonNegativeInt64(value string, label string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", label)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be >= 0", label)
	}
	return n, nil
}

func (h *handler) uploadArchive(r *http.Request, site string, user AdminUser, settings ServerSettings) (protocol.UploadArchiveResponse, error) {
	ctx := r.Context()
	siteSHA := sha256Hex(site)

	upload, err := h.db.BeginUpload(ctx, site, siteSHA)
	if err != nil {
		return protocol.UploadArchiveResponse{}, fmt.Errorf("begin upload: %w", err)
	}
	slog.WarnContext(ctx, "upload started", "site", upload.Site, "version", upload.Version)

	files, bytes, err := h.acceptArchive(r, &upload, settings)
	if err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		slog.WarnContext(ctx, "upload failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, err
	}

	if err := h.db.FinishUpload(ctx, upload); err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		slog.ErrorContext(ctx, "finish upload metadata failed", "site", upload.Site, "version", upload.Version, "error", err)
		return protocol.UploadArchiveResponse{}, fmt.Errorf("finish upload metadata: %w", err)
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

func (h *handler) acceptArchive(r *http.Request, upload *UploadRecord, settings ServerSettings) (int64, int64, error) {
	ctx := r.Context()
	tr := tar.NewReader(r.Body)

	var files, bytes int64
	for {
		header, err := tr.Next()
		switch {
		case errors.Is(err, io.EOF):
			return files, bytes, nil
		case err != nil:
			return 0, 0, badArchiveError{err: fmt.Errorf("read tar archive: %w", err)}
		}

		rec, n, err := h.acceptArchiveEntry(ctx, tr, header, upload.SiteSHA, upload.Version, files, settings)
		if err != nil {
			return 0, 0, err
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
	settings ServerSettings,
) (*UploadFileRecord, int64, error) {
	if err := validateArchivePath(header.Name); err != nil {
		return nil, 0, err
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return nil, 0, nil
	case tar.TypeReg, tar.TypeRegA:
		return h.acceptRegularFile(ctx, body, header, siteSHA, version, files, settings)
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
	settings ServerSettings,
) (*UploadFileRecord, int64, error) {
	if settings.MaxUploadFiles > 0 && files >= settings.MaxUploadFiles {
		return nil, 0, uploadLimitError{err: fmt.Errorf("upload exceeds maximum file count: %d", settings.MaxUploadFiles)}
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

func validateArchivePath(name string) error {
	if name == "" {
		return badArchiveError{err: errors.New("archive path is empty")}
	}
	if strings.HasPrefix(name, "/") {
		return badArchiveError{err: fmt.Errorf("archive path must be relative: %s", name)}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return badArchiveError{err: fmt.Errorf("archive path cannot contain ..: %s", name)}
		}
	}
	if clean := path.Clean(name); clean == "." {
		return badArchiveError{err: fmt.Errorf("archive path cannot contain ..: %s", name)}
	}
	return nil
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
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	return host
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
	return site, site != ""
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sanitizeServingPath(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if err := validateArchivePath(clean); err != nil {
		return "", err
	}

	parts := strings.Split(clean, "/")
	for i, part := range parts {
		parts[i] = sanitizePathPart(part)
	}
	return strings.Join(parts, "/"), nil
}

func sanitizePathPart(part string) string {
	var b strings.Builder
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
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

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.UploadArchiveResponse{
		OK:    false,
		Error: message,
	})
}

func writeLoginCheckError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.LoginCheckResponse{
		OK:    false,
		Error: message,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
