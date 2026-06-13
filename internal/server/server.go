package server

import "net/http"

func New(addr string, token string, store Storage) *http.Server {
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := &handler{
		token: token,
		store: store,
	}
	h.routes(mux)

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}
