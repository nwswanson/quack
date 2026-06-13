package protocol

const UploadArchivePath = "/v1/uploads/archive"
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
