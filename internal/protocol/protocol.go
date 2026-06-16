package protocol

const UploadArchivePath = "/v1/uploads/archive"
const SitesPath = "/v1/sites"
const SettingsDefaultSitePath = "/v1/settings/default-site"
const DeleteSitePathPrefix = "/v1/sites/"
const SiteRevisionPathSuffix = "/revisions"
const SiteRollbackPathSuffix = "/rollback"
const SiteUnpublishPathSuffix = "/unpublish"
const SitePublishPathSuffix = "/publish"
const LoginCheckPath = "/v1/login/check"
const ContentTypeTar = "application/x-tar"
const HeaderSite = "X-Quack-Site"

type UploadArchiveResponse struct {
	OK      bool   `json:"ok"`
	Site    string `json:"site,omitempty"`
	Version int64  `json:"version,omitempty"`
	Files   int64  `json:"files,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Error   string `json:"error,omitempty"`
}

type DeleteSiteResponse struct {
	OK      bool   `json:"ok"`
	Site    string `json:"site,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Error   string `json:"error,omitempty"`
}

type UnpublishSiteResponse struct {
	OK          bool   `json:"ok"`
	Site        string `json:"site,omitempty"`
	Unpublished bool   `json:"unpublished"`
	LiveState   string `json:"live_state,omitempty"`
	Error       string `json:"error,omitempty"`
}

type PublishSiteResponse struct {
	OK        bool   `json:"ok"`
	Site      string `json:"site,omitempty"`
	Published bool   `json:"published"`
	LiveState string `json:"live_state,omitempty"`
	Error     string `json:"error,omitempty"`
}

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
	RuntimeStatus  string `json:"runtime_status,omitempty"`
	PolicyReason   string `json:"policy_reason,omitempty"`
}

type ListSitesResponse struct {
	OK    bool          `json:"ok"`
	Sites []SiteSummary `json:"sites,omitempty"`
	Error string        `json:"error,omitempty"`
}

type SetDefaultSiteResponse struct {
	OK          bool   `json:"ok"`
	DefaultSite string `json:"default_site"`
	Error       string `json:"error,omitempty"`
}

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

type RollbackSiteResponse struct {
	OK              bool   `json:"ok"`
	Site            string `json:"site,omitempty"`
	RolledBack      bool   `json:"rolled_back"`
	PreviousVersion int64  `json:"previous_version,omitempty"`
	CurrentVersion  int64  `json:"current_version,omitempty"`
	Warning         string `json:"warning,omitempty"`
	Error           string `json:"error,omitempty"`
}

type LoginCheckResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}
