package server

import (
	"quack/internal/adminui"
	"quack/internal/controlapi"
	"quack/internal/hotdata"
	"quack/internal/releases"
	"quack/internal/sites"
	"quack/internal/uploads"
)

type Database interface {
	uploads.UploadRepository
	hotdata.Source
	sites.SiteWriteRepository
	adminui.UserRepository
	adminui.SessionRepository
	controlapi.UserRepository
	releases.Repository
	Close() error
}
