package admin2

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	adminsecret "quack/internal/admin2/secret"
	"quack/internal/adminui"
	"quack/internal/domain"
	"quack/internal/protocol"
)

//go:embed assets/dist/*
var assetFS embed.FS

type SessionRepository interface {
	FindAdminSession(ctx context.Context, token string) (domain.AdminUser, bool, error)
}

type Handler struct {
	sessions SessionRepository
	secrets  *adminsecret.Handler
}

type Options struct {
	Sessions SessionRepository
	Secrets  adminsecret.SecretStore
}

func New(opts Options) Handler {
	return Handler{
		sessions: opts.Sessions,
		secrets:  adminsecret.NewHandler(adminsecret.NewService(opts.Secrets)),
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	assets, err := fs.Sub(assetFS, "assets/dist")
	if err != nil {
		panic(err)
	}
	mux.Handle("/admin2/assets/", http.StripPrefix("/admin2/assets/", http.FileServer(http.FS(assets))))
	mux.Handle("/admin2", h.requireAdmin(http.HandlerFunc(h.secrets.Page)))
	mux.Handle("/admin2/", h.requireAdmin(http.HandlerFunc(h.rootPage)))
	adminsecret.RegisterRoutes(mux, h.secrets, h.requireAdmin)
}

func (h Handler) rootPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin2/" {
		http.NotFound(w, r)
		return
	}
	h.secrets.Page(w, r)
}

func (h Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOriginAdminRequest(r) {
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
		ctx := adminsecret.ContextWithAdminUser(r.Context(), adminsecret.AdminUser{
			Username: user.Username,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
