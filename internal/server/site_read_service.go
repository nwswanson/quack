package server

import "quack/internal/sites"

type SiteReadService = sites.SiteReadService
type ServeSiteFileStatus = sites.ServeSiteFileStatus
type ServeSiteFileDecision = sites.ServeSiteFileDecision

const (
	ServeSiteFileFound             = sites.ServeSiteFileFound
	ServeSiteFileDirectoryRedirect = sites.ServeSiteFileDirectoryRedirect
	ServeSiteFileEmptyIndex        = sites.ServeSiteFileEmptyIndex
	ServeSiteFileNotFound          = sites.ServeSiteFileNotFound
	ServeSiteFileSuspended         = sites.ServeSiteFileSuspended
)

func NewSiteReadService(hot HotDataReader) SiteReadService {
	return sites.NewSiteReadService(hot)
}
