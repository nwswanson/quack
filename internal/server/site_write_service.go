package server

import "quack/internal/sites"

type SiteWriteService = sites.SiteWriteService
type SiteWriteRepository = sites.SiteWriteRepository

func NewSiteWriteService(db SiteWriteRepository, hot HotDataReader, invalidator HotDataInvalidator) SiteWriteService {
	return sites.NewSiteWriteService(db, hot, invalidator)
}

func NewNoopHotDataInvalidator() HotDataInvalidator {
	return sites.NewNoopHotDataInvalidator()
}
