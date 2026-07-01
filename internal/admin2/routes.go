package admin2

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"quack/internal/admin2/secret"
	"quack/internal/adminui"
	"quack/internal/domain"
	"quack/internal/protocol"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

type SessionRepository interface {
	FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error)
}

type Handler struct {
	sessions SessionRepository
	secret   *secret.Service
}

type Options struct {
	Sessions SessionRepository
	Secrets  secret.SecretStore
}

func New(opts Options) Handler {
	return Handler{
		sessions: opts.Sessions,
		secret:   secret.NewService(opts.Secrets),
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/admin2", h.handleSecretStore)
	mux.HandleFunc("/admin2/", h.handleSecretStore)
}

func (h Handler) handleSecretStore(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin2" || r.URL.Path == "/admin2/":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.secretPage(w, r, "", "")
	case r.URL.Path == "/admin2/secret/unlock":
		if r.Method != http.MethodPost {
			protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.unlockSecretStore(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) secretPage(w http.ResponseWriter, r *http.Request, errorMessage string, message string) {
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin2 session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		redirectToLogin(w, r)
		return
	}
	if !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	status, err := h.secret.Status(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "load admin2 secret status failed", "username", user.Username, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	data := secretPageData{
		User:    user,
		Status:  status,
		Error:   strings.TrimSpace(errorMessage),
		Message: strings.TrimSpace(message),
	}
	h.renderPage(w, r, data)
}

func (h Handler) unlockSecretStore(w http.ResponseWriter, r *http.Request) {
	if !sameOriginAdminRequest(r) {
		slog.WarnContext(r.Context(), "admin2 post rejected by origin check",
			"host", r.Host,
			"origin", r.Header.Get("Origin"),
			"referer", r.Header.Get("Referer"),
			"path", r.URL.Path,
		)
		protocol.WriteError(w, http.StatusForbidden, "invalid origin")
		return
	}
	user, ok, err := h.currentAdminUser(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup admin2 session failed", "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		redirectToLogin(w, r)
		return
	}
	if !user.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	form, err := secret.DecodeUnlockForm(r)
	if err != nil {
		status, statusErr := h.secret.Status(r.Context())
		if statusErr != nil {
			slog.ErrorContext(r.Context(), "load admin2 secret status after form error failed", "username", user.Username, "error", statusErr)
		}
		h.renderSecretResponse(w, r, user, status, "Invalid form submission.", "")
		return
	}
	result, err := h.secret.Unlock(r.Context(), form.ToInput())
	status := secret.Status{Configured: result.Configured, Unlocked: result.Unlocked}
	if err != nil {
		msg := "Could not unlock the secret store."
		if errors.Is(err, secret.ErrPassphraseRequired) {
			msg = "Passphrase is required."
		}
		h.renderSecretResponse(w, r, user, status, msg, "")
		return
	}
	h.renderSecretResponse(w, r, user, status, "", "Secret store unlocked.")
}

func (h Handler) renderSecretResponse(w http.ResponseWriter, r *http.Request, user domain.AdminUser, status secret.Status, errorMessage string, message string) {
	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(statusCodeForMessage(errorMessage))
		if err := templates.ExecuteTemplate(w, "secret_panel", secretPageData{
			User:    user,
			Status:  status,
			Error:   errorMessage,
			Message: message,
		}); err != nil {
			slog.ErrorContext(r.Context(), "render admin2 secret panel failed", "error", err)
		}
		return
	}
	h.renderPage(w, r, secretPageData{
		User:    user,
		Status:  status,
		Error:   errorMessage,
		Message: message,
	})
}

func statusCodeForMessage(errorMessage string) int {
	if strings.TrimSpace(errorMessage) == "" {
		return http.StatusOK
	}
	return http.StatusUnprocessableEntity
}

func (h Handler) renderPage(w http.ResponseWriter, r *http.Request, data secretPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := templates.ExecuteTemplate(w, "admin2.html", data); err != nil {
		slog.ErrorContext(r.Context(), "render admin2 page failed", "error", err)
	}
}

func (h Handler) currentAdminUser(r *http.Request) (domain.AdminUser, bool, error) {
	if h.sessions == nil {
		return domain.AdminUser{}, false, nil
	}
	cookie, err := r.Cookie(adminui.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return domain.AdminUser{}, false, nil
	}
	return h.sessions.FindAdminSession(r.Context(), cookie.Value)
}

func isHTMX(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	values := url.Values{}
	values.Set("error", "Sign in to open Admin 2.")
	http.Redirect(w, r, "/?"+values.Encode(), http.StatusSeeOther)
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

func normalizeAdminHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.HasSuffix(host, ":80") {
		return strings.TrimSuffix(host, ":80")
	}
	if strings.HasSuffix(host, ":443") {
		return strings.TrimSuffix(host, ":443")
	}
	return host
}

type secretPageData struct {
	User    domain.AdminUser
	Status  secret.Status
	Error   string
	Message string
}

func (d secretPageData) StoreState() string {
	if d.Status.Unlocked {
		return "Unlocked"
	}
	if d.Status.Configured {
		return "Locked"
	}
	return "Not initialized"
}

func (d secretPageData) StoreStateClass() string {
	if d.Status.Unlocked {
		return "success"
	}
	if d.Status.Configured {
		return "warning"
	}
	return "muted"
}

func (d secretPageData) AccessState() string {
	if d.Status.Unlocked {
		return "Runtime secrets are available."
	}
	if d.Status.Configured {
		return "Unlock required before runtime access."
	}
	return "Create a root key in the current admin before unlocking here."
}
