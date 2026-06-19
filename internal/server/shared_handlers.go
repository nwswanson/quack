package server

import (
	"errors"
	"log/slog"
	"net/http"
	"quack/internal/domain"
	"strings"

	"quack/internal/protocol"
	"quack/internal/sites"
	appuploads "quack/internal/uploads"
)

func (h *handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
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

func (h *handler) validUploadRequest(w http.ResponseWriter, r *http.Request) (string, AdminUser, domain.UploadPolicy, bool) {
	var user AdminUser
	var policy domain.UploadPolicy
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

func (h *handler) writeUploadError(w http.ResponseWriter, err error) {
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
	case errors.Is(err, ErrSiteOwnership):
		protocol.WriteError(w, http.StatusForbidden, "site is owned by another user")
	default:
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h *handler) authorizedUploadUser(r *http.Request) (AdminUser, bool, error) {
	return h.authorizedAPIUser(r)
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
