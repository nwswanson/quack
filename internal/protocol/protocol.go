package protocol

const UploadArchivePath = "/v1/uploads/archive"
const DeleteSitePathPrefix = "/v1/sites/"
const SiteRevisionPathSuffix = "/revisions"
const SiteRollbackPathSuffix = "/rollback"
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
