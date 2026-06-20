package server

import (
	"net/http"

	"quack/internal/adminui"
	"quack/internal/controlapi"
	"quack/internal/hotdata"
	"quack/internal/publichttp"
	"quack/internal/publishing"
	"quack/internal/releases"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
	"quack/internal/statichttp"
	appstorage "quack/internal/storage"
	"quack/internal/uploads"
)

const (
	DefaultMaxUploadBytes int64 = appsettings.DefaultMaxUploadBytes
	DefaultMaxUploadFiles int64 = appsettings.DefaultMaxUploadFiles
)

type Options struct {
	AllowUnauthenticated bool
}

func DefaultOptions() Options {
	return Options{}
}

type Servers struct {
	Admin  *http.Server
	Public *http.Server
}

func New(adminAddr string, publicAddr string, token string, store appstorage.Storage, db Database, opts Options) Servers {
	if adminAddr == "" {
		adminAddr = ":8081"
	}
	if publicAddr == "" {
		publicAddr = ":8080"
	}

	adminMux := http.NewServeMux()
	source := hotdata.NewPassthroughHotDataReader(db)
	//hot := hotdata.NewMemoryHotDataReader(source, hotdata.MemoryHotDataReaderOptions{})
	hot := hotdata.NewOtterHotDataReader(source, hotdata.OtterHotDataReaderOptions{})
	read := sites.NewSiteReadService(hot)
	write := sites.NewSiteWriteService(db, hot, hot)
	uploadService := uploads.NewService(db, store, read, write)
	publishingService := publishing.NewService(uploadService)
	releaseService := releases.NewService(db, hot)

	adminui.New(adminui.Options{
		Users:       db,
		Sessions:    db,
		Releases:    releaseService,
		Read:        read,
		Write:       write,
		SetLogLevel: SetLogLevel,
	}).Register(adminMux)

	controlapi.New(controlapi.Options{
		Token:                token,
		AllowUnauthenticated: opts.AllowUnauthenticated,
		Store:                store,
		Publishing:           publishingService,
		Read:                 read,
		Write:                write,
		Users:                db,
		Releases:             releaseService,
	}).Register(adminMux)

	publicMux := http.NewServeMux()
	staticHandler := statichttp.New(store, read)
	// Phase 12 TODO: construct the real runtime service here after choosing the
	// executor strategy and pass it through publichttp.WithRuntime. The composition
	// root should be the only place that knows about concrete executors, storage,
	// repositories, loggers, and metrics sinks.
	publichttp.New(staticHandler, publichttp.WithRoutes(publichttp.ReleaseRouteReader{Releases: releaseService, Policies: hot})).Register(publicMux)

	return Servers{
		Admin: &http.Server{
			Addr:    adminAddr,
			Handler: requestLogger(adminMux),
		},
		Public: &http.Server{
			Addr:    publicAddr,
			Handler: requestLogger(publicMux),
		},
	}
}
