package server

import (
	"quack/internal/adminui"
	"quack/internal/cache"
	"quack/internal/controlapi"
	"quack/internal/releases"
	"quack/internal/secrets"
	"quack/internal/sites"
	"quack/internal/uploads"
)

type Database interface {
	uploads.UploadRepository
	cache.Source
	sites.SiteWriteRepository
	adminui.UserRepository
	adminui.SessionRepository
	adminui.HardwareRepository
	controlapi.UserRepository
	releases.Repository
	secrets.Repository
	Close() error
}
