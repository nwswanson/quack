package server

import (
	"quack/internal/sites"
	appstorage "quack/internal/storage"
	appuploads "quack/internal/uploads"
)

type handler struct {
	token                string
	allowUnauthenticated bool

	store   appstorage.Storage
	uploads appuploads.Service
	read    sites.SiteReadService
	write   sites.SiteWriteService

	users     UserRepository
	sessions  SessionRepository
	revisions RevisionRepository
}
