package server

import (
	"quack/internal/adminui"
	"quack/internal/hotdata"
	"quack/internal/serverapi"
	"quack/internal/sites"
	"quack/internal/uploads"
)

type Database interface {
	uploads.UploadRepository
	hotdata.Source
	sites.SiteWriteRepository
	adminui.UserRepository
	adminui.SessionRepository
	serverapi.UserRepository
	serverapi.RevisionRepository
	Close() error
}
