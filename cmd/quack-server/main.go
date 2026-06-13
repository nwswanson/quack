package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"quack/internal/server"
	"quack/internal/sqlitedb"
)

func main() {
	configureLogger()

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
		slog.Error("create blob storage failed", "root", *root, "error", err)
		os.Exit(1)
	}
	db, err := sqlitedb.Open(context.Background(), *databasePath)
	if err != nil {
		slog.Error("open database failed", "database", *databasePath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	opts := server.DefaultOptions()
	opts.MaxUploadBytes = *maxUploadBytes
	opts.MaxUploadFiles = *maxUploadFiles

	srv := server.New(addr, os.Getenv("UPLOAD_TOKEN"), store, db, opts)
	slog.Info("starting quack server",
		"addr", addr,
		"root", *root,
		"database", *databasePath,
		"max_upload_bytes", opts.MaxUploadBytes,
		"max_upload_files", opts.MaxUploadFiles,
		"auth_enabled", os.Getenv("UPLOAD_TOKEN") != "",
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

func configureLogger() {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)
}
