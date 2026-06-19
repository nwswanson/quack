package adminui

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"quack/internal/access"
	"quack/internal/domain"
	"quack/internal/protocol"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
)

const SessionCookieName = "quack_admin_session"

//go:embed templates/*.html
var templateFS embed.FS

var adminTemplates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

type UserRepository interface {
	AuthenticateAdmin(ctx context.Context, username string, password string) (domain.AdminUser, bool, error)
	CreateUser(ctx context.Context, username string, adminPriv string) (domain.CreatedUser, error)
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error)
}

type SessionRepository interface {
	CreateAdminSession(ctx context.Context, userID int64) (string, error)
	FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error)
	DeleteAdminSession(ctx context.Context, token string) error
}

type Handler struct {
	users       UserRepository
	sessions    SessionRepository
	read        sites.SiteReadService
	write       sites.SiteWriteService
	setLogLevel func(string) error
}

type Options struct {
	Users       UserRepository
	Sessions    SessionRepository
	Read        sites.SiteReadService
	Write       sites.SiteWriteService
	SetLogLevel func(string) error
}

func New(opts Options) Handler {
	setLogLevel := opts.SetLogLevel
	if setLogLevel == nil {
		setLogLevel = func(string) error { return nil }
	}
	return Handler{
		users:       opts.Users,
		sessions:    opts.Sessions,
		read:        opts.Read,
		write:       opts.Write,
		setLogLevel: setLogLevel,
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleAdminLoginPage)
	mux.HandleFunc("/login", h.handleAdminLogin)
	mux.HandleFunc("/logout", h.handleAdminLogout)
	mux.HandleFunc("/users", h.handleAdminCreateUser)
	mux.HandleFunc("/settings", h.handleAdminSettings)
	mux.HandleFunc("/policy", h.handleAdminPolicy)
}

func (h Handler) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
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

func (h Handler) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
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

func (h Handler) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	cookie, err := r.Cookie(SessionCookieName)
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

func (h Handler) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
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
	if !ok || !access.Can(user, "users.create") {
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

func (h Handler) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
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
	if !ok || !access.Can(user, "server.settings.edit") {
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
	if err := h.setLogLevel(settings.LogLevel); err != nil {
		slog.ErrorContext(r.Context(), "apply log level failed", "username", user.Username, "log_level", settings.LogLevel, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	slog.WarnContext(r.Context(), "server settings updated", "username", user.Username, "max_upload_bytes", settings.MaxUploadBytes, "max_upload_files", settings.MaxUploadFiles, "max_retained_versions", settings.MaxRetainedVersions, "log_level", settings.LogLevel)
	redirectAdminMessage(w, r, "message", "Settings saved.")
}

func (h Handler) handleAdminPolicy(w http.ResponseWriter, r *http.Request) {
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
	if !ok || !access.Can(user, "policy.edit") {
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
	if err := h.write.SavePolicy(r.Context(), domain.PolicyRecord{
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
	User        domain.AdminUser
	Error       string
	Message     string
	Sites       []domain.PublishedSite
	Settings    domain.ServerSettings
	Policy      domain.PolicyRecord
	CreatedUser domain.CreatedUser
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

func (h Handler) adminPageData(r *http.Request, user domain.AdminUser) (adminPageData, error) {
	siteList, err := h.users.ListPublishedSites(r.Context(), user.ID, user.IsAdmin())
	if err != nil {
		return adminPageData{}, err
	}
	settings, err := h.read.ServerSettings(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	for i := range siteList {
		decision, err := h.read.CurrentSiteRuntime(r.Context(), siteList[i].Site)
		if err != nil {
			return adminPageData{}, err
		}
		siteList[i].RuntimeStatus = decision.Status
		if siteList[i].RuntimeStatus == "" {
			siteList[i].RuntimeStatus = domain.SiteRuntimeActive
		}
		siteList[i].PolicyReason = decision.Reason
	}
	policy, err := h.read.SystemDatabasePolicy(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	return adminPageData{
		User:     user,
		Sites:    siteList,
		Settings: settings,
		Policy:   policy,
	}, nil
}

func (h Handler) renderAdminPageWithMessage(w http.ResponseWriter, r *http.Request, user domain.AdminUser, errorMessage string, message string) {
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

func (h Handler) renderAdminPage(w http.ResponseWriter, r *http.Request, data adminPageData) {
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

func (h Handler) currentAdminUser(r *http.Request) (domain.AdminUser, bool, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return domain.AdminUser{}, false, nil
	}
	return h.sessions.FindAdminSession(r.Context(), cookie.Value)
}

func (h Handler) requireAdminSameOrigin(w http.ResponseWriter, r *http.Request) bool {
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
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	}
}

func parseServerSettingsForm(r *http.Request) (domain.ServerSettings, error) {
	maxUploadBytes, err := parseNonNegativeInt64(r.Form.Get("max_upload_bytes"), "max upload bytes")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	maxUploadFiles, err := parseNonNegativeInt64(r.Form.Get("max_upload_files"), "max upload files")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	maxRetainedVersions, err := parseNonNegativeInt64(r.Form.Get("max_retained_versions"), "max retained versions")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	logLevel := appsettings.ParseLogLevel(r.Form.Get("log_level"))
	if strings.TrimSpace(r.Form.Get("log_level")) == "" {
		logLevel = "warn"
	}
	if logLevel == "" {
		return domain.ServerSettings{}, fmt.Errorf("log level must be debug, info, warn, or error")
	}
	return domain.ServerSettings{
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

func normalizeAdminHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			value = parsed.Host
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, ".")
}
