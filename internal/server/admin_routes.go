package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"quack/internal/domain"
	"strconv"
	"strings"

	"quack/internal/protocol"
	appsettings "quack/internal/settings"
)

const adminSessionCookieName = "quack_admin_session"

var adminTemplates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

func (h *handler) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleAdminLoginPage)
	mux.HandleFunc("/login", h.handleAdminLogin)
	mux.HandleFunc("/logout", h.handleAdminLogout)
	mux.HandleFunc("/users", h.handleAdminCreateUser)
	mux.HandleFunc("/settings", h.handleAdminSettings)
	mux.HandleFunc("/policy", h.handleAdminPolicy)
	mux.HandleFunc(protocol.LoginCheckPath, h.handleLoginCheck)
	mux.HandleFunc(protocol.UploadArchivePath, h.handleUploadArchive)
	mux.HandleFunc(protocol.SitesPath, h.handleListSites)
	mux.HandleFunc(protocol.SettingsDefaultSitePath, h.handleSetDefaultSite)
	mux.HandleFunc(protocol.DeleteSitePathPrefix, h.handleDeleteSite)
}

func (h *handler) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	user, loggedIn, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data := adminPageData{User: user}
	if loggedIn {
		data, err = h.adminPageData(r, user)
		if err != nil {
			slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h *handler) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAdminPage(w, r, adminPageData{Error: "Unable to read login form."})
		return
	}

	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	user, ok, err := h.users.AuthenticateAdmin(r.Context(), username, password)
	if err != nil {
		slog.ErrorContext(r.Context(), "admin login failed", "username", username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		slog.WarnContext(r.Context(), "admin login rejected", "username", username)
		h.renderAdminPage(w, r, adminPageData{Error: "Invalid username or password."})
		return
	}

	token, err := h.sessions.CreateAdminSession(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "create admin session failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	http.SetCookie(w, adminSessionCookie(r, token, 86400))
	slog.WarnContext(r.Context(), "admin login accepted", "username", user.Username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err == nil && cookie.Value != "" {
		if err := h.sessions.DeleteAdminSession(r.Context(), cookie.Value); err != nil {
			slog.ErrorContext(r.Context(), "delete admin session failed", "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	http.SetCookie(w, adminSessionCookie(r, "", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !Can(user, "users.create") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAdminPageWithMessage(w, r, user, "Unable to read user form.", "")
		return
	}
	created, err := h.users.CreateUser(r.Context(), r.Form.Get("username"), r.Form.Get("admin_priv"))
	if err != nil {
		slog.WarnContext(r.Context(), "create admin user failed", "username", r.Form.Get("username"), "error", err)
		h.renderAdminPageWithMessage(w, r, user, err.Error(), "")
		return
	}
	slog.WarnContext(r.Context(), "admin user created", "created_username", created.User.Username, "created_by", user.Username)
	data, err := h.adminPageData(r, user)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.CreatedUser = created
	data.Message = "User created."
	h.renderAdminPage(w, r, data)
}

func (h *handler) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !Can(user, "server.settings.edit") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdminMessage(w, r, "error", "Unable to read settings form.")
		return
	}
	settings, err := parseServerSettingsForm(r)
	if err != nil {
		redirectAdminMessage(w, r, "error", err.Error())
		return
	}
	if err := h.write.SaveServerSettings(r.Context(), settings); err != nil {
		slog.ErrorContext(r.Context(), "save server settings failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := SetLogLevel(settings.LogLevel); err != nil {
		slog.ErrorContext(r.Context(), "apply log level failed", "username", user.Username, "log_level", settings.LogLevel, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	slog.WarnContext(r.Context(), "server settings updated", "username", user.Username, "max_upload_bytes", settings.MaxUploadBytes, "max_upload_files", settings.MaxUploadFiles, "max_retained_versions", settings.MaxRetainedVersions, "log_level", settings.LogLevel)
	redirectAdminMessage(w, r, "message", "Settings saved.")
}

func (h *handler) handleAdminPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !Can(user, "policy.edit") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdminMessage(w, r, "error", "Unable to read policy form.")
		return
	}
	mode := strings.TrimSpace(r.Form.Get("database_policy_mode"))
	switch mode {
	case "inherit", "allow", "deny":
	default:
		redirectAdminMessage(w, r, "error", "Database policy must be inherit, allow, or deny.")
		return
	}
	if err := h.write.SavePolicy(r.Context(), PolicyRecord{
		ScopeType: appsettings.ScopeSystem, Key: appsettings.SettingDatabaseFeature, Mode: mode,
		Reason: strings.TrimSpace(r.Form.Get("database_policy_reason")), UpdatedByUserID: user.ID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "save policy failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := h.write.ReconcilePolicyViolations(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "reconcile policy violations failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	redirectAdminMessage(w, r, "message", "Policy saved.")
}

type adminPageData struct {
	User        AdminUser
	Error       string
	Message     string
	Sites       []PublishedSite
	Settings    ServerSettings
	Policy      PolicyRecord
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
	sites, err := h.users.ListPublishedSites(r.Context(), user.ID, user.IsAdmin())
	if err != nil {
		return adminPageData{}, err
	}
	settings, err := h.read.ServerSettings(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	for i := range sites {
		decision, err := h.read.CurrentSiteRuntime(r.Context(), sites[i].Site)
		if err != nil {
			return adminPageData{}, err
		}
		sites[i].RuntimeStatus = decision.Status
		if sites[i].RuntimeStatus == "" {
			sites[i].RuntimeStatus = domain.SiteRuntimeActive
		}
		sites[i].PolicyReason = decision.Reason
	}
	policy, err := h.read.SystemDatabasePolicy(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	return adminPageData{
		User:     user,
		Sites:    sites,
		Settings: settings,
		Policy:   policy,
	}, nil
}

func (h *handler) renderAdminPageWithMessage(w http.ResponseWriter, r *http.Request, user AdminUser, errorMessage string, message string) {
	data, err := h.adminPageData(r, user)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
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

func redirectAdminMessage(w http.ResponseWriter, r *http.Request, key string, message string) {
	values := url.Values{}
	values.Set(key, message)
	http.Redirect(w, r, "/?"+values.Encode(), http.StatusSeeOther)
}

func (h *handler) currentAdminUser(r *http.Request) (AdminUser, bool, error) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || cookie.Value == "" {
		return AdminUser{}, false, nil
	}
	return h.sessions.FindAdminSession(r.Context(), cookie.Value)
}

func (h *handler) requireAdminSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if sameOriginAdminRequest(r) {
		return true
	}
	slog.WarnContext(r.Context(), "admin post rejected by origin check",
		"host", r.Host,
		"origin", r.Header.Get("Origin"),
		"referer", r.Header.Get("Referer"),
		"path", r.URL.Path,
	)
	protocol.WriteError(w, http.StatusForbidden, "invalid origin")
	return false
}

func sameOriginAdminRequest(r *http.Request) bool {
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if source == "" || strings.EqualFold(source, "null") {
		return false
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" {
		return false
	}
	return normalizeAdminHost(parsed.Host) == normalizeAdminHost(r.Host)
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
		protocol.WriteLoginCheckError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ok, err := h.authorizedAPI(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "login check authorization lookup failed", "error", err)
		protocol.WriteLoginCheckError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		protocol.WriteLoginCheckError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	protocol.WriteJSON(w, http.StatusOK, protocol.LoginCheckResponse{OK: true})
}

func (h *handler) handleListSites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok, err := h.authorizedAPIUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "site list authorization lookup failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		protocol.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	query := r.URL.Query()
	includeAll := strings.EqualFold(strings.TrimSpace(query.Get("all")), "true")
	username := strings.TrimSpace(query.Get("user"))

	var sites []PublishedSite
	switch {
	case includeAll:
		if !Can(user, "sites.view_all") {
			protocol.WriteError(w, http.StatusForbidden, "not allowed to list all sites")
			return
		}
		sites, err = h.users.ListPublishedSites(r.Context(), user.ID, true)
	case username != "":
		if !Can(user, "sites.view_all") {
			protocol.WriteError(w, http.StatusForbidden, "not allowed to list another user's sites")
			return
		}
		sites, err = h.users.ListPublishedSitesByUsername(r.Context(), username)
	default:
		sites, err = h.users.ListPublishedSites(r.Context(), user.ID, false)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "list sites failed", "username", user.Username, "target_username", username, "all", includeAll, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	out := protocol.ListSitesResponse{OK: true}
	for _, site := range sites {
		decision, err := h.read.CurrentSiteRuntime(r.Context(), site.Site)
		if err != nil {
			slog.ErrorContext(r.Context(), "resolve site runtime failed", "site", site.Site, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		status := decision.Status
		if status == "" {
			status = domain.SiteRuntimeActive
		}
		out.Sites = append(out.Sites, protocol.SiteSummary{
			Site: site.Site, SiteSHA: site.SiteSHA, PublishedBy: site.PublishedBy,
			CurrentVersion: site.CurrentVersion, VersionCount: site.VersionCount,
			FileCount: site.FileCount, ByteCount: site.ByteCount, UpdatedAt: site.UpdatedAt,
			LiveState:     site.LiveState,
			RuntimeStatus: string(status), PolicyReason: decision.Reason,
		})
	}
	protocol.WriteJSON(w, http.StatusOK, out)
}

func (h *handler) handleSetDefaultSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok, err := h.authorizedAPIUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "default site authorization lookup failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		protocol.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !Can(user, "server.settings.edit") {
		protocol.WriteError(w, http.StatusForbidden, "not allowed to edit server settings")
		return
	}

	var req struct {
		DefaultSite string `json:"default_site"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		protocol.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := h.read.ServerSettings(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "load server settings failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	settings.DefaultSite = strings.TrimSpace(req.DefaultSite)
	if err := h.write.SaveServerSettings(r.Context(), settings); err != nil {
		slog.ErrorContext(r.Context(), "save default site failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	protocol.WriteJSON(w, http.StatusOK, protocol.SetDefaultSiteResponse{
		OK: true, DefaultSite: settings.DefaultSite,
	})
}

func (h *handler) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, protocol.SiteRevisionPathSuffix) || strings.HasSuffix(r.URL.Path, protocol.SiteRollbackPathSuffix) || strings.HasSuffix(r.URL.Path, protocol.SiteUnpublishPathSuffix) || strings.HasSuffix(r.URL.Path, protocol.SitePublishPathSuffix) {
		h.handleSiteRevisionRoutes(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ok, err := h.authorizedAPI(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "delete authorization lookup failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		protocol.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	site, ok := siteFromDeletePath(r.URL.Path)
	if !ok {
		protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
		return
	}
	siteSHA := sha256Hex(site)
	deleted, err := h.write.DeleteSite(r.Context(), site, siteSHA)
	if err != nil {
		slog.ErrorContext(r.Context(), "delete site metadata failed", "site", site, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if deleted {
		if err := h.store.DeleteSite(r.Context(), siteSHA); err != nil {
			slog.ErrorContext(r.Context(), "delete site blobs failed", "site", site, "site_sha", siteSHA, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	slog.WarnContext(r.Context(), "site delete completed", "site", site, "deleted", deleted)
	protocol.WriteJSON(w, http.StatusOK, protocol.DeleteSiteResponse{
		OK:      true,
		Site:    site,
		Deleted: deleted,
	})
}

func (h *handler) handleSiteRevisionRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok, err := h.authorizedAPIUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "revision authorization lookup failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		protocol.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch {
	case strings.HasSuffix(r.URL.Path, protocol.SiteRevisionPathSuffix):
		if r.Method != http.MethodGet {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		site, ok := siteFromSuffixedSitePath(r.URL.Path, protocol.SiteRevisionPathSuffix)
		if !ok {
			protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
			return
		}
		h.handleListRevisions(w, r, user, site)
	case strings.HasSuffix(r.URL.Path, protocol.SiteRollbackPathSuffix):
		if r.Method != http.MethodPost {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		site, ok := siteFromSuffixedSitePath(r.URL.Path, protocol.SiteRollbackPathSuffix)
		if !ok {
			protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
			return
		}
		h.handleRollbackSite(w, r, user, site)
	case strings.HasSuffix(r.URL.Path, protocol.SiteUnpublishPathSuffix):
		if r.Method != http.MethodPost {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		site, ok := siteFromSuffixedSitePath(r.URL.Path, protocol.SiteUnpublishPathSuffix)
		if !ok {
			protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
			return
		}
		h.handleUnpublishSite(w, r, user, site)
	case strings.HasSuffix(r.URL.Path, protocol.SitePublishPathSuffix):
		if r.Method != http.MethodPost {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		site, ok := siteFromSuffixedSitePath(r.URL.Path, protocol.SitePublishPathSuffix)
		if !ok {
			protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
			return
		}
		h.handlePublishSite(w, r, user, site)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) handleListRevisions(w http.ResponseWriter, r *http.Request, user AdminUser, site string) {
	revisions, err := h.revisions.ListSiteRevisions(r.Context(), user, site, sha256Hex(site))
	if err != nil {
		if errors.Is(err, ErrSiteOwnership) {
			protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
			return
		}
		slog.ErrorContext(r.Context(), "list revisions failed", "site", site, "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	resp := protocol.ListRevisionsResponse{OK: true, Site: site}
	var currentVersion int64
	for _, rev := range revisions {
		if rev.Current {
			currentVersion = rev.Version
			break
		}
	}
	older := 0
	for _, rev := range revisions {
		if currentVersion > 0 && rev.Version < currentVersion {
			older++
		}
		resp.Revisions = append(resp.Revisions, protocol.SiteRevision{
			Version: rev.Version, Current: rev.Current, Files: rev.Files, Bytes: rev.Bytes,
			PublishedBy: rev.PublishedBy, CreatedAt: rev.CreatedAt, FinishedAt: rev.FinishedAt,
		})
	}
	if older == 0 {
		resp.Warning = "no older revisions available"
	}
	protocol.WriteJSON(w, http.StatusOK, resp)
}

func (h *handler) handleRollbackSite(w http.ResponseWriter, r *http.Request, user AdminUser, site string) {
	rollback, err := h.write.RollbackSite(r.Context(), user, site, sha256Hex(site))
	if err != nil {
		if errors.Is(err, ErrSiteOwnership) {
			protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
			return
		}
		slog.ErrorContext(r.Context(), "rollback site failed", "site", site, "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if rollback.Warning == "" && !rollback.RolledBack {
		rollback.Warning = "no older revisions available"
	}
	if rollback.RolledBack {
		slog.WarnContext(r.Context(), "site rolled back", "site", site, "username", user.Username, "previous_version", rollback.PreviousVersion, "current_version", rollback.CurrentVersion)
	}
	protocol.WriteJSON(w, http.StatusOK, protocol.RollbackSiteResponse{
		OK: true, Site: site, RolledBack: rollback.RolledBack,
		PreviousVersion: rollback.PreviousVersion, CurrentVersion: rollback.CurrentVersion, Warning: rollback.Warning,
	})
}

func (h *handler) handleUnpublishSite(w http.ResponseWriter, r *http.Request, user AdminUser, site string) {
	out, err := h.write.UnpublishSite(r.Context(), user, site, sha256Hex(site))
	if err != nil {
		if errors.Is(err, ErrSiteOwnership) {
			protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
			return
		}
		slog.ErrorContext(r.Context(), "unpublish site failed", "site", site, "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if out.Unpublished {
		slog.WarnContext(r.Context(), "site unpublished", "site", site, "username", user.Username)
	}
	protocol.WriteJSON(w, http.StatusOK, protocol.UnpublishSiteResponse{
		OK: true, Site: site, Unpublished: out.Unpublished, LiveState: out.LiveState,
	})
}

func (h *handler) handlePublishSite(w http.ResponseWriter, r *http.Request, user AdminUser, site string) {
	out, err := h.write.PublishSite(r.Context(), user, site, sha256Hex(site))
	if err != nil {
		if errors.Is(err, ErrSiteOwnership) {
			protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
			return
		}
		slog.ErrorContext(r.Context(), "publish site failed", "site", site, "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if out.Published {
		slog.WarnContext(r.Context(), "site published", "site", site, "username", user.Username)
	}
	protocol.WriteJSON(w, http.StatusOK, protocol.PublishSiteResponse{
		OK: true, Site: site, Published: out.Published, LiveState: out.LiveState,
	})
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
	maxRetainedVersions, err := parseNonNegativeInt64(r.Form.Get("max_retained_versions"), "max retained versions")
	if err != nil {
		return ServerSettings{}, err
	}
	logLevel := appsettings.ParseLogLevel(r.Form.Get("log_level"))
	if strings.TrimSpace(r.Form.Get("log_level")) == "" {
		logLevel = "warn"
	}
	if logLevel == "" {
		return ServerSettings{}, fmt.Errorf("log level must be debug, info, warn, or error")
	}
	return ServerSettings{
		MaxUploadBytes:      maxUploadBytes,
		MaxUploadFiles:      maxUploadFiles,
		MaxRetainedVersions: maxRetainedVersions,
		DefaultSite:         strings.TrimSpace(r.Form.Get("default_site")),
		LogLevel:            logLevel,
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
