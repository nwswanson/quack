package protocol

import (
	"net/url"
	"strconv"
	"strings"
)

const UploadArchivePath = "/v1/uploads/archive"
const SitesPath = "/v1/sites"
const SettingsDefaultSitePath = "/v1/settings/default-site"
const LogsPath = "/v1/logs"
const SecretsPath = "/v1/secrets"
const DeleteSitePathPrefix = "/v1/sites/"
const SiteRevisionPathSuffix = "/revisions"
const SiteRollbackPathSuffix = "/rollback"
const SiteUnpublishPathSuffix = "/unpublish"
const SitePublishPathSuffix = "/publish"
const LoginCheckPath = "/v1/login/check"
const ContentTypeTar = "application/x-tar"
const HeaderSite = "X-Quack-Site"

type Operation interface {
	OperationName() string
}

type UploadArchiveRequest struct {
	Site string
}

func (UploadArchiveRequest) OperationName() string { return "upload archive" }

type DeleteSiteRequest struct {
	Site string
}

func (DeleteSiteRequest) OperationName() string { return "delete site" }

type SiteActionRequest struct {
	Site string
}

func (SiteActionRequest) OperationName() string { return "site action" }

type SetDefaultSiteRequest struct {
	DefaultSite string `json:"default_site"`
}

func (SetDefaultSiteRequest) OperationName() string { return "set default site" }

type ListSitesRequest struct {
	Username   string
	IncludeAll bool
}

func (ListSitesRequest) OperationName() string { return "list sites" }

type LoginCheckRequest struct{}

func (LoginCheckRequest) OperationName() string { return "login check" }

type LogsRequest struct {
	Site          string
	IncludeAll    bool
	IncludeSystem bool
	Limit         int
	Follow        bool
}

func (LogsRequest) OperationName() string { return "logs" }

type SetSecretRequest struct {
	Site  string `json:"site"`
	Scope string `json:"scope"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (SetSecretRequest) OperationName() string { return "set secret" }

type ListSecretsRequest struct {
	Site string
}

func (ListSecretsRequest) OperationName() string { return "list secrets" }

type DeleteSecretRequest struct {
	Site  string
	Scope string
	Name  string
}

func (DeleteSecretRequest) OperationName() string { return "delete secret" }

type Endpoint interface {
	UploadArchive(UploadArchiveRequest) (UploadArchiveResponse, error)
	DeleteSite(DeleteSiteRequest) (DeleteSiteResponse, error)
	UnpublishSite(SiteActionRequest) (UnpublishSiteResponse, error)
	PublishSite(SiteActionRequest) (PublishSiteResponse, error)
	ListSites(ListSitesRequest) (ListSitesResponse, error)
	ListRevisions(SiteActionRequest) (ListRevisionsResponse, error)
	RollbackSite(SiteActionRequest) (RollbackSiteResponse, error)
	SetDefaultSite(SetDefaultSiteRequest) (SetDefaultSiteResponse, error)
	CheckLogin(LoginCheckRequest) (LoginCheckResponse, error)
}

func UploadArchiveURL(baseURL string) string {
	return JoinURL(baseURL, UploadArchivePath)
}

func SitesURL(baseURL string, req ListSitesRequest) (string, error) {
	target, err := url.Parse(JoinURL(baseURL, SitesPath))
	if err != nil {
		return "", err
	}
	query := target.Query()
	if req.IncludeAll {
		query.Set("all", "true")
	}
	if username := strings.TrimSpace(req.Username); username != "" {
		query.Set("user", username)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func SettingsDefaultSiteURL(baseURL string) string {
	return JoinURL(baseURL, SettingsDefaultSitePath)
}

func SiteURL(baseURL string, site string) string {
	return JoinURL(baseURL, SitePath(site))
}

func SiteRevisionsURL(baseURL string, site string) string {
	return JoinURL(baseURL, SiteRevisionsPath(site))
}

func SiteRollbackURL(baseURL string, site string) string {
	return JoinURL(baseURL, SiteRollbackPath(site))
}

func SiteUnpublishURL(baseURL string, site string) string {
	return JoinURL(baseURL, SiteUnpublishPath(site))
}

func SitePublishURL(baseURL string, site string) string {
	return JoinURL(baseURL, SitePublishPath(site))
}

func LoginCheckURL(baseURL string) string {
	return JoinURL(baseURL, LoginCheckPath)
}

func LogsURL(baseURL string, req LogsRequest) (string, error) {
	target, err := url.Parse(JoinURL(baseURL, LogsPath))
	if err != nil {
		return "", err
	}
	query := target.Query()
	if req.Site != "" {
		query.Set("site", req.Site)
	}
	if req.IncludeAll {
		query.Set("all", "true")
	}
	if req.IncludeSystem {
		query.Set("system", "true")
	}
	if req.Limit > 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Follow {
		query.Set("follow", "true")
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func SecretsURL(baseURL string, site string) (string, error) {
	target, err := url.Parse(JoinURL(baseURL, SecretsPath))
	if err != nil {
		return "", err
	}
	query := target.Query()
	if site != "" {
		query.Set("site", site)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func JoinURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}

func SitePath(site string) string {
	return DeleteSitePathPrefix + url.PathEscape(site)
}

func SiteRevisionsPath(site string) string {
	return SitePath(site) + SiteRevisionPathSuffix
}

func SiteRollbackPath(site string) string {
	return SitePath(site) + SiteRollbackPathSuffix
}

func SiteUnpublishPath(site string) string {
	return SitePath(site) + SiteUnpublishPathSuffix
}

func SitePublishPath(site string) string {
	return SitePath(site) + SitePublishPathSuffix
}

type UploadArchiveResponse struct {
	OK      bool   `json:"ok"`
	Site    string `json:"site,omitempty"`
	Version int64  `json:"version,omitempty"`
	Files   int64  `json:"files,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (r *UploadArchiveResponse) SetError(message string) { r.Error = message }
func (r UploadArchiveResponse) ErrorMessage() string     { return r.Error }

type DeleteSiteResponse struct {
	OK      bool   `json:"ok"`
	Site    string `json:"site,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (r *DeleteSiteResponse) SetError(message string) { r.Error = message }
func (r DeleteSiteResponse) ErrorMessage() string     { return r.Error }

type UnpublishSiteResponse struct {
	OK          bool   `json:"ok"`
	Site        string `json:"site,omitempty"`
	Unpublished bool   `json:"unpublished"`
	LiveState   string `json:"live_state,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (r *UnpublishSiteResponse) SetError(message string) { r.Error = message }
func (r UnpublishSiteResponse) ErrorMessage() string     { return r.Error }

type PublishSiteResponse struct {
	OK        bool   `json:"ok"`
	Site      string `json:"site,omitempty"`
	Published bool   `json:"published"`
	LiveState string `json:"live_state,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (r *PublishSiteResponse) SetError(message string) { r.Error = message }
func (r PublishSiteResponse) ErrorMessage() string     { return r.Error }

type SiteSummary struct {
	Site           string `json:"site"`
	SiteSHA        string `json:"site_sha,omitempty"`
	PublishedBy    string `json:"published_by,omitempty"`
	CurrentVersion int64  `json:"current_version"`
	VersionCount   int64  `json:"version_count"`
	FileCount      int64  `json:"file_count"`
	ByteCount      int64  `json:"byte_count"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	LiveState      string `json:"live_state,omitempty"`
	// RuntimeStatus is the compatibility wire name for internal serving status.
	RuntimeStatus string `json:"runtime_status,omitempty"`
	PolicyReason  string `json:"policy_reason,omitempty"`
}

type ListSitesResponse struct {
	OK    bool          `json:"ok"`
	Sites []SiteSummary `json:"sites,omitempty"`
	Error string        `json:"error,omitempty"`
}

func (r *ListSitesResponse) SetError(message string) { r.Error = message }
func (r ListSitesResponse) ErrorMessage() string     { return r.Error }

type SetDefaultSiteResponse struct {
	OK          bool   `json:"ok"`
	DefaultSite string `json:"default_site"`
	Error       string `json:"error,omitempty"`
}

func (r *SetDefaultSiteResponse) SetError(message string) { r.Error = message }
func (r SetDefaultSiteResponse) ErrorMessage() string     { return r.Error }

type SiteRevision struct {
	Version     int64  `json:"version"`
	Current     bool   `json:"current"`
	Files       int64  `json:"files"`
	Bytes       int64  `json:"bytes"`
	PublishedBy string `json:"published_by,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
}

type ListRevisionsResponse struct {
	OK        bool           `json:"ok"`
	Site      string         `json:"site,omitempty"`
	Revisions []SiteRevision `json:"revisions,omitempty"`
	Warning   string         `json:"warning,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func (r *ListRevisionsResponse) SetError(message string) { r.Error = message }
func (r ListRevisionsResponse) ErrorMessage() string     { return r.Error }

type RollbackSiteResponse struct {
	OK              bool   `json:"ok"`
	Site            string `json:"site,omitempty"`
	RolledBack      bool   `json:"rolled_back"`
	PreviousVersion int64  `json:"previous_version,omitempty"`
	CurrentVersion  int64  `json:"current_version,omitempty"`
	Warning         string `json:"warning,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (r *RollbackSiteResponse) SetError(message string) { r.Error = message }
func (r RollbackSiteResponse) ErrorMessage() string     { return r.Error }

type LoginCheckResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (r *LoginCheckResponse) SetError(message string) { r.Error = message }
func (r LoginCheckResponse) ErrorMessage() string     { return r.Error }

type LogEvent struct {
	ID         int64             `json:"id"`
	Time       string            `json:"time"`
	Level      string            `json:"level"`
	Source     string            `json:"source"`
	Site       string            `json:"site,omitempty"`
	Version    int64             `json:"version,omitempty"`
	Route      string            `json:"route,omitempty"`
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`
	StackTrace string            `json:"stack_trace,omitempty"`
}

type LogsResponse struct {
	OK     bool       `json:"ok"`
	Events []LogEvent `json:"events,omitempty"`
	Error  string     `json:"error,omitempty"`
}

func (r *LogsResponse) SetError(message string) { r.Error = message }
func (r LogsResponse) ErrorMessage() string     { return r.Error }

type SecretSummary struct {
	Scope     string `json:"scope"`
	Site      string `json:"site,omitempty"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type SetSecretResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (r *SetSecretResponse) SetError(message string) { r.Error = message }
func (r SetSecretResponse) ErrorMessage() string     { return r.Error }

type ListSecretsResponse struct {
	OK      bool            `json:"ok"`
	Secrets []SecretSummary `json:"secrets,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func (r *ListSecretsResponse) SetError(message string) { r.Error = message }
func (r ListSecretsResponse) ErrorMessage() string     { return r.Error }

type DeleteSecretResponse struct {
	OK      bool   `json:"ok"`
	Deleted bool   `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

func (r *DeleteSecretResponse) SetError(message string) { r.Error = message }
func (r DeleteSecretResponse) ErrorMessage() string     { return r.Error }
