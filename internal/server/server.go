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
		adminAddr = ":8080"
	}
	if publicAddr == "" {
		publicAddr = ":8081"
	}

	adminMux := http.NewServeMux()
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
	}).Register(adminMux)

	serverapi.New(serverapi.Options{
		Token:                token,
		AllowUnauthenticated: opts.AllowUnauthenticated,
		Store:                store,
		Uploads:              uploadService,
		Read:                 read,
		Write:                write,
		Users:                db,
		Revisions:            db,
	}).Register(adminMux)

	publicMux := http.NewServeMux()
	sitehttp.New(store, read).Register(publicMux)

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
