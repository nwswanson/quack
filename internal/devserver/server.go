package devserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"quack/internal/domain"
	"quack/internal/publichttp"
	"quack/internal/releases"
	appruntime "quack/internal/runtime"
	"quack/internal/runtimehttp"
	"quack/internal/sites"
	"quack/internal/statichttp"
)

type Options struct {
	BuildDir      string
	Site          string
	Addr          string
	Port          string
	PortFile      string
	Watch         string
	WatchInterval time.Duration
	WatchDebounce time.Duration
	HostMatch     HostMatchMode
	AllowedHosts  []string
	StateDir      string
	Output        io.Writer
	Logger        *slog.Logger
}

func Run(ctx context.Context, opts Options) error {
	if opts.Output == nil {
		opts.Output = os.Stdout
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1"
	}
	if opts.Port == "" {
		opts.Port = "0"
	}
	if opts.Watch == "" {
		opts.Watch = "poll"
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = 500 * time.Millisecond
	}
	if opts.WatchDebounce <= 0 {
		opts.WatchDebounce = 100 * time.Millisecond
	}
	if opts.HostMatch == "" {
		opts.HostMatch = HostMatchAny
	}
	root, err := filepath.Abs(opts.BuildDir)
	if err != nil {
		return fmt.Errorf("resolve build directory: %w", err)
	}
	site := opts.Site
	if site == "" {
		site = filepath.Base(root)
	}
	repo := NewRepository(domain.ServerSettings{
		DefaultSite:  site,
		AllowedHosts: append([]string(nil), opts.AllowedHosts...),
	})
	source, err := repo.Refresh(ctx, root, site)
	if err != nil {
		return err
	}
	opts.Logger.InfoContext(ctx, "dev refresh succeeded", "generation", source.Generation, "files", len(source.Files), "routes", len(source.Routes), "build_dir", root)

	store := NewStorage(root)
	read := sites.NewSiteReadService(repo)
	releaseService := releases.NewService(repo, repo)
	staticHandler := statichttp.New(store, read)
	executor, err := appruntime.NewStarlarkExecutor(appruntime.ScriptLoaderFunc(func(ctx context.Context, key string) (io.ReadCloser, error) {
		return store.OpenBlob(ctx, key)
	}), appruntime.ResourceLimits{})
	if err != nil {
		return err
	}
	runtimeService := appruntime.NewService(appruntime.ServiceOptions{
		Repository:      repo,
		Policies:        repo,
		Executor:        executor,
		Settings:        repo,
		EnableExecution: true,
		DefaultLimits: appruntime.ResourceLimits{
			MaxDurationMillis:       60_000,
			MaxRequestBytes:         16 << 20,
			MaxResponseBytes:        16 << 20,
			MaxScriptBytes:          4 << 20,
			MaxMemoryBytes:          256 << 20,
			MaxConcurrency:          64,
			MaxExecutionSteps:       0,
			MaxWebSocketConnections: 4096,
		},
		MaxConcurrency: 64,
	})
	runtimeHandler := runtimehttp.New(runtimeService, runtimehttp.WithSettings(repo))

	mux := http.NewServeMux()
	mux.HandleFunc("/__quack/dev", statusHandler(repo, root))
	publichttp.New(
		staticHandler,
		publichttp.WithHostResolver(HostResolver{Site: site, Mode: opts.HostMatch, AllowedHosts: opts.AllowedHosts}),
		publichttp.WithRoutes(publichttp.ReleaseRouteReader{Releases: releaseService, Policies: repo}),
		publichttp.WithRuntime(runtimeHandler),
	).Register(mux)

	listener, err := net.Listen("tcp", net.JoinHostPort(opts.Addr, opts.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	actualURL := "http://" + listener.Addr().String()
	if host, port, splitErr := net.SplitHostPort(listener.Addr().String()); splitErr == nil {
		if host == "127.0.0.1" || host == "::1" {
			actualURL = "http://localhost:" + port
		}
	}
	if opts.PortFile != "" {
		if err := writePortFile(opts.PortFile, listener.Addr().String()); err != nil {
			return err
		}
	}
	fmt.Fprintf(opts.Output, "quack dev-server serving %s as %s\n", root, site)
	fmt.Fprintf(opts.Output, "%s\n", actualURL)

	watcherDone := make(chan struct{})
	if opts.Watch != "off" {
		go func() {
			defer close(watcherDone)
			err := WatchPoll(ctx, WatchOptions{
				RootDir:  root,
				Interval: opts.WatchInterval,
				Debounce: opts.WatchDebounce,
				OnRefresh: func(ctx context.Context) error {
					next, err := repo.Refresh(ctx, root, site)
					if err != nil {
						opts.Logger.ErrorContext(ctx, "dev refresh failed", "error", err)
						return nil
					}
					opts.Logger.InfoContext(ctx, "dev refresh succeeded", "generation", next.Generation, "files", len(next.Files), "routes", len(next.Routes))
					return nil
				},
			})
			if err != nil && ctx.Err() == nil {
				opts.Logger.ErrorContext(ctx, "dev watcher stopped", "error", err)
			}
		}()
	} else {
		close(watcherDone)
	}

	server := &http.Server{Handler: mux}
	serverErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err == http.ErrServerClosed {
			err = nil
		}
		serverErr <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-watcherDone
		return nil
	case err := <-serverErr:
		return err
	}
}

func statusHandler(repo *Repository, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		current, ok := repo.Current()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"site":       current.Site,
			"siteSHA":    current.SiteSHA,
			"generation": current.Generation,
			"buildDir":   root,
			"fileCount":  len(current.Files),
			"routeCount": len(current.Routes),
			"loadedAt":   current.LoadedAt.Format(time.RFC3339),
		})
	}
}

func writePortFile(name string, addr string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("create port-file directory: %w", err)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("read listener port: %w", err)
	}
	if err := os.WriteFile(name, []byte(port+"\n"), 0o644); err != nil {
		return fmt.Errorf("write port-file: %w", err)
	}
	return nil
}
