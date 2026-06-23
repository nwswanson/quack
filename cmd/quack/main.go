package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"quack/internal/client"
	"quack/internal/devserver"
	"quack/internal/protocol"
)

var checkLogin = client.CheckLogin
var uploadDirectory = client.UploadDirectory
var listLogs = client.ListLogs
var streamLogs = client.StreamLogs

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var resp any
	var err error
	textOutput := false
	switch os.Args[1] {
	case "login":
		resp, err = runLogin(os.Args[2:], os.Stdin, os.Stderr)
	case "deploy":
		resp, err = runDeploy(os.Args[2:])
	case "delete":
		resp, err = runDelete(os.Args[2:])
	case "sites":
		resp, err = runSites(os.Args[2:])
		textOutput = true
	case "logs":
		if err := runLogs(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case "revisions":
		resp, err = runRevisions(os.Args[2:])
		textOutput = true
	case "rollback":
		resp, err = runRollback(os.Args[2:])
	case "unpublish":
		resp, err = runUnpublish(os.Args[2:])
	case "publish":
		resp, err = runPublish(os.Args[2:])
	case "default-site":
		resp, err = runDefaultSite(os.Args[2:])
	case "dev-server":
		if err := devserver.Command(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if textOutput {
		switch out := resp.(type) {
		case *protocol.ListSitesResponse:
			writeSitesText(os.Stdout, out)
		case *protocol.ListRevisionsResponse:
			writeRevisionsText(os.Stdout, out)
		}
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

func runDeploy(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) < 1 || len(positionals) > 2 {
		return nil, fmt.Errorf("usage: quack deploy <directory> [site name] [--token <token>] [--serverURL <url>]")
	}
	directory := positionals[0]
	site := ""
	switch len(positionals) {
	case 1:
		if !canInferSiteNameFromDirectory(directory) {
			return nil, fmt.Errorf("usage: quack deploy <directory> <site name> [--token <token>] [--serverURL <url>]")
		}
		site = directory
	case 2:
		site = positionals[1]
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}

	return uploadDirectory(context.Background(), resolved.serverURL, resolved.token, site, directory)
}

func canInferSiteNameFromDirectory(directory string) bool {
	directory = strings.TrimSpace(directory)
	if directory == "" || directory == "." || directory == ".." {
		return false
	}
	if filepath.IsAbs(directory) {
		return false
	}
	return !strings.ContainsAny(directory, `/\`)
}

func runDelete(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack delete <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}

	return client.DeleteSite(context.Background(), resolved.serverURL, resolved.token, positionals[0])
}

func runUnpublish(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack unpublish <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}
	return client.UnpublishSite(context.Background(), resolved.serverURL, resolved.token, positionals[0])
}

func runPublish(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack publish <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}
	return client.PublishSite(context.Background(), resolved.serverURL, resolved.token, positionals[0])
}

func runDefaultSite(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if values.clear && len(positionals) > 0 {
		return nil, fmt.Errorf("usage: quack default-site <site name> [--clear] [--token <token>] [--serverURL <url>]")
	}
	if !values.clear && len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack default-site <site name> [--clear] [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}
	site := ""
	if !values.clear {
		site = positionals[0]
	}
	return client.SetDefaultSite(context.Background(), resolved.serverURL, resolved.token, site)
}

func runSites(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) > 1 {
		return nil, fmt.Errorf("usage: quack sites [username] [--all] [--token <token>] [--serverURL <url>]")
	}
	if values.all && len(positionals) > 0 {
		return nil, fmt.Errorf("usage: quack sites [username] [--all] [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}
	username := ""
	if len(positionals) == 1 {
		username = positionals[0]
	}
	return client.ListSites(context.Background(), resolved.serverURL, resolved.token, username, values.all)
}

func runLogs(args []string, w io.Writer) error {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("usage: quack logs [site] [--follow] [--all] [--system] [--limit <n>] [--token <token>] [--serverURL <url>]")
	}
	if values.all && len(positionals) > 0 {
		return fmt.Errorf("usage: quack logs [site] [--follow] [--all] [--system] [--limit <n>] [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return err
	}
	req := protocol.LogsRequest{
		IncludeAll: values.all, IncludeSystem: values.system, Limit: values.limit, Follow: values.follow,
	}
	if len(positionals) == 1 {
		req.Site = positionals[0]
	}
	if values.follow {
		return streamLogs(context.Background(), resolved.serverURL, resolved.token, req, func(event protocol.LogEvent) error {
			writeLogEvent(w, event)
			return nil
		})
	}
	resp, err := listLogs(context.Background(), resolved.serverURL, resolved.token, req)
	if err != nil {
		return err
	}
	writeLogsText(w, resp.Events)
	return nil
}

func writeLogsText(w io.Writer, events []protocol.LogEvent) {
	if len(events) == 0 {
		fmt.Fprintln(w, "No logs.")
		return
	}
	for _, event := range events {
		writeLogEvent(w, event)
	}
}

func writeLogEvent(w io.Writer, event protocol.LogEvent) {
	site := event.Site
	if site == "" {
		site = "system"
	}
	route := event.Route
	if route == "" {
		route = "-"
	}
	fmt.Fprintf(w, "%s %-5s %-14s %-24s %-18s %s\n", event.Time, strings.ToUpper(event.Level), event.Source, site, route, event.Message)
	if event.StackTrace != "" {
		fmt.Fprintln(w, event.StackTrace)
	}
}

func writeSitesText(w io.Writer, resp *protocol.ListSitesResponse) {
	if len(resp.Sites) == 0 {
		fmt.Fprintln(w, "No sites.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tCURRENT\tVERSIONS\tFILES\tBYTES\tPUBLISHED BY\tLIVE\tSTATUS\tUPDATED\tPOLICY REASON")
	for _, site := range resp.Sites {
		publishedBy := site.PublishedBy
		if publishedBy == "" {
			publishedBy = "-"
		}
		status := site.RuntimeStatus
		if status == "" {
			status = "-"
		}
		liveState := site.LiveState
		if liveState == "" {
			liveState = "-"
		}
		updatedAt := site.UpdatedAt
		if updatedAt == "" {
			updatedAt = "-"
		}
		reason := site.PolicyReason
		if reason == "" {
			reason = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			site.Site, site.CurrentVersion, site.VersionCount, site.FileCount, site.ByteCount,
			publishedBy, liveState, status, updatedAt, reason,
		)
	}
	_ = tw.Flush()
}

func runRevisions(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack revisions <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}

	return client.ListRevisions(context.Background(), resolved.serverURL, resolved.token, positionals[0])
}

func writeRevisionsText(w io.Writer, resp *protocol.ListRevisionsResponse) {
	fmt.Fprintf(w, "Site: %s\n", resp.Site)
	if resp.Warning != "" {
		fmt.Fprintf(w, "Warning: %s\n", resp.Warning)
	}
	if len(resp.Revisions) == 0 {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tCURRENT\tFILES\tBYTES\tPUBLISHED BY\tFINISHED")
	for _, rev := range resp.Revisions {
		current := ""
		if rev.Current {
			current = "yes"
		}
		publishedBy := rev.PublishedBy
		if publishedBy == "" {
			publishedBy = "-"
		}
		finishedAt := rev.FinishedAt
		if finishedAt == "" {
			finishedAt = rev.CreatedAt
		}
		if finishedAt == "" {
			finishedAt = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%s\t%s\n", rev.Version, current, rev.Files, rev.Bytes, publishedBy, finishedAt)
	}
	_ = tw.Flush()
}

func runRollback(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack rollback <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}

	return client.RollbackSite(context.Background(), resolved.serverURL, resolved.token, positionals[0])
}

func runLogin(args []string, stdin io.Reader, stderr io.Writer) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 0 {
		return nil, fmt.Errorf("usage: quack login [--token <token>] [--serverURL <url>]")
	}

	reader := bufio.NewReader(stdin)
	if values.serverURL == "" {
		values.serverURL, err = prompt(reader, stderr, "server URL: ")
		if err != nil {
			return nil, err
		}
	}
	if values.token == "" {
		values.token, err = prompt(reader, stderr, "token: ")
		if err != nil {
			return nil, err
		}
	}
	values.serverURL = strings.TrimSpace(values.serverURL)
	values.token = strings.TrimSpace(values.token)
	if values.serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if values.token == "" {
		return nil, fmt.Errorf("token is required")
	}

	if _, err := checkLogin(context.Background(), values.serverURL, values.token); err != nil {
		return nil, err
	}

	path, err := defaultConfigPath()
	if err != nil {
		return nil, err
	}
	if err := saveConfig(path, configFile{
		ServerURL: values.serverURL,
		Token:     values.token,
	}); err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":         true,
		"configPath": path,
	}, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  quack login")
	fmt.Fprintln(os.Stderr, "  quack sites [username] [--all] [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack logs [site] [--follow] [--all] [--system] [--limit <n>] [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack deploy <directory> [site name] [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack revisions <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack rollback <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack unpublish <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack publish <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack default-site <site name> [--clear] [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack delete <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack dev-server <build-dir> [site] [--addr 127.0.0.1] [--port 0]")
}

type commandValues struct {
	token     string
	serverURL string
	all       bool
	clear     bool
	follow    bool
	system    bool
	limit     int
}

type configFile struct {
	ServerURL string `json:"serverURL"`
	Token     string `json:"token"`
}

func parseCommandArgs(args []string) (commandValues, []string, error) {
	var values commandValues
	var positionals []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--token":
			if !hasValue {
				i++
				if i >= len(args) {
					return values, nil, fmt.Errorf("--token requires a value")
				}
				value = args[i]
			}
			values.token = value
		case "--serverURL":
			if !hasValue {
				i++
				if i >= len(args) {
					return values, nil, fmt.Errorf("--serverURL requires a value")
				}
				value = args[i]
			}
			values.serverURL = value
		case "--all":
			if hasValue {
				return values, nil, fmt.Errorf("--all does not take a value")
			}
			values.all = true
		case "--follow":
			if hasValue {
				return values, nil, fmt.Errorf("--follow does not take a value")
			}
			values.follow = true
		case "--system":
			if hasValue {
				return values, nil, fmt.Errorf("--system does not take a value")
			}
			values.system = true
		case "--limit":
			if !hasValue {
				i++
				if i >= len(args) {
					return values, nil, fmt.Errorf("--limit requires a value")
				}
				value = args[i]
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return values, nil, fmt.Errorf("--limit must be >= 0")
			}
			values.limit = n
		case "--clear":
			if hasValue {
				return values, nil, fmt.Errorf("--clear does not take a value")
			}
			values.clear = true
		default:
			if strings.HasPrefix(arg, "-") {
				return values, nil, fmt.Errorf("unknown flag: %s", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	return values, positionals, nil
}

func resolveCommandValues(values commandValues) (commandValues, error) {
	if values.serverURL != "" && values.token != "" {
		return values, nil
	}

	cfg, err := loadConfig()
	if err != nil && !os.IsNotExist(err) {
		return commandValues{}, err
	}
	if values.serverURL == "" && cfg.ServerURL != "" {
		values.serverURL = cfg.ServerURL
	}
	if values.token == "" && cfg.Token != "" {
		values.token = cfg.Token
	}
	if values.serverURL == "" || values.token == "" {
		return commandValues{}, fmt.Errorf("serverURL and token are required; pass --serverURL and --token or run `quack login`")
	}
	return values, nil
}

func loadConfig() (configFile, error) {
	path, err := defaultConfigPath()
	if err != nil {
		return configFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return configFile{}, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return configFile{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg configFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure config permissions: %w", err)
	}
	return nil
}

func defaultConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("QUACK_CONFIG")); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "quack.json"), nil
}

func prompt(reader *bufio.Reader, stderr io.Writer, label string) (string, error) {
	fmt.Fprint(stderr, label)
	value, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read prompt: %w", err)
	}
	return strings.TrimSpace(value), nil
}
