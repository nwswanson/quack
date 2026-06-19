package server

import (
	"net/http"

	"quack/internal/hotdata"
	appsettings "quack/internal/settings"
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
	h := &handler{
		token:                token,
		allowUnauthenticated: opts.AllowUnauthenticated,
		store:                store,
		uploads:              uploadService,
		read:                 read,
		write:                write,
		users:                db,
		sessions:             db,
		revisions:            db,
	}
	h.adminRoutes(mux)

	siteMux := http.NewServeMux()
	h.siteRoutes(siteMux)

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
