package server

import "net/http"

const (
	DefaultMaxUploadBytes int64 = 512 << 20
	DefaultMaxUploadFiles int64 = 10000
)

type Options struct {
	MaxUploadBytes int64
	MaxUploadFiles int64
}

func DefaultOptions() Options {
	return Options{
		MaxUploadBytes: DefaultMaxUploadBytes,
		MaxUploadFiles: DefaultMaxUploadFiles,
	}
}

func New(addr string, token string, store Storage, db Database, opts Options) *http.Server {
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := &handler{
		token:          token,
		store:          store,
		db:             db,
		maxUploadBytes: opts.MaxUploadBytes,
		maxUploadFiles: opts.MaxUploadFiles,
	}
	h.routes(mux)

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}
