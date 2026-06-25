package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"quack/internal/adminui"
	"quack/internal/cache"
	"quack/internal/controlapi"
	"quack/internal/domain"
	"quack/internal/logbuffer"
	"quack/internal/publichttp"
	"quack/internal/publishing"
	"quack/internal/releases"
	appruntime "quack/internal/runtime"
	"quack/internal/runtimehttp"
	appsecrets "quack/internal/secrets"
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
	AllowUnauthenticated       bool
	MemoryDirectory            string
	RuntimeHTTPClientAllowSelf bool
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
	logs := logbuffer.New(logbuffer.DefaultCapacity)
	initialSettings, settingsErr := db.GetServerSettings(context.Background())
	if settingsErr != nil {
		slog.Warn("load server settings failed", "error", settingsErr)
	} else if initialSettings.LogBufferCount > 0 {
		logs.SetCapacity(int(initialSettings.LogBufferCount))
	}
	source := cache.NewPassthroughHotDataReader(db)
	//hot := hotdata.NewMemoryHotDataReader(source, hotdata.MemoryHotDataReaderOptions{})
	hot := cache.NewOtterHotDataReader(source, cache.OtterHotDataReaderOptions{})
	read := sites.NewSiteReadService(hot)
	write := sites.NewSiteWriteService(db, hot, hot)
	uploadService := uploads.NewService(db, store, read, write)
	publishingService := publishing.NewService(uploadService)
	releaseService := releases.NewService(db, hot)
	secretService := appsecrets.NewService(db)
	if opts.MemoryDirectory != "" {
		settings, err := db.GetServerSettings(context.Background())
		if err != nil {
			slog.Warn("load memory persistence settings failed", "error", err)
		} else if err := ApplyRuntimeSettings(settings, opts.MemoryDirectory); err != nil {
			slog.Warn("apply memory persistence settings failed", "error", err)
		}
	}

	var metricsDB metricsRepository
	if repo, ok := db.(metricsRepository); ok {
		metricsDB = repo
	}

	controlapi.New(controlapi.Options{
		Token:                token,
		AllowUnauthenticated: opts.AllowUnauthenticated,
		Store:                store,
		Publishing:           publishingService,
		Read:                 read,
		Write:                write,
		Users:                db,
		Releases:             releaseService,
		Logs:                 logs,
		Secrets:              secretService,
	}).Register(adminMux)

	publicMux := http.NewServeMux()
	staticHandler := statichttp.New(store, read)
	starlarkExecutor, err := appruntime.NewStarlarkExecutor(appruntime.ScriptLoaderFunc(func(ctx context.Context, objectKey string) (io.ReadCloser, error) {
		return store.OpenBlob(ctx, objectKey)
	}), appruntime.ResourceLimits{})
	if err != nil {
		starlarkExecutor = nil
	} else {
		starlarkExecutor.SetLogBuffer(logs)
		starlarkExecutor.SetHTTPClientPolicy(hot, hot, opts.RuntimeHTTPClientAllowSelf)
		starlarkExecutor.SetSecretStore(secretService)
	}
	metrics := newPrometheusMetrics(metricsDB, runtimehttp.Handler{})
	runtimeService := appruntime.NewService(appruntime.ServiceOptions{
		Repository:      hot,
		Policies:        hot,
		Executor:        starlarkExecutor,
		Settings:        hot,
		Metrics:         metrics,
		EnableExecution: true,
	})
	runtimeHandler := runtimehttp.New(runtimeService, runtimehttp.WithSettings(hot), runtimehttp.WithLogBuffer(logs))
	metrics.runtime = prometheusRuntimeStats{runtime: runtimeHandler}

	adminui.New(adminui.Options{
		Users:       db,
		Sessions:    db,
		Releases:    releaseService,
		Store:       store,
		Read:        read,
		Write:       write,
		Stats:       runtimeStatsReader{runtime: runtimeHandler},
		SetLogLevel: SetLogLevel,
		ApplySettings: func(settings domain.ServerSettings) error {
			logs.SetCapacity(int(settings.LogBufferCount))
			return ApplyRuntimeSettings(settings, opts.MemoryDirectory)
		},
		Logs:    logs,
		Secrets: secretService,
	}).Register(adminMux)
	adminMux.HandleFunc("/metrics", metrics.handle)

	publichttp.New(
		staticHandler,
		publichttp.WithHostResolver(sites.SettingsHostResolver{Settings: hot}),
		publichttp.WithRoutes(publichttp.ReleaseRouteReader{Releases: releaseService, Policies: hot}),
		publichttp.WithRuntime(runtimeHandler),
	).Register(publicMux)

	return Servers{
		Admin: &http.Server{
			Addr:    adminAddr,
			Handler: requestLoggerWithMetricsAndLogs(adminMux, "admin", metrics, logs),
		},
		Public: &http.Server{
			Addr:    publicAddr,
			Handler: requestLoggerWithMetricsAndLogs(publicMux, "public", metrics, logs),
		},
	}
}
