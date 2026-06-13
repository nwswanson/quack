package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"quack/internal/client"
)

var checkLogin = client.CheckLogin

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var resp any
	var err error
	switch os.Args[1] {
	case "login":
		resp, err = runLogin(os.Args[2:], os.Stdin, os.Stderr)
	case "deploy":
		resp, err = runDeploy(os.Args[2:])
	case "delete":
		resp, err = runDelete(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

func runDeploy(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 2 {
		return nil, fmt.Errorf("usage: quack deploy <directory> <site name> [--token <token>] [--serverURL <url>]")
	}
	resolved, err := resolveCommandValues(values)
	if err != nil {
		return nil, err
	}

	return client.UploadDirectory(context.Background(), resolved.serverURL, resolved.token, positionals[1], positionals[0])
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
	fmt.Fprintln(os.Stderr, "  quack deploy <directory> <site name> [--token <token>] [--serverURL <url>]")
	fmt.Fprintln(os.Stderr, "  quack delete <site name> [--token <token>] [--serverURL <url>]")
}

type commandValues struct {
	token     string
	serverURL string
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
