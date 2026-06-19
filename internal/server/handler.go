package server

import (
	appstorage "quack/internal/storage"
	appuploads "quack/internal/uploads"
)

type handler struct {
	token                string
	allowUnauthenticated bool

	store   appstorage.Storage
	uploads appuploads.Service
	read    SiteReadService
	write   SiteWriteService

	users     UserRepository
	sessions  SessionRepository
	revisions RevisionRepository
}
