package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"quack/internal/access"
	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
	appuploads "quack/internal/uploads"
)

type UserRepository interface {
	FindUserByToken(ctx context.Context, token string) (domain.AdminUser, bool, error)
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error)
	ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error)
}

type RevisionRepository interface {
	ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error)
}

type Handler struct {
	token                string
	allowUnauthenticated bool

	store     appstorage.Storage
	uploads   appuploads.Service
	read      sites.SiteReadService
	write     sites.SiteWriteService
	users     UserRepository
	revisions RevisionRepository
}

type Options struct {
	Token                string
	AllowUnauthenticated bool
	Store                appstorage.Storage
	Uploads              appuploads.Service
	Read                 sites.SiteReadService
	Write                sites.SiteWriteService
	Users                UserRepository
	Revisions            RevisionRepository
}

func New(opts Options) Handler {
	return Handler{
		token:                opts.Token,
		allowUnauthenticated: opts.AllowUnauthenticated,
		store:                opts.Store,
		uploads:              opts.Uploads,
		read:                 opts.Read,
		write:                opts.Write,
		users:                opts.Users,
		revisions:            opts.Revisions,
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc(protocol.LoginCheckPath, h.handleLoginCheck)
	mux.HandleFunc(protocol.UploadArchivePath, h.handleUploadArchive)
	mux.HandleFunc(protocol.SitesPath, h.handleListSites)
	mux.HandleFunc(protocol.SettingsDefaultSitePath, h.handleSetDefaultSite)
	mux.HandleFunc(protocol.DeleteSitePathPrefix, h.handleDeleteSite)
}

func (h Handler) handleLoginCheck(w http.ResponseWriter, r *http.Request) {
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

func (h Handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
	site, user, policy, ok := h.validUploadRequest(w, r)
	if !ok {
		return
	}

	resp, err := h.uploads.UploadArchive(r.Context(), appuploads.Request{
		Site:   site,
		User:   user,
		Policy: policy,
		Body:   r.Body,
	})
	if err != nil {
		h.writeUploadError(w, err)
		return
	}

	protocol.WriteJSON(w, http.StatusOK, resp)
}

func (h Handler) validUploadRequest(w http.ResponseWriter, r *http.Request) (string, domain.AdminUser, domain.UploadPolicy, bool) {
	var user domain.AdminUser
	var policy domain.UploadPolicy
	switch {
	case r.Method != http.MethodPost:
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return "", user, policy, false
	}
	var ok bool
	var err error
	user, ok, err = h.authorizedAPIUser(r)
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
		site, err := sites.CanonicalName(r.Header.Get(protocol.HeaderSite))
		if err != nil {
			protocol.WriteError(w, http.StatusBadRequest, err.Error())
			return "", user, policy, false
		}
		policy, err = h.read.UploadPolicy(r.Context(), user, site)
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

func (h Handler) writeUploadError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	var limitErr appuploads.LimitError
	var badRequest appuploads.BadArchiveError
	var forbidden sites.ForbiddenPolicyError

	switch {
	case errors.As(err, &maxBytesErr):
		protocol.WriteError(w, http.StatusRequestEntityTooLarge, "upload exceeds maximum size")
	case errors.As(err, &limitErr):
		protocol.WriteError(w, http.StatusRequestEntityTooLarge, limitErr.Error())
	case errors.As(err, &badRequest):
		protocol.WriteError(w, http.StatusBadRequest, badRequest.Error())
	case errors.As(err, &forbidden):
		protocol.WriteError(w, http.StatusForbidden, forbidden.Error())
	case errors.Is(err, domain.ErrSiteOwnership):
		protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
	default:
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h Handler) handleListSites(w http.ResponseWriter, r *http.Request) {
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

	var siteList []domain.PublishedSite
	switch {
	case includeAll:
		if !access.Can(user, "sites.view_all") {
			protocol.WriteError(w, http.StatusForbidden, "not allowed to list all sites")
			return
		}
		siteList, err = h.users.ListPublishedSites(r.Context(), user.ID, true)
	case username != "":
		if !access.Can(user, "sites.view_all") {
			protocol.WriteError(w, http.StatusForbidden, "not allowed to list another user's sites")
			return
		}
		siteList, err = h.users.ListPublishedSitesByUsername(r.Context(), username)
	default:
		siteList, err = h.users.ListPublishedSites(r.Context(), user.ID, false)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "list sites failed", "username", user.Username, "target_username", username, "all", includeAll, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	out := protocol.ListSitesResponse{OK: true}
	for _, site := range siteList {
		decision, err := h.read.CurrentSiteServingStatus(r.Context(), site.Site)
		if err != nil {
			slog.ErrorContext(r.Context(), "resolve site serving status failed", "site", site.Site, "error", err)
			protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		status := decision.Status
		if status == "" {
			status = domain.SiteServingActive
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

func (h Handler) handleSetDefaultSite(w http.ResponseWriter, r *http.Request) {
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
	if !access.Can(user, "server.settings.edit") {
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

func (h Handler) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
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

	site, ok := sites.SiteFromDeletePath(r.URL.Path)
	if !ok {
		protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
		return
	}
	siteSHA := sites.HashName(site)
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

func (h Handler) handleSiteRevisionRoutes(w http.ResponseWriter, r *http.Request) {
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
		site, ok := sites.SiteFromSuffixedSitePath(r.URL.Path, protocol.SiteRevisionPathSuffix)
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
		site, ok := sites.SiteFromSuffixedSitePath(r.URL.Path, protocol.SiteRollbackPathSuffix)
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
		site, ok := sites.SiteFromSuffixedSitePath(r.URL.Path, protocol.SiteUnpublishPathSuffix)
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
		site, ok := sites.SiteFromSuffixedSitePath(r.URL.Path, protocol.SitePublishPathSuffix)
		if !ok {
			protocol.WriteError(w, http.StatusBadRequest, "site is invalid")
			return
		}
		h.handlePublishSite(w, r, user, site)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) handleListRevisions(w http.ResponseWriter, r *http.Request, user domain.AdminUser, site string) {
	revisions, err := h.revisions.ListSiteRevisions(r.Context(), user, site, sites.HashName(site))
	if err != nil {
		if errors.Is(err, domain.ErrSiteOwnership) {
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

func (h Handler) handleRollbackSite(w http.ResponseWriter, r *http.Request, user domain.AdminUser, site string) {
	rollback, err := h.write.RollbackSite(r.Context(), user, site, sites.HashName(site))
	if err != nil {
		if errors.Is(err, domain.ErrSiteOwnership) {
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

func (h Handler) handleUnpublishSite(w http.ResponseWriter, r *http.Request, user domain.AdminUser, site string) {
	out, err := h.write.UnpublishSite(r.Context(), user, site, sites.HashName(site))
	if err != nil {
		if errors.Is(err, domain.ErrSiteOwnership) {
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

func (h Handler) handlePublishSite(w http.ResponseWriter, r *http.Request, user domain.AdminUser, site string) {
	out, err := h.write.PublishSite(r.Context(), user, site, sites.HashName(site))
	if err != nil {
		if errors.Is(err, domain.ErrSiteOwnership) {
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

func (h Handler) authorizedAPIUser(r *http.Request) (domain.AdminUser, bool, error) {
	requestToken, hasBearerToken := bearerToken(r)
	if h.token != "" && hasBearerToken && requestToken == h.token {
		return domain.AdminUser{}, true, nil
	}
	if hasBearerToken {
		user, ok, err := h.users.FindUserByToken(r.Context(), requestToken)
		if err != nil || ok {
			return user, ok, err
		}
	}
	if h.token == "" && h.allowUnauthenticated {
		return domain.AdminUser{}, true, nil
	}
	if authorized(r, h.token, h.allowUnauthenticated) {
		return domain.AdminUser{}, true, nil
	}
	return domain.AdminUser{}, false, nil
}

func (h Handler) authorizedAPI(r *http.Request) (bool, error) {
	_, ok, err := h.authorizedAPIUser(r)
	return ok, err
}

func authorized(r *http.Request, token string, allowUnauthenticated bool) bool {
	requestToken, ok := bearerToken(r)
	if token != "" && ok && requestToken == token {
		return true
	}
	return token == "" && allowUnauthenticated
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
