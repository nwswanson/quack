package devserver

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	appserver "quack/internal/server"
)

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*l = append(*l, value)
	}
	return nil
}

func Command(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	var allowedHosts stringList
	fs := flag.NewFlagSet("dev-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", envDefault("QUACK_DEV_HOST", "127.0.0.1"), "address to bind")
	hostAlias := fs.String("host", "", "address to bind")
	port := fs.String("port", envDefault("QUACK_DEV_PORT", "0"), "port to bind")
	site := fs.String("site", envDefault("QUACK_DEV_SITE", ""), "site name")
	watch := fs.String("watch", envDefault("QUACK_DEV_WATCH", "poll"), "watch mode: poll or off")
	watchInterval := fs.Duration("watch-interval", envDurationDefault("QUACK_DEV_WATCH_INTERVAL", 500*time.Millisecond), "poll interval")
	watchDebounce := fs.Duration("watch-debounce", 100*time.Millisecond, "refresh debounce")
	stateDir := fs.String("state-dir", envDefault("QUACK_DEV_STATE_DIR", ""), "dev state directory")
	portFile := fs.String("port-file", "", "write selected port to this file")
	hostMatch := fs.String("host-match", envDefault("QUACK_DEV_HOST_MATCH", string(HostMatchAny)), "host matching: any or site")
	fs.Var(&allowedHosts, "allowed-host", "allowed host, repeatable")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	positionals := fs.Args()
	if len(positionals) < 1 || len(positionals) > 2 {
		return fmt.Errorf("usage: quack dev-server <build-dir> [site] [--addr 127.0.0.1] [--port 0]")
	}
	if *hostAlias != "" {
		*addr = *hostAlias
	}
	if len(positionals) == 2 && *site == "" {
		*site = positionals[1]
	}
	mode := HostMatchMode(*hostMatch)
	if mode != HostMatchAny && mode != HostMatchSite {
		return fmt.Errorf("--host-match must be any or site")
	}
	switch strings.ToLower(strings.TrimSpace(*watch)) {
	case "poll", "fs":
		*watch = "poll"
	case "off":
	default:
		return fmt.Errorf("--watch must be poll, fs, or off")
	}
	if envAllowed := strings.TrimSpace(os.Getenv("QUACK_DEV_ALLOWED_HOSTS")); envAllowed != "" && len(allowedHosts) == 0 {
		for _, host := range strings.Split(envAllowed, ",") {
			_ = allowedHosts.Set(host)
		}
	}

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := appserver.ConfigureLogger("info", stdout); err != nil {
		return err
	}
	return Run(runCtx, Options{
		BuildDir:      positionals[0],
		Site:          *site,
		Addr:          *addr,
		Port:          *port,
		PortFile:      *portFile,
		Watch:         strings.ToLower(strings.TrimSpace(*watch)),
		WatchInterval: *watchInterval,
		WatchDebounce: *watchDebounce,
		HostMatch:     mode,
		AllowedHosts:  []string(allowedHosts),
		StateDir:      *stateDir,
		Logger:        appserver.NewLogger(stdout),
	})
}

func reorderFlags(args []string) []string {
	valueFlags := map[string]bool{
		"--addr": true, "--host": true, "--port": true, "--site": true,
		"--watch": true, "--watch-interval": true, "--watch-debounce": true,
		"--state-dir": true, "--port-file": true, "--host-match": true,
		"--allowed-host": true,
	}
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := arg
		if before, _, ok := strings.Cut(arg, "="); ok {
			name = before
		}
		if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

func envDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDurationDefault(name string, fallback time.Duration) time.Duration {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
