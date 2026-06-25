package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"quack/internal/domain"
	"quack/internal/hardware"
	"quack/internal/storage"

	"quack/internal/server"
	"quack/internal/sqlitedb"
)

func main() {
	root := flag.String("root", "", "root directory for blob storage")
	databasePath := flag.String("database", "", "sqlite database file")
	memoryDir := flag.String("memory-dir", "", "directory for runtime memory snapshots")
	allowUnauthenticated := flag.Bool("allow-unauthenticated", false, "allow unauthenticated /v1 API access; development only")
	runtimeHTTPClientAllowSelf := flag.Bool("runtime-http-client-allow-self", false, "allow Starlark HTTP client access to loopback/self addresses; development only")
	hardwarePluginPath := flag.String("hardware-plugin", "", "path to hardware plugin executable; disabled when empty")
	hardwareConfigPath := flag.String("hardware-config", "", "trusted platform hardware config; required when -hardware-plugin is set")
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

	adminAddr := os.Getenv("ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = ":8081"
	}
	publicAddr := os.Getenv("PUBLIC_ADDR")
	if publicAddr == "" {
		publicAddr = ":8080"
	}

	store, err := storage.NewBlobStorage(*root)
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
	if err := db.InitializeServerSettings(context.Background(), domain.ServerSettings{
		MaxUploadBytes:      server.DefaultMaxUploadBytes,
		MaxUploadFiles:      server.DefaultMaxUploadFiles,
		MaxRetainedVersions: 0,
		DefaultSite:         "",
		LogLevel:            "warn",
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
	if *memoryDir == "" {
		*memoryDir = filepath.Join(filepath.Dir(*databasePath), "memory")
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
	opts.AllowUnauthenticated = *allowUnauthenticated
	opts.MemoryDirectory = *memoryDir
	opts.RuntimeHTTPClientAllowSelf = *runtimeHTTPClientAllowSelf
	if *hardwarePluginPath != "" {
		if *hardwareConfigPath == "" {
			fmt.Fprintln(os.Stderr, "-hardware-config is required when -hardware-plugin is set")
			os.Exit(1)
		}
		hardwareConfig, err := hardware.LoadConfigFile(*hardwareConfigPath)
		if err != nil {
			slog.Error("load hardware config failed", "path", *hardwareConfigPath, "error", err)
			os.Exit(1)
		}
		hardwareService, err := hardware.StartPluginClient(context.Background(), *hardwarePluginPath)
		if err != nil {
			slog.Error("start hardware plugin failed", "path", *hardwarePluginPath, "error", err)
			os.Exit(1)
		}
		boundHardwareService, err := hardware.NewBoundService(hardwareService, hardwareConfig)
		if err != nil {
			_ = hardwareService.Close()
			slog.Error("configure hardware bindings failed", "path", *hardwareConfigPath, "error", err)
			os.Exit(1)
		}
		defer boundHardwareService.Close()
		opts.HardwareService = boundHardwareService
	}

	servers := server.New(adminAddr, publicAddr, uploadToken, store, db, opts)
	slog.Warn("starting quack server",
		"admin_addr", adminAddr,
		"public_addr", publicAddr,
		"root", *root,
		"database", *databasePath,
		"memory_dir", *memoryDir,
		"max_upload_bytes", settings.MaxUploadBytes,
		"max_upload_files", settings.MaxUploadFiles,
		"max_retained_versions", settings.MaxRetainedVersions,
		"default_site", settings.DefaultSite,
		"log_level", settings.LogLevel,
		"legacy_upload_token_enabled", uploadToken != "",
		"allow_unauthenticated", *allowUnauthenticated,
		"runtime_http_client_allow_self", *runtimeHTTPClientAllowSelf,
		"hardware_plugin_enabled", *hardwarePluginPath != "",
		"hardware_config_enabled", *hardwareConfigPath != "",
	)

	type serverError struct {
		name string
		err  error
	}
	errCh := make(chan serverError, 2)
	go func() {
		if err := servers.Admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- serverError{name: "admin", err: err}
		}
	}()
	go func() {
		if err := servers.Public.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- serverError{name: "public", err: err}
		}
	}()

	failed := <-errCh
	if failed.err != nil {
		slog.Error("server stopped unexpectedly", "listener", failed.name, "error", failed.err)
		os.Exit(1)
	}
}
