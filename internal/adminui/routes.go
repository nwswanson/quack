package adminui

import (
	"context"
	"embed"
	"errors"
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
	"quack/internal/hardware"
	"quack/internal/logbuffer"
	"quack/internal/protocol"
	"quack/internal/releases"
	appsecrets "quack/internal/secrets"
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
	ListUsers(ctx context.Context) ([]domain.AdminUser, error)
}

type SessionRepository interface {
	CreateAdminSession(ctx context.Context, userID int64) (string, error)
	FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error)
	DeleteAdminSession(ctx context.Context, token string) error
}

type SiteStorage interface {
	DeleteSite(ctx context.Context, siteSHA string) error
}

type HardwareRepository interface {
	ListHardwareDevices(ctx context.Context) ([]hardware.AdminDevice, error)
	SaveHardwareDevice(ctx context.Context, device hardware.AdminDevice) error
	DeleteHardwareDevice(ctx context.Context, id string) (bool, error)
}

type HardwareControl interface {
	CancelCapture(ctx context.Context, req hardware.CancelCaptureRequest) (hardware.CancelCaptureResponse, error)
}

type Handler struct {
	users         UserRepository
	sessions      SessionRepository
	releases      releases.Service
	store         SiteStorage
	read          sites.SiteReadService
	write         sites.SiteWriteService
	stats         SiteRuntimeStatsReader
	setLogLevel   func(string) error
	applySettings func(domain.ServerSettings) error
	logs          *logbuffer.Service
	secrets       *appsecrets.Service
	hardware      HardwareRepository
	hardwareCtl   HardwareControl
}

type SiteRuntimeStats struct {
	ActiveWebSockets int64
	MemoryUsedBytes  int64
}

type SiteRuntimeStatsReader interface {
	SiteRuntimeStats(site string) SiteRuntimeStats
}

type Options struct {
	Users         UserRepository
	Sessions      SessionRepository
	Releases      releases.Service
	Store         SiteStorage
	Read          sites.SiteReadService
	Write         sites.SiteWriteService
	Stats         SiteRuntimeStatsReader
	SetLogLevel   func(string) error
	ApplySettings func(domain.ServerSettings) error
	Logs          *logbuffer.Service
	Secrets       *appsecrets.Service
	Hardware      HardwareRepository
	HardwareCtl   HardwareControl
}

func New(opts Options) Handler {
	setLogLevel := opts.SetLogLevel
	if setLogLevel == nil {
		setLogLevel = func(string) error { return nil }
	}
	applySettings := opts.ApplySettings
	if applySettings == nil {
		applySettings = func(domain.ServerSettings) error { return nil }
	}
	return Handler{
		users:         opts.Users,
		sessions:      opts.Sessions,
		releases:      opts.Releases,
		store:         opts.Store,
		read:          opts.Read,
		write:         opts.Write,
		stats:         opts.Stats,
		setLogLevel:   setLogLevel,
		applySettings: applySettings,
		logs:          opts.Logs,
		secrets:       opts.Secrets,
		hardware:      opts.Hardware,
		hardwareCtl:   opts.HardwareCtl,
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleAdminLoginPage)
	mux.HandleFunc("/login", h.handleAdminLogin)
	mux.HandleFunc("/logout", h.handleAdminLogout)
	mux.HandleFunc("/sites/action", h.handleAdminSiteAction)
	mux.HandleFunc("/users", h.handleAdminUsers)
	mux.HandleFunc("/settings", h.handleAdminSettings)
	mux.HandleFunc("/policy", h.handleAdminPolicy)
	mux.HandleFunc("/secrets", h.handleAdminSecrets)
	mux.HandleFunc("/hardware", h.handleAdminHardware)
	mux.HandleFunc("/logs", h.handleAdminLogsPage)
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
		data, err = h.adminPageData(r, user, adminPageSites)
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

func (h Handler) handleAdminSiteAction(w http.ResponseWriter, r *http.Request) {
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
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdminMessage(w, r, "/", "error", "Unable to read site action form.")
		return
	}
	site := strings.TrimSpace(r.Form.Get("site"))
	if site == "" {
		redirectAdminMessage(w, r, "/", "error", "Site is required.")
		return
	}
	siteSHA := sites.HashName(site)
	switch strings.TrimSpace(r.Form.Get("action")) {
	case "delete":
		deleted, err := h.releases.DeleteSite(r.Context(), user, site, siteSHA)
		if err != nil {
			slog.ErrorContext(r.Context(), "delete site metadata failed", "site", site, "username", user.Username, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if deleted && h.store != nil {
			if err := h.store.DeleteSite(r.Context(), siteSHA); err != nil {
				slog.ErrorContext(r.Context(), "delete site blobs failed", "site", site, "site_sha", siteSHA, "username", user.Username, "error", err)
				protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
				return
			}
		}
		slog.WarnContext(r.Context(), "admin site deleted", "site", site, "username", user.Username, "deleted", deleted)
		redirectAdminMessage(w, r, "/", "message", "Site deleted.")
	case "unpublish":
		out, err := h.releases.UnpublishSite(r.Context(), user, site, siteSHA)
		if err != nil {
			slog.ErrorContext(r.Context(), "admin unpublish site failed", "site", site, "username", user.Username, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		slog.WarnContext(r.Context(), "admin site unpublished", "site", site, "username", user.Username, "unpublished", out.Unpublished)
		redirectAdminMessage(w, r, "/", "message", "Site unpublished.")
	case "publish":
		out, err := h.releases.PublishSite(r.Context(), user, site, siteSHA)
		if err != nil {
			slog.ErrorContext(r.Context(), "admin publish site failed", "site", site, "username", user.Username, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		slog.WarnContext(r.Context(), "admin site published", "site", site, "username", user.Username, "published", out.Published)
		redirectAdminMessage(w, r, "/", "message", "Site published.")
	case "rollback":
		version, err := parsePositiveInt64(r.Form.Get("version"), "rollback version")
		if err != nil {
			redirectAdminMessage(w, r, "/", "error", err.Error())
			return
		}
		rollback, err := h.releases.RollbackSiteToVersion(r.Context(), user, site, siteSHA, version)
		if err != nil {
			slog.ErrorContext(r.Context(), "admin rollback site failed", "site", site, "version", version, "username", user.Username, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if rollback.Warning != "" {
			redirectAdminMessage(w, r, "/", "error", rollback.Warning)
			return
		}
		if !rollback.RolledBack {
			redirectAdminMessage(w, r, "/", "message", fmt.Sprintf("Site is already at version %d.", rollback.CurrentVersion))
			return
		}
		slog.WarnContext(r.Context(), "admin site rolled back", "site", site, "username", user.Username, "previous_version", rollback.PreviousVersion, "current_version", rollback.CurrentVersion)
		redirectAdminMessage(w, r, "/", "message", fmt.Sprintf("Site rolled back to version %d.", rollback.CurrentVersion))
	default:
		redirectAdminMessage(w, r, "/", "error", "Site action is invalid.")
	}
}

func (h Handler) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.handleAdminUsersPage(w, r)
		return
	}
	if r.Method != http.MethodPost {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleAdminCreateUser(w, r)
}

func (h Handler) handleAdminUsersPage(w http.ResponseWriter, r *http.Request) {
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
	data, err := h.adminPageData(r, user, adminPageUsers)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin users page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h Handler) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
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
		h.renderAdminPageWithMessage(w, r, user, adminPageUsers, "Unable to read user form.", "")
		return
	}
	created, err := h.users.CreateUser(r.Context(), r.Form.Get("username"), r.Form.Get("admin_priv"))
	if err != nil {
		slog.WarnContext(r.Context(), "create admin user failed", "username", r.Form.Get("username"), "error", err)
		h.renderAdminPageWithMessage(w, r, user, adminPageUsers, err.Error(), "")
		return
	}
	slog.WarnContext(r.Context(), "admin user created", "created_username", created.User.Username, "created_by", user.Username)
	data, err := h.adminPageData(r, user, adminPageUsers)
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
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.handleAdminSettingsPage(w, r)
		return
	}
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
		redirectAdminMessage(w, r, "/settings", "error", "Unable to read settings form.")
		return
	}
	settings, err := parseServerSettingsForm(r)
	if err != nil {
		redirectAdminMessage(w, r, "/settings", "error", err.Error())
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
	if err := h.applySettings(settings); err != nil {
		slog.ErrorContext(r.Context(), "apply server settings failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	slog.WarnContext(r.Context(), "server settings updated", "username", user.Username, "max_upload_bytes", settings.MaxUploadBytes, "max_upload_files", settings.MaxUploadFiles, "max_retained_versions", settings.MaxRetainedVersions, "log_level", settings.LogLevel)
	redirectAdminMessage(w, r, "/settings", "message", "Settings saved.")
}

func (h Handler) handleAdminSettingsPage(w http.ResponseWriter, r *http.Request) {
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
	data, err := h.adminPageData(r, user, adminPageSettings)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin settings page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h Handler) handleAdminPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.handleAdminPolicyPage(w, r)
		return
	}
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
		redirectAdminMessage(w, r, "/policy", "error", "Unable to read policy form.")
		return
	}
	databasePolicy, ok := policyFromForm(r, appsettings.SettingDatabaseFeature, "database_policy", user.ID)
	if !ok {
		redirectAdminMessage(w, r, "/policy", "error", "Database policy must be allow or deny.")
		return
	}
	runtimeHTTPPolicy, ok := policyFromForm(r, appsettings.SettingRuntimeHTTPFeature, "runtime_http_policy", user.ID)
	if !ok {
		redirectAdminMessage(w, r, "/policy", "error", "Starlark HTTP Routes must be allow or deny.")
		return
	}
	runtimeHTTPClientPolicy, ok := policyFromForm(r, appsettings.SettingRuntimeHTTPClientFeature, "runtime_http_client_policy", user.ID)
	if !ok {
		redirectAdminMessage(w, r, "/policy", "error", "Starlark HTTP Module must be allow or deny.")
		return
	}
	runtimeWebSocketPolicy, ok := policyFromForm(r, appsettings.SettingRuntimeWebSocketFeature, "runtime_websocket_policy", user.ID)
	if !ok {
		redirectAdminMessage(w, r, "/policy", "error", "Starlark WebSocket routes must be allow or deny.")
		return
	}
	for _, record := range []domain.PolicyRecord{databasePolicy, runtimeHTTPPolicy, runtimeHTTPClientPolicy, runtimeWebSocketPolicy} {
		if err := h.write.SavePolicy(r.Context(), record); err != nil {
			slog.ErrorContext(r.Context(), "save policy failed", "username", user.Username, "key", record.Key, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	if err := h.write.ReconcilePolicyViolations(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "reconcile policy violations failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	redirectAdminMessage(w, r, "/policy", "message", "Policy saved.")
}

func (h Handler) handleAdminPolicyPage(w http.ResponseWriter, r *http.Request) {
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
	data, err := h.adminPageData(r, user, adminPagePolicy)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin policy page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h Handler) handleAdminSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.handleAdminSecretsPage(w, r)
		return
	}
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
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	if h.secrets == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdminMessage(w, r, "/secrets", "error", "Unable to read secrets form.")
		return
	}
	action := strings.TrimSpace(r.Form.Get("action"))
	switch action {
	case "create":
		passphrase := r.Form.Get("passphrase")
		confirm := r.Form.Get("passphrase_confirm")
		if passphrase != confirm {
			redirectAdminMessage(w, r, "/secrets", "error", "Passwords do not match.")
			return
		}
		if err := h.secrets.Initialize(r.Context(), passphrase, user.ID); err != nil {
			redirectAdminMessage(w, r, "/secrets", "error", err.Error())
			return
		}
		redirectAdminMessage(w, r, "/secrets", "message", "Secrets root key created and unlocked.")
	case "unlock":
		if err := h.secrets.Unlock(r.Context(), r.Form.Get("passphrase")); err != nil {
			redirectAdminMessage(w, r, "/secrets", "error", err.Error())
			return
		}
		redirectAdminMessage(w, r, "/secrets", "message", "Secrets unlocked.")
	case "reset":
		newPassphrase := r.Form.Get("new_passphrase")
		confirm := r.Form.Get("new_passphrase_confirm")
		if newPassphrase != confirm {
			redirectAdminMessage(w, r, "/secrets", "error", "New passwords do not match.")
			return
		}
		if err := h.secrets.ResetPassphrase(r.Context(), r.Form.Get("old_passphrase"), newPassphrase, user.ID); err != nil {
			redirectAdminMessage(w, r, "/secrets", "error", err.Error())
			return
		}
		redirectAdminMessage(w, r, "/secrets", "message", "Secrets password reset.")
	default:
		redirectAdminMessage(w, r, "/secrets", "error", "Secret action is invalid.")
	}
}

func (h Handler) handleAdminSecretsPage(w http.ResponseWriter, r *http.Request) {
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !user.IsAdmin() || h.secrets == nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.adminPageData(r, user, adminPageSecrets)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin secrets page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h Handler) handleAdminHardware(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.handleAdminHardwarePage(w, r)
	case http.MethodPost:
		h.handleAdminHardwareSave(w, r)
	default:
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h Handler) handleAdminHardwarePage(w http.ResponseWriter, r *http.Request) {
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	data, err := h.adminPageData(r, user, adminPageHardware)
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin hardware page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func (h Handler) handleAdminHardwareSave(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminSameOrigin(w, r) {
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok || !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	if h.hardware == nil {
		redirectAdminMessage(w, r, "/hardware", "error", "Hardware management is not configured.")
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdminMessage(w, r, "/hardware", "error", "Unable to read hardware form.")
		return
	}
	switch strings.TrimSpace(r.Form.Get("action")) {
	case "delete":
		deleted, err := h.hardware.DeleteHardwareDevice(r.Context(), r.Form.Get("id"))
		if err != nil {
			slog.ErrorContext(r.Context(), "delete hardware device failed", "username", user.Username, "device", r.Form.Get("id"), "error", err)
			redirectAdminMessage(w, r, "/hardware", "error", err.Error())
			return
		}
		if deleted {
			redirectAdminMessage(w, r, "/hardware", "message", "Hardware device deleted.")
		} else {
			redirectAdminMessage(w, r, "/hardware", "message", "Hardware device was already absent.")
		}
	case "unstuck":
		if h.hardwareCtl == nil {
			redirectAdminMessage(w, r, "/hardware", "error", "Hardware control is not configured.")
			return
		}
		resp, err := h.hardwareCtl.CancelCapture(r.Context(), hardware.CancelCaptureRequest{CameraID: r.Form.Get("id")})
		if err != nil {
			slog.ErrorContext(r.Context(), "unstuck hardware capture failed", "username", user.Username, "device", r.Form.Get("id"), "error", err)
			redirectAdminMessage(w, r, "/hardware", "error", err.Error())
			return
		}
		if resp.Cancelled {
			redirectAdminMessage(w, r, "/hardware", "message", "Hardware read cancelled.")
		} else {
			redirectAdminMessage(w, r, "/hardware", "message", "No active hardware read was found.")
		}
	default:
		device, err := hardwareDeviceFromForm(r)
		if err != nil {
			redirectAdminMessage(w, r, "/hardware", "error", err.Error())
			return
		}
		if strings.TrimSpace(device.OriginalID) != "" {
			if strings.TrimSpace(device.ID) == "" {
				device.ID = device.OriginalID
			}
			devices, err := h.hardware.ListHardwareDevices(r.Context())
			if err != nil {
				slog.ErrorContext(r.Context(), "load hardware devices for immutable kind failed", "username", user.Username, "device", device.OriginalID, "error", err)
				protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			for _, existing := range devices {
				if existing.ID == strings.TrimSpace(device.OriginalID) {
					device.Kind = existing.Kind
					break
				}
			}
		}
		if strings.TrimSpace(device.OriginalID) != "" && strings.TrimSpace(device.OriginalID) != strings.TrimSpace(device.ID) && strings.TrimSpace(device.Alias) == strings.TrimSpace(device.OriginalID) {
			device.Alias = ""
		}
		if strings.TrimSpace(device.Site) != "" {
			site, err := sites.CanonicalName(device.Site)
			if err != nil {
				redirectAdminMessage(w, r, "/hardware", "error", err.Error())
				return
			}
			siteList, err := h.releases.ListPublishedSites(r.Context(), user.ID, true)
			if err != nil {
				slog.ErrorContext(r.Context(), "load sites for hardware binding failed", "username", user.Username, "error", err)
				protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			if !publishedSiteExists(siteList, site) {
				redirectAdminMessage(w, r, "/hardware", "error", "Selected site does not exist.")
				return
			}
			device.Site = site
		}
		if err := h.hardware.SaveHardwareDevice(r.Context(), device); err != nil {
			slog.WarnContext(r.Context(), "save hardware device failed", "username", user.Username, "device", device.ID, "error", err)
			redirectAdminMessage(w, r, "/hardware", "error", err.Error())
			return
		}
		redirectAdminMessage(w, r, "/hardware", "message", "Hardware device saved.")
	}
}

func hardwareDeviceFromForm(r *http.Request) (hardware.AdminDevice, error) {
	baud, err := parseNonNegativeInt64(r.Form.Get("serial_baud"), "serial baud")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	dataBits, err := parseNonNegativeInt64(r.Form.Get("serial_data_bits"), "serial data bits")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	readTimeoutMillis, err := parseNonNegativeInt64(r.Form.Get("serial_read_timeout_ms"), "serial read timeout")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	requestTimeoutMillis, err := parseNonNegativeInt64(r.Form.Get("serial_request_timeout_ms"), "serial request timeout")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	writeQueueSize, err := parseNonNegativeInt64(r.Form.Get("serial_write_queue_size"), "serial write queue size")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	recentEvents, err := parseNonNegativeInt64(r.Form.Get("serial_recent_events"), "serial recent events")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	reconnectMillis, err := parseNonNegativeInt64(r.Form.Get("serial_reconnect_ms"), "serial reconnect")
	if err != nil {
		return hardware.AdminDevice{}, err
	}
	return hardware.AdminDevice{
		OriginalID: r.Form.Get("original_id"),
		ID:         r.Form.Get("id"),
		Kind:       r.Form.Get("kind"),
		Path:       r.Form.Get("path"),
		Label:      r.Form.Get("label"),
		Site:       r.Form.Get("site"),
		Alias:      r.Form.Get("alias"),
		Serial: hardware.SerialOptions{
			BaudRate:             int(baud),
			DataBits:             int(dataBits),
			Parity:               r.Form.Get("serial_parity"),
			StopBits:             r.Form.Get("serial_stop_bits"),
			ReadTimeoutMillis:    int(readTimeoutMillis),
			RequestTimeoutMillis: int(requestTimeoutMillis),
			WriteQueueSize:       int(writeQueueSize),
			RecentEvents:         int(recentEvents),
			ReconnectMillis:      int(reconnectMillis),
		},
	}, nil
}

func publishedSiteExists(sites []domain.PublishedSite, site string) bool {
	for _, candidate := range sites {
		if candidate.Site == site {
			return true
		}
	}
	return false
}

func (h Handler) handleAdminLogsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := h.adminPageData(r, user, adminPageLogs)
	if err != nil {
		if errors.Is(err, domain.ErrSiteOwnership) {
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(r.Context(), "load admin logs page data failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	h.renderAdminPage(w, r, data)
}

func policyFromForm(r *http.Request, key string, prefix string, userID int64) (domain.PolicyRecord, bool) {
	mode := strings.TrimSpace(r.Form.Get(prefix + "_mode"))
	switch mode {
	case "allow", "deny":
	default:
		return domain.PolicyRecord{}, false
	}
	return domain.PolicyRecord{
		ScopeType:       domain.ScopeSystem,
		Key:             key,
		Mode:            mode,
		Reason:          strings.TrimSpace(r.Form.Get(prefix + "_reason")),
		UpdatedByUserID: userID,
	}, true
}

const (
	adminPageSites    = "sites"
	adminPageUsers    = "users"
	adminPageSettings = "settings"
	adminPagePolicy   = "policy"
	adminPageSecrets  = "secrets"
	adminPageHardware = "hardware"
	adminPageLogs     = "logs"
)

type adminNavItem struct {
	Key   string
	Label string
	Path  string
}

type adminSiteRow struct {
	domain.PublishedSite
	Revisions        []domain.RevisionRecord
	IsDefault        bool
	ActiveWebSockets int64
	MemoryUsedBytes  int64
}

func (s adminSiteRow) DisplayLiveState() string {
	if s.LiveState == "" {
		return "-"
	}
	return s.LiveState
}

func (s adminSiteRow) IsUnpublished() bool {
	return s.LiveState == "unpublished"
}

type adminPageData struct {
	User                    domain.AdminUser
	Page                    string
	Title                   string
	Nav                     []adminNavItem
	Error                   string
	Message                 string
	Sites                   []adminSiteRow
	Users                   []domain.AdminUser
	Settings                domain.ServerSettings
	DatabasePolicy          domain.PolicyRecord
	RuntimeHTTPPolicy       domain.PolicyRecord
	RuntimeHTTPClientPolicy domain.PolicyRecord
	RuntimeWebSocketPolicy  domain.PolicyRecord
	CreatedUser             domain.CreatedUser
	LogSite                 string
	LogEvents               []logbuffer.Event
	SecretsHasRootKey       bool
	SecretsUnlocked         bool
	HardwareDevices         []hardware.AdminDevice
	HardwareSites           []domain.PublishedSite
	HardwareKinds           []hardware.AdminKindInfo
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

func (d adminPageData) ActivePage(page string) bool {
	return d.Page == page
}

func (d adminPageData) AllowedHostsValue() string {
	return appsettings.FormatAllowedHosts(d.Settings.AllowedHosts)
}

func (d adminPageData) HTTPClientAllowedCIDRsValue() string {
	return appsettings.FormatHTTPClientAllowedCIDRs(d.Settings.HTTPClientAllowedCIDRs)
}

func (h Handler) adminPageData(r *http.Request, user domain.AdminUser, page string) (adminPageData, error) {
	data := adminPageData{
		User:          user,
		Page:          page,
		Title:         adminPageTitle(page),
		Nav:           adminNav(user),
		HardwareKinds: hardware.AdminKinds,
	}
	switch page {
	case adminPageSites:
		siteList, err := h.releases.ListPublishedSites(r.Context(), user.ID, user.IsAdmin())
		if err != nil {
			return adminPageData{}, err
		}
		settings, err := h.read.ServerSettings(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		data.Settings = settings
		data.Sites = make([]adminSiteRow, 0, len(siteList))
		for i := range siteList {
			decision, err := h.read.CurrentSiteServingStatus(r.Context(), siteList[i].Site)
			if err != nil {
				return adminPageData{}, err
			}
			siteList[i].ServingStatus = decision.Status
			if siteList[i].ServingStatus == "" {
				siteList[i].ServingStatus = domain.SiteServingActive
			}
			siteList[i].PolicyReason = decision.Reason
			row := adminSiteRow{
				PublishedSite: siteList[i],
				IsDefault:     settings.DefaultSite != "" && siteList[i].Site == settings.DefaultSite,
			}
			if h.stats != nil {
				stats := h.stats.SiteRuntimeStats(siteList[i].Site)
				row.ActiveWebSockets = stats.ActiveWebSockets
				row.MemoryUsedBytes = stats.MemoryUsedBytes
			}
			if user.IsAdmin() {
				revisions, err := h.releases.ListSiteRevisions(r.Context(), user, siteList[i].Site, siteList[i].SiteSHA)
				if err != nil {
					return adminPageData{}, err
				}
				row.Revisions = revisions
			}
			data.Sites = append(data.Sites, row)
		}
	case adminPageUsers:
		users, err := h.users.ListUsers(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		data.Users = users
	case adminPageSettings:
		settings, err := h.read.ServerSettings(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		data.Settings = settings
	case adminPagePolicy:
		databasePolicy, err := h.read.SystemDatabasePolicy(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		runtimeHTTPPolicy, err := h.read.SystemRuntimeHTTPPolicy(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		runtimeHTTPClientPolicy, err := h.read.SystemRuntimeHTTPClientPolicy(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		runtimeWebSocketPolicy, err := h.read.SystemRuntimeWebSocketPolicy(r.Context())
		if err != nil {
			return adminPageData{}, err
		}
		data.DatabasePolicy = databasePolicy
		data.RuntimeHTTPPolicy = runtimeHTTPPolicy
		data.RuntimeHTTPClientPolicy = runtimeHTTPClientPolicy
		data.RuntimeWebSocketPolicy = runtimeWebSocketPolicy
	case adminPageSecrets:
		if h.secrets != nil {
			hasKey, unlocked, err := h.secrets.Status(r.Context())
			if err != nil {
				return adminPageData{}, err
			}
			data.SecretsHasRootKey = hasKey
			data.SecretsUnlocked = unlocked
		}
	case adminPageHardware:
		if h.hardware != nil {
			devices, err := h.hardware.ListHardwareDevices(r.Context())
			if err != nil {
				return adminPageData{}, err
			}
			data.HardwareDevices = devices
		}
		sites, err := h.releases.ListPublishedSites(r.Context(), user.ID, true)
		if err != nil {
			return adminPageData{}, err
		}
		data.HardwareSites = sites
	case adminPageLogs:
		data.LogSite = strings.TrimSpace(r.URL.Query().Get("site"))
		if h.logs == nil {
			break
		}
		if user.IsAdmin() {
			filter := logbuffer.Filter{IncludeSystem: true}
			if data.LogSite != "" {
				filter = logbuffer.Filter{Site: data.LogSite}
			}
			data.LogEvents = h.logs.Tail(filter, 200)
			break
		}
		siteList, err := h.releases.ListPublishedSites(r.Context(), user.ID, false)
		if err != nil {
			return adminPageData{}, err
		}
		owned := map[string]bool{}
		for _, site := range siteList {
			owned[site.Site] = true
		}
		if data.LogSite != "" {
			if !owned[data.LogSite] {
				return adminPageData{}, domain.ErrSiteOwnership
			}
			data.LogEvents = h.logs.Tail(logbuffer.Filter{Site: data.LogSite}, 200)
			break
		}
		for _, event := range h.logs.Tail(logbuffer.Filter{}, 200) {
			if owned[event.Site] {
				data.LogEvents = append(data.LogEvents, event)
			}
		}
	}
	return data, nil
}

func adminPageTitle(page string) string {
	switch page {
	case adminPageUsers:
		return "Users"
	case adminPageSettings:
		return "Server Settings"
	case adminPagePolicy:
		return "Policies"
	case adminPageSecrets:
		return "Secrets"
	case adminPageHardware:
		return "Hardware"
	case adminPageLogs:
		return "Logs"
	default:
		return "Published Sites"
	}
}

func adminNav(user domain.AdminUser) []adminNavItem {
	nav := []adminNavItem{{Key: adminPageSites, Label: "Published Sites", Path: "/"}}
	nav = append(nav, adminNavItem{Key: adminPageLogs, Label: "Logs", Path: "/logs"})
	if user.IsAdmin() {
		nav = append(nav,
			adminNavItem{Key: adminPageUsers, Label: "Users", Path: "/users"},
			adminNavItem{Key: adminPageSettings, Label: "Server Settings", Path: "/settings"},
			adminNavItem{Key: adminPagePolicy, Label: "Policies", Path: "/policy"},
			adminNavItem{Key: adminPageSecrets, Label: "Secrets", Path: "/secrets"},
			adminNavItem{Key: adminPageHardware, Label: "Hardware", Path: "/hardware"},
		)
	}
	return nav
}

func (h Handler) renderAdminPageWithMessage(w http.ResponseWriter, r *http.Request, user domain.AdminUser, page string, errorMessage string, message string) {
	data, err := h.adminPageData(r, user, page)
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

func redirectAdminMessage(w http.ResponseWriter, r *http.Request, path string, key string, message string) {
	values := url.Values{}
	values.Set(key, message)
	http.Redirect(w, r, path+"?"+values.Encode(), http.StatusSeeOther)
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
	maxRuntimeDurationMillis, err := parseNonNegativeInt64(r.Form.Get("max_runtime_duration_millis"), "max runtime duration")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if maxRuntimeDurationMillis == 0 {
		maxRuntimeDurationMillis = parseDefaultInt64(appsettings.SettingRuntimeMaxDurationMillis)
	}
	httpClientMaxBytes, err := parseNonNegativeInt64(r.Form.Get("http_client_max_bytes"), "http client max bytes")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if httpClientMaxBytes == 0 {
		httpClientMaxBytes = parseDefaultInt64(appsettings.SettingRuntimeHTTPClientMaxBytes)
	}
	httpClientMaxTimeoutMS, err := parseNonNegativeInt64(r.Form.Get("http_client_max_timeout_ms"), "http client max timeout")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if httpClientMaxTimeoutMS == 0 {
		httpClientMaxTimeoutMS = parseDefaultInt64(appsettings.SettingRuntimeHTTPClientMaxTimeoutMS)
	}
	httpClientAllowedCIDRs, err := appsettings.ParseHTTPClientAllowedCIDRs(r.Form.Get("http_client_allowed_cidrs"))
	if err != nil {
		return domain.ServerSettings{}, err
	}
	maxWebSocketConnections, err := parseNonNegativeInt64(r.Form.Get("max_websocket_connections"), "max websocket connections")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	maxWebSocketConnectionsPerSite, err := parseNonNegativeInt64(r.Form.Get("max_websocket_connections_per_site"), "max websocket connections per site")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	maxPipesPerSite, err := parseNonNegativeInt64(r.Form.Get("max_pipes_per_site"), "max pipes per site")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if maxPipesPerSite == 0 {
		maxPipesPerSite = parseDefaultInt64(appsettings.SettingRuntimePipesMaxPipesPerSite)
	}
	maxTopicsPerSite, err := parseNonNegativeInt64(r.Form.Get("max_topics_per_site"), "max topics per site")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if maxTopicsPerSite == 0 {
		maxTopicsPerSite = parseDefaultInt64(appsettings.SettingRuntimePipesMaxTopicsPerSite)
	}
	maxRetainedEventsPerSite, err := parseNonNegativeInt64(r.Form.Get("max_retained_events_per_site"), "max retained events per site")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if maxRetainedEventsPerSite == 0 {
		maxRetainedEventsPerSite = parseDefaultInt64(appsettings.SettingRuntimePipesMaxRetainedEventsPerSite)
	}
	maxRetainedBytesPerSite, err := parseNonNegativeInt64(r.Form.Get("max_retained_bytes_per_site"), "max retained bytes per site")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if maxRetainedBytesPerSite == 0 {
		maxRetainedBytesPerSite = parseDefaultInt64(appsettings.SettingRuntimePipesMaxRetainedBytesPerSite)
	}
	memoryPersistenceModeValue := strings.TrimSpace(r.Form.Get("memory_persistence_mode"))
	if memoryPersistenceModeValue == "" {
		memoryPersistenceModeValue = appsettings.Default(appsettings.SettingRuntimeMemoryPersistenceMode)
	}
	memoryPersistenceMode := appsettings.ParseMemoryPersistenceMode(memoryPersistenceModeValue)
	if memoryPersistenceMode == "" {
		return domain.ServerSettings{}, fmt.Errorf("memory persistence mode must be off or snapshot")
	}
	memorySnapshotSave := strings.TrimSpace(r.Form.Get("memory_snapshot_save"))
	if memorySnapshotSave == "" {
		memorySnapshotSave = appsettings.Default(appsettings.SettingRuntimeMemorySnapshotSave)
	}
	if _, err := appsettings.ParseMemorySnapshotSaveRules(memorySnapshotSave); err != nil {
		return domain.ServerSettings{}, err
	}
	memorySnapshotMinIntervalMS, err := parseNonNegativeInt64(r.Form.Get("memory_snapshot_min_interval_ms"), "memory snapshot min interval")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if memorySnapshotMinIntervalMS == 0 {
		memorySnapshotMinIntervalMS = parseDefaultInt64(appsettings.SettingRuntimeMemorySnapshotMinIntervalMS)
	}
	memorySnapshotMaxConcurrency, err := parseNonNegativeInt64(r.Form.Get("memory_snapshot_max_concurrency"), "memory snapshot max concurrency")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if memorySnapshotMaxConcurrency == 0 {
		memorySnapshotMaxConcurrency = parseDefaultInt64(appsettings.SettingRuntimeMemorySnapshotMaxConcurrency)
	}
	memoryShutdownFlushTimeoutMS, err := parseNonNegativeInt64(r.Form.Get("memory_shutdown_flush_timeout_ms"), "memory shutdown flush timeout")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if memoryShutdownFlushTimeoutMS == 0 {
		memoryShutdownFlushTimeoutMS = parseDefaultInt64(appsettings.SettingRuntimeMemoryShutdownFlushTimeoutMS)
	}
	logLevel := appsettings.ParseLogLevel(r.Form.Get("log_level"))
	if strings.TrimSpace(r.Form.Get("log_level")) == "" {
		logLevel = "warn"
	}
	if logLevel == "" {
		return domain.ServerSettings{}, fmt.Errorf("log level must be debug, info, warn, or error")
	}
	logBufferCount, err := parseNonNegativeInt64(r.Form.Get("log_buffer_count"), "log buffer count")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if logBufferCount == 0 {
		logBufferCount = parseDefaultInt64(appsettings.SettingLogBufferCount)
	}
	httpCacheModeValue := strings.TrimSpace(r.Form.Get("http_cache_mode"))
	if httpCacheModeValue == "" {
		httpCacheModeValue = appsettings.Default(appsettings.SettingHTTPCacheMode)
	}
	httpCacheMode := appsettings.ParseHTTPCacheMode(httpCacheModeValue)
	if httpCacheMode == "" {
		return domain.ServerSettings{}, fmt.Errorf("http cache mode must be revalidate, anti_cache, or max_age")
	}
	httpCacheMaxAgeSeconds, err := parseNonNegativeInt64(r.Form.Get("http_cache_max_age_seconds"), "http cache max age seconds")
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if httpCacheMaxAgeSeconds == 0 {
		httpCacheMaxAgeSeconds = parseDefaultInt64(appsettings.SettingHTTPCacheMaxAgeSeconds)
	}
	allowedHosts, err := appsettings.ParseAllowedHosts(r.Form.Get("allowed_hosts"))
	if err != nil {
		return domain.ServerSettings{}, err
	}
	return domain.ServerSettings{
		MaxUploadBytes:                 maxUploadBytes,
		MaxUploadFiles:                 maxUploadFiles,
		MaxRetainedVersions:            maxRetainedVersions,
		MaxRuntimeDurationMillis:       maxRuntimeDurationMillis,
		HTTPClientMaxBytes:             httpClientMaxBytes,
		HTTPClientMaxTimeoutMS:         httpClientMaxTimeoutMS,
		HTTPClientAllowedCIDRs:         httpClientAllowedCIDRs,
		HTTPClientAllowInsecureSSL:     r.Form.Get("http_client_allow_insecure_ssl") == "on",
		MaxWebSocketConnections:        maxWebSocketConnections,
		MaxWebSocketConnectionsPerSite: maxWebSocketConnectionsPerSite,
		MaxPipesPerSite:                maxPipesPerSite,
		MaxTopicsPerSite:               maxTopicsPerSite,
		MaxRetainedEventsPerSite:       maxRetainedEventsPerSite,
		MaxRetainedBytesPerSite:        maxRetainedBytesPerSite,
		HTTPCacheMode:                  httpCacheMode,
		HTTPCacheMaxAgeSeconds:         httpCacheMaxAgeSeconds,
		MemoryPersistenceMode:          memoryPersistenceMode,
		MemorySnapshotSave:             memorySnapshotSave,
		MemorySnapshotMinIntervalMS:    memorySnapshotMinIntervalMS,
		MemorySnapshotMaxConcurrency:   memorySnapshotMaxConcurrency,
		MemoryShutdownFlushTimeoutMS:   memoryShutdownFlushTimeoutMS,
		DefaultSite:                    strings.TrimSpace(r.Form.Get("default_site")),
		AllowedHosts:                   allowedHosts,
		LogLevel:                       logLevel,
		LogBufferCount:                 logBufferCount,
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

func parseDefaultInt64(key string) int64 {
	n, _ := strconv.ParseInt(appsettings.Default(key), 10, 64)
	return n
}

func parsePositiveInt64(value string, label string) (int64, error) {
	n, err := parseNonNegativeInt64(value, label)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s is required", label)
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
