package server

import (
	"net/http"

	"quack/internal/adminui"
	"quack/internal/hotdata"
	"quack/internal/serverapi"
	appsettings "quack/internal/settings"
	"quack/internal/sitehttp"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
	"quack/internal/uploads"
)

const (
	DefaultMaxUploadBytes int64 = appsettings.DefaultMaxUploadBytes
	DefaultMaxUploadFiles int64 = appsettings.DefaultMaxUploadFiles
)

type Options struct {
	AdminHost            string
	AllowUnauthenticated bool
}

func DefaultOptions() Options {
	return Options{}
}

func New(addr string, token string, store appstorage.Storage, db Database, opts Options) *http.Server {
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	source := hotdata.NewPassthroughHotDataReader(db)
	//hot := hotdata.NewMemoryHotDataReader(source, hotdata.MemoryHotDataReaderOptions{})
	hot := hotdata.NewOtterHotDataReader(source, hotdata.OtterHotDataReaderOptions{})
	read := sites.NewSiteReadService(hot)
	write := sites.NewSiteWriteService(db, hot, hot)
	uploadService := uploads.NewService(db, store, read, write)
	adminui.New(adminui.Options{
		Users:       db,
		Sessions:    db,
		Read:        read,
		Write:       write,
		SetLogLevel: SetLogLevel,
	}).Register(mux)
	serverapi.New(serverapi.Options{
		Token:                token,
		AllowUnauthenticated: opts.AllowUnauthenticated,
		Store:                store,
		Uploads:              uploadService,
		Read:                 read,
		Write:                write,
		Users:                db,
		Revisions:            db,
	}).Register(mux)

	siteMux := http.NewServeMux()
	sitehttp.New(store, read).Register(siteMux)

	router := adminHostRouter{
		adminHost: normalizeAdminHost(opts.AdminHost),
		admin:     mux,
		site:      siteMux,
	}

	return &http.Server{
		Addr:    addr,
		Handler: requestLogger(router),
	}
}
