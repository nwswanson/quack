package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quack/internal/protocol"
)

func TestResolveCommandValuesUsesConfigAndAllowsFlagOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", path)
	if err := saveConfig(path, configFile{
		ServerURL: "http://config.example",
		Token:     "config-token",
	}); err != nil {
		t.Fatal(err)
	}

	values, err := resolveCommandValues(commandValues{token: "flag-token"})
	if err != nil {
		t.Fatal(err)
	}
	if values.serverURL != "http://config.example" {
		t.Fatalf("serverURL = %q, want config value", values.serverURL)
	}
	if values.token != "flag-token" {
		t.Fatalf("token = %q, want flag override", values.token)
	}
}

func TestResolveCommandValuesDoesNotReadConfigWhenFlagsAreComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", path)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	values, err := resolveCommandValues(commandValues{
		serverURL: "http://flag.example",
		token:     "flag-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if values.serverURL != "http://flag.example" || values.token != "flag-token" {
		t.Fatalf("values = %#v, want flags", values)
	}
}

func TestResolveCommandValuesSuggestsLoginWhenMissingConfig(t *testing.T) {
	t.Setenv("QUACK_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	_, err := resolveCommandValues(commandValues{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "quack login") {
		t.Fatalf("error = %q, want login suggestion", err.Error())
	}
}

func TestSaveConfigUsesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "quack.json")
	if err := saveConfig(path, configFile{
		ServerURL: "http://example.test",
		Token:     "secret",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	var cfg configFile
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://example.test" || cfg.Token != "secret" {
		t.Fatalf("config = %#v, want saved values", cfg)
	}
}

func TestRunLoginValidatesBeforeSavingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", configPath)

	checks := 0
	withCheckLogin(t, func(ctx context.Context, serverURL, token string) (*protocol.LoginCheckResponse, error) {
		checks++
		if serverURL != "http://example.test" {
			t.Fatalf("serverURL = %q, want http://example.test", serverURL)
		}
		if token != "secret" {
			t.Fatalf("token = %q, want secret", token)
		}
		return &protocol.LoginCheckResponse{OK: true}, nil
	})

	resp, err := runLogin([]string{"--serverURL", "http://example.test", "--token", "secret"}, strings.NewReader(""), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("checks = %d, want 1", checks)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://example.test" || cfg.Token != "secret" {
		t.Fatalf("config = %#v, want login values", cfg)
	}
}

func TestRunLoginDoesNotSaveInvalidCredentials(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", configPath)

	withCheckLogin(t, func(ctx context.Context, serverURL, token string) (*protocol.LoginCheckResponse, error) {
		return &protocol.LoginCheckResponse{OK: false, Error: "unauthorized"}, &clientUploadErrorForTest{}
	})

	_, err := runLogin([]string{"--serverURL", "http://example.test", "--token", "bad"}, strings.NewReader(""), os.Stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("config stat error = %v, want not exist", statErr)
	}
}

func TestRunDeployInfersSiteNameFromSimpleDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", configPath)
	if err := saveConfig(configPath, configFile{ServerURL: "http://example.test", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	var gotDirectory, gotSite string
	withUploadDirectory(t, func(ctx context.Context, serverURL, token, site, directory string) (*protocol.UploadArchiveResponse, error) {
		gotDirectory = directory
		gotSite = site
		if serverURL != "http://example.test" || token != "secret" {
			t.Fatalf("auth = (%q, %q), want config values", serverURL, token)
		}
		return &protocol.UploadArchiveResponse{OK: true, Site: site}, nil
	})

	resp, err := runDeploy([]string{"my-site"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
	if gotDirectory != "my-site" || gotSite != "my-site" {
		t.Fatalf("upload = (directory %q, site %q), want inferred my-site", gotDirectory, gotSite)
	}
}

func TestRunDeployRequiresSiteNameForPathLikeDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", configPath)
	if err := saveConfig(configPath, configFile{ServerURL: "http://example.test", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	withUploadDirectory(t, func(ctx context.Context, serverURL, token, site, directory string) (*protocol.UploadArchiveResponse, error) {
		t.Fatalf("upload should not be called for %q", directory)
		return nil, nil
	})

	for _, directory := range []string{".", "..", "./my-site", "../my-site", "../../folder", "nested/my-site", "/tmp/my-site"} {
		t.Run(directory, func(t *testing.T) {
			_, err := runDeploy([]string{directory})
			if err == nil {
				t.Fatal("expected usage error")
			}
			if !strings.Contains(err.Error(), "quack deploy <directory> <site name>") {
				t.Fatalf("error = %q, want explicit site-name usage", err.Error())
			}
		})
	}
}

func TestRunDeployUsesExplicitSiteForPathLikeDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "quack.json")
	t.Setenv("QUACK_CONFIG", configPath)
	if err := saveConfig(configPath, configFile{ServerURL: "http://example.test", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	var gotDirectory, gotSite string
	withUploadDirectory(t, func(ctx context.Context, serverURL, token, site, directory string) (*protocol.UploadArchiveResponse, error) {
		gotDirectory = directory
		gotSite = site
		return &protocol.UploadArchiveResponse{OK: true, Site: site}, nil
	})

	if _, err := runDeploy([]string{"./my-site", "my-site"}); err != nil {
		t.Fatal(err)
	}
	if gotDirectory != "./my-site" || gotSite != "my-site" {
		t.Fatalf("upload = (directory %q, site %q), want explicit site", gotDirectory, gotSite)
	}
}

func TestWriteRevisionsText(t *testing.T) {
	var out strings.Builder
	writeRevisionsText(&out, &protocol.ListRevisionsResponse{
		OK:      true,
		Site:    "example",
		Warning: "no older revisions available",
		Revisions: []protocol.SiteRevision{
			{Version: 3, Current: true, Files: 2, Bytes: 128, PublishedBy: "alice", FinishedAt: "2026-06-13T12:00:00Z"},
			{Version: 2, Files: 1, Bytes: 64, PublishedBy: "alice", FinishedAt: "2026-06-12T12:00:00Z"},
		},
	})

	got := out.String()
	for _, want := range []string{
		"Site: example\n",
		"Warning: no older revisions available\n",
		"VERSION",
		"3        yes",
		"alice",
		"2026-06-13T12:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want substring %q", got, want)
		}
	}
}

func TestWriteSitesText(t *testing.T) {
	var out strings.Builder
	writeSitesText(&out, &protocol.ListSitesResponse{
		OK: true,
		Sites: []protocol.SiteSummary{{
			Site: "foo", CurrentVersion: 2, VersionCount: 3, FileCount: 4, ByteCount: 512,
			PublishedBy: "alice", RuntimeStatus: "active", UpdatedAt: "2026-06-16T12:00:00Z",
		}},
	})

	got := out.String()
	for _, want := range []string{
		"SITE",
		"CURRENT",
		"foo",
		"alice",
		"active",
		"2026-06-16T12:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want substring %q", got, want)
		}
	}
}

type clientUploadErrorForTest struct{}

func (clientUploadErrorForTest) Error() string {
	return "login check failed (401 Unauthorized): unauthorized"
}

func withCheckLogin(t *testing.T, fn func(context.Context, string, string) (*protocol.LoginCheckResponse, error)) {
	t.Helper()
	previous := checkLogin
	checkLogin = fn
	t.Cleanup(func() {
		checkLogin = previous
	})
}

func withUploadDirectory(t *testing.T, fn func(context.Context, string, string, string, string) (*protocol.UploadArchiveResponse, error)) {
	t.Helper()
	previous := uploadDirectory
	uploadDirectory = fn
	t.Cleanup(func() {
		uploadDirectory = previous
	})
}
