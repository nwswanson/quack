package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"quack/internal/server"
	"quack/internal/sqlitedb"
)

func main() {
	root := flag.String("root", "", "root directory for blob storage")
	databasePath := flag.String("database", "", "sqlite database file")
	maxUploadBytes := flag.Int64("max-upload-bytes", server.DefaultMaxUploadBytes, "maximum tar upload request size in bytes; 0 disables")
	maxUploadFiles := flag.Int64("max-upload-files", server.DefaultMaxUploadFiles, "maximum regular files accepted per upload; 0 disables")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root is required")
		os.Exit(1)
	}
	if *databasePath == "" {
		fmt.Fprintln(os.Stderr, "-database is required")
		os.Exit(1)
	}
	if *maxUploadBytes < 0 {
		fmt.Fprintln(os.Stderr, "-max-upload-bytes must be >= 0")
		os.Exit(1)
	}
	if *maxUploadFiles < 0 {
		fmt.Fprintln(os.Stderr, "-max-upload-files must be >= 0")
		os.Exit(1)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store, err := server.NewBlobStorage(*root)
	if err != nil {
		log.Fatal(err)
	}
	db, err := sqlitedb.Open(context.Background(), *databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	opts := server.DefaultOptions()
	opts.MaxUploadBytes = *maxUploadBytes
	opts.MaxUploadFiles = *maxUploadFiles

	srv := server.New(addr, os.Getenv("UPLOAD_TOKEN"), store, db, opts)
	log.Printf("listening on %s max_upload_bytes=%d max_upload_files=%d", addr, opts.MaxUploadBytes, opts.MaxUploadFiles)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
