package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"quack/internal/server"
)

func main() {
	root := flag.String("root", "", "root directory for blob storage")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root is required")
		os.Exit(1)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store, err := server.NewBlobStorage(*root, server.StubSaveUpload)
	if err != nil {
		log.Fatal(err)
	}

	srv := server.New(addr, os.Getenv("UPLOAD_TOKEN"), store)
	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
