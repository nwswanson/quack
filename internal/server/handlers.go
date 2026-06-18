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
	token                string
	store                Storage
	db                   Database
	read                 SiteReadService
	allowUnauthenticated bool
}

func (h *handler) siteReadService() SiteReadService {
	if h.read != nil {
		return h.read
	}
	return NewSiteReadService(h.db, nil)
}

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

func (h *handler) siteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/serve", h.handleServeDisabled)
	mux.HandleFunc("/serve/", h.handleServeDisabled)
	mux.HandleFunc("/", h.handleServeFile)
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
	user, ok, err := h.db.AuthenticateAdmin(r.Context(), username, password)
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

	token, err := h.db.CreateAdminSession(r.Context(), user.ID)
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
		if err := h.db.DeleteAdminSession(r.Context(), cookie.Value); err != nil {
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
	if err := h.db.SaveServerSettings(r.Context(), settings); err != nil {
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
	if err := h.db.SavePolicy(r.Context(), PolicyRecord{
		ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: mode,
		Reason: strings.TrimSpace(r.Form.Get("database_policy_reason")), UpdatedByUserID: user.ID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "save policy failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := h.siteReadService().ReconcilePolicyViolations(r.Context()); err != nil {
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
	sites, err := h.db.ListPublishedSites(r.Context(), user.ID, user.IsAdmin())
	if err != nil {
		return adminPageData{}, err
	}
	settings, err := h.siteReadService().ServerSettings(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	for i := range sites {
		decision, err := h.siteReadService().CurrentSiteRuntime(r.Context(), sites[i].Site)
		if err != nil {
			return adminPageData{}, err
		}
		sites[i].RuntimeStatus = decision.Status
		if sites[i].RuntimeStatus == "" {
			sites[i].RuntimeStatus = SiteRuntimeActive
		}
		sites[i].PolicyReason = decision.Reason
	}
	policies, err := h.db.LoadPolicies(r.Context(), []PolicyScope{{Type: ScopeSystem, ID: ""}})
	if err != nil {
		return adminPageData{}, err
	}
	policy := PolicyRecord{ScopeType: ScopeSystem, Key: SettingDatabaseFeature, Mode: "inherit"}
	for _, p := range policies {
		if p.Key == SettingDatabaseFeature {
			policy = p
			break
		}
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
	return h.db.FindAdminSession(r.Context(), cookie.Value)
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
		sites, err = h.db.ListPublishedSites(r.Context(), user.ID, true)
	case username != "":
		if !Can(user, "sites.view_all") {
			protocol.WriteError(w, http.StatusForbidden, "not allowed to list another user's sites")
			return
		}
		sites, err = h.db.ListPublishedSitesByUsername(r.Context(), username)
	default:
		sites, err = h.db.ListPublishedSites(r.Context(), user.ID, false)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "list sites failed", "username", user.Username, "target_username", username, "all", includeAll, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	out := protocol.ListSitesResponse{OK: true}
	for _, site := range sites {
		decision, err := h.siteReadService().CurrentSiteRuntime(r.Context(), site.Site)
		if err != nil {
			slog.ErrorContext(r.Context(), "resolve site runtime failed", "site", site.Site, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		status := decision.Status
		if status == "" {
			status = SiteRuntimeActive
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
	settings, err := h.siteReadService().ServerSettings(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "load server settings failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	settings.DefaultSite = strings.TrimSpace(req.DefaultSite)
	if err := h.db.SaveServerSettings(r.Context(), settings); err != nil {
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
	deleted, err := h.db.DeleteSite(r.Context(), site, siteSHA)
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
	revisions, err := h.db.ListSiteRevisions(r.Context(), user, site, sha256Hex(site))
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
	rollback, err := h.db.RollbackSite(r.Context(), user, site, sha256Hex(site))
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
	out, err := h.db.UnpublishSite(r.Context(), user, site, sha256Hex(site))
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
	out, err := h.db.PublishSite(r.Context(), user, site, sha256Hex(site))
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

func (h *handler) handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
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
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	settings, err := h.siteReadService().ServerSettings(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "load server settings failed", "site", site, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.serveSiteFileWithFallback(w, r, site, urlPath, redirectPrefix, strings.TrimSpace(settings.DefaultSite), false)
}

func (h *handler) serveSiteFileWithFallback(w http.ResponseWriter, r *http.Request, site string, urlPath string, redirectPrefix string, defaultSite string, usingDefault bool) {
	decision, err := h.siteReadService().CurrentSiteRuntime(r.Context(), site)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve site runtime failed", "site", site, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if decision.Status == SiteRuntimeSuspendedByPolicy {
		protocol.WriteError(w, http.StatusForbidden, "site suspended by administrator policy")
		return
	}

	relativePath, wantsIndex := requestedRelativePath(urlPath)
	file, ok, siteExists, err := h.siteReadService().CurrentSiteFile(r.Context(), site, relativePath)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup file failed", "site", site, "path", relativePath, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if ok {
		h.serveBlob(w, r, site, relativePath, file)
		return
	}
	if !siteExists && !usingDefault && defaultSite != "" && defaultSite != site {
		h.serveSiteFileWithFallback(w, r, defaultSite, urlPath, redirectPrefix, defaultSite, true)
		return
	}

	if shouldTryDirectoryIndex(urlPath, relativePath, wantsIndex) {
		indexPath := path.Join(relativePath, "index.html")
		_, ok, _, err := h.siteReadService().CurrentSiteFile(r.Context(), site, indexPath)
		if err != nil {
			slog.ErrorContext(r.Context(), "lookup directory index failed", "site", site, "path", indexPath, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
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
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer blob.Close()

	http.ServeContent(w, r, relativePath, time.Time{}, blob)
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
	logLevel := parseLogLevelName(r.Form.Get("log_level"))
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
	if err := h.db.SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, ManifestSettings(manifest)); err != nil {
		if markErr := h.db.FailUpload(ctx, upload, err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "mark upload failed", "site", upload.Site, "version", upload.Version, "upload_error", err, "error", markErr)
		}
		return protocol.UploadArchiveResponse{}, fmt.Errorf("save upload settings: %w", err)
	}

	if err := h.db.FinishUpload(ctx, upload); err != nil {
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
