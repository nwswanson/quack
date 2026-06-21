package server

import (
	"context"
	"io"
	"net/http"

	"quack/internal/adminui"
	"quack/internal/cache"
	"quack/internal/controlapi"
	"quack/internal/publichttp"
	"quack/internal/publishing"
	"quack/internal/releases"
	appruntime "quack/internal/runtime"
	"quack/internal/runtimehttp"
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
	source := cache.NewPassthroughHotDataReader(db)
	//hot := hotdata.NewMemoryHotDataReader(source, hotdata.MemoryHotDataReaderOptions{})
	hot := cache.NewOtterHotDataReader(source, cache.OtterHotDataReaderOptions{})
	read := sites.NewSiteReadService(hot)
	write := sites.NewSiteWriteService(db, hot, hot)
	uploadService := uploads.NewService(db, store, read, write)
	publishingService := publishing.NewService(uploadService)
	releaseService := releases.NewService(db, hot)

	adminui.New(adminui.Options{
		Users:       db,
		Sessions:    db,
		Releases:    releaseService,
		Store:       store,
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
	starlarkExecutor, err := appruntime.NewStarlarkExecutor(appruntime.ScriptLoaderFunc(func(ctx context.Context, objectKey string) (io.ReadCloser, error) {
		return store.OpenBlob(ctx, objectKey)
	}), appruntime.ResourceLimits{})
	if err != nil {
		starlarkExecutor = nil
	}
	runtimeService := appruntime.NewService(appruntime.ServiceOptions{
		Repository:      hot,
		Policies:        hot,
		Executor:        starlarkExecutor,
		EnableExecution: true,
	})
	publichttp.New(
		staticHandler,
		publichttp.WithHostResolver(sites.SettingsHostResolver{Settings: hot}),
		publichttp.WithRoutes(publichttp.ReleaseRouteReader{Releases: releaseService, Policies: hot}),
		publichttp.WithRuntime(runtimehttp.New(runtimeService)),
	).Register(publicMux)

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
