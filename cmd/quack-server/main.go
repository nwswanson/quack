package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"quack/internal/server"
	"quack/internal/sqlitedb"
)

func main() {
	root := flag.String("root", "", "root directory for blob storage")
	databasePath := flag.String("database", "", "sqlite database file")
	adminHost := flag.String("admin-host", "", "host allowed to serve /v1 admin API routes; accepts a host or URL")
	allowUnauthenticated := flag.Bool("allow-unauthenticated", false, "allow unauthenticated /v1 API access; development only")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root is required")
		os.Exit(1)
	}
	if *databasePath == "" {
		fmt.Fprintln(os.Stderr, "-database is required")
		os.Exit(1)
	}
	uploadToken := os.Getenv("UPLOAD_TOKEN")

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store, err := server.NewBlobStorage(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create blob storage failed: %v\n", err)
		os.Exit(1)
	}
	db, err := sqlitedb.Open(context.Background(), *databasePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.InitializeServerSettings(context.Background(), server.ServerSettings{
		MaxUploadBytes: server.DefaultMaxUploadBytes,
		MaxUploadFiles: server.DefaultMaxUploadFiles,
		LogLevel:       "warn",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "initialize server settings failed: %v\n", err)
		os.Exit(1)
	}
	settings, err := db.GetServerSettings(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load server settings failed: %v\n", err)
		os.Exit(1)
	}
	if err := server.ConfigureLogger(settings.LogLevel, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "log_level: %v\n", err)
		os.Exit(1)
	}
	if *allowUnauthenticated {
		slog.Warn("unauthenticated api access enabled")
	}
	admin, err := db.BootstrapAdmin(context.Background())
	if err != nil {
		slog.Error("bootstrap admin user failed", "error", err)
		os.Exit(1)
	}
	if admin.Created {
		slog.Warn("bootstrap admin user created",
			"username", admin.Username,
			"password", admin.Password,
			"token", admin.Token,
		)
	}

	opts := server.DefaultOptions()
	opts.AdminHost = *adminHost
	opts.AllowUnauthenticated = *allowUnauthenticated

	srv := server.New(addr, uploadToken, store, db, opts)
	slog.Warn("starting quack server",
		"addr", addr,
		"root", *root,
		"database", *databasePath,
		"max_upload_bytes", settings.MaxUploadBytes,
		"max_upload_files", settings.MaxUploadFiles,
		"log_level", settings.LogLevel,
		"admin_host", *adminHost,
		"legacy_upload_token_enabled", uploadToken != "",
		"allow_unauthenticated", *allowUnauthenticated,
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}
