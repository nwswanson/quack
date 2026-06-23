package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"quack/internal/manifest"
	"quack/internal/protocol"
)

var httpClient = http.DefaultClient

func require(fields ...string) error {
	for i := 0; i < len(fields); i += 2 {
		if fields[i+1] == "" {
			return fmt.Errorf("%s is required", fields[i])
		}
	}
	return nil
}

func requireSite(serverURL, token, site string) error {
	return require("serverURL", serverURL, "token", token, "site", site)
}

func created[T protocol.ErrorGetter](createErr, operation, transportOperation string) func(*http.Request, error) (*T, error) {
	return func(req *http.Request, err error) (*T, error) {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", createErr, err)
		}
		return doRequest[T](req, operation, transportOperation)
	}
}

func request[T protocol.ErrorGetter](ctx context.Context, method, target, token string, body io.Reader, createErr, operation, transportOperation string) (*T, error) {
	return created[T](createErr, operation, transportOperation)(protocol.NewRequest(ctx, method, target, token, body))
}

func siteRequest[T protocol.ErrorGetter](ctx context.Context, serverURL, token, site, method string, url func(string, string) string, createName, operation, transportOperation string) (*T, error) {
	if err := requireSite(serverURL, token, site); err != nil {
		return nil, err
	}
	return request[T](ctx, method, url(serverURL, site), token, nil, "create "+createName+" request", operation, transportOperation)
}

func UploadDirectory(ctx context.Context, serverURL, token, site, directory string) (*protocol.UploadArchiveResponse, error) {
	if err := require("server", serverURL, "token", token, "site", site); err != nil {
		return nil, err
	}
	if err := validateDirectory(directory); err != nil {
		return nil, err
	}
	siteManifest, err := readUploadManifest(directory)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		err := protocol.WriteTarWithOptions(ctx, directory, pw, protocol.WriteTarOptions{
			Exclude: siteManifest.Exclude,
		})
		_ = pw.CloseWithError(err)
	}()

	req, err := protocol.NewRequest(ctx, http.MethodPost, protocol.UploadArchiveURL(serverURL), token, pr)
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", protocol.ContentTypeTar)
	req.Header.Set(protocol.HeaderSite, site)
	return doRequest[protocol.UploadArchiveResponse](req, "upload", "upload archive")
}

func readUploadManifest(directory string) (manifest.Manifest, error) {
	for _, name := range []string{"site.yml", "site.yaml"} {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return manifest.Manifest{}, fmt.Errorf("stat %s: %w", name, err)
		}
		if info.IsDir() {
			continue
		}

		file, err := os.Open(path)
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("open %s: %w", name, err)
		}
		siteManifest, parseErr := manifest.Parse(file, info.Size())
		closeErr := file.Close()
		if parseErr != nil {
			return manifest.Manifest{}, parseErr
		}
		if closeErr != nil {
			return manifest.Manifest{}, fmt.Errorf("close %s: %w", name, closeErr)
		}
		return siteManifest, nil
	}
	return manifest.Default(), nil
}

func DeleteSite(ctx context.Context, serverURL, token, site string) (*protocol.DeleteSiteResponse, error) {
	return siteRequest[protocol.DeleteSiteResponse](ctx, serverURL, token, site, http.MethodDelete, protocol.SiteURL, "delete", "delete", "delete site")
}

func UnpublishSite(ctx context.Context, serverURL, token, site string) (*protocol.UnpublishSiteResponse, error) {
	return siteRequest[protocol.UnpublishSiteResponse](ctx, serverURL, token, site, http.MethodPost, protocol.SiteUnpublishURL, "unpublish", "unpublish", "unpublish site")
}

func PublishSite(ctx context.Context, serverURL, token, site string) (*protocol.PublishSiteResponse, error) {
	return siteRequest[protocol.PublishSiteResponse](ctx, serverURL, token, site, http.MethodPost, protocol.SitePublishURL, "publish", "publish", "publish site")
}

func SetDefaultSite(ctx context.Context, serverURL, token, site string) (*protocol.SetDefaultSiteResponse, error) {
	if err := require("serverURL", serverURL, "token", token); err != nil {
		return nil, err
	}
	return created[protocol.SetDefaultSiteResponse]("create default site request", "set default site", "set default site")(
		protocol.NewJSONRequest(ctx, http.MethodPost, protocol.SettingsDefaultSiteURL(serverURL), token, protocol.SetDefaultSiteRequest{DefaultSite: site}),
	)
}

func ListSites(ctx context.Context, serverURL, token, username string, includeAll bool) (*protocol.ListSitesResponse, error) {
	if err := require("serverURL", serverURL, "token", token); err != nil {
		return nil, err
	}

	target, err := protocol.SitesURL(serverURL, protocol.ListSitesRequest{Username: username, IncludeAll: includeAll})
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}

	return request[protocol.ListSitesResponse](ctx, http.MethodGet, target, token, nil, "create site list request", "list sites", "list sites")
}

func ListRevisions(ctx context.Context, serverURL, token, site string) (*protocol.ListRevisionsResponse, error) {
	return siteRequest[protocol.ListRevisionsResponse](ctx, serverURL, token, site, http.MethodGet, protocol.SiteRevisionsURL, "revision list", "list revisions", "list revisions")
}

func RollbackSite(ctx context.Context, serverURL, token, site string) (*protocol.RollbackSiteResponse, error) {
	return siteRequest[protocol.RollbackSiteResponse](ctx, serverURL, token, site, http.MethodPost, protocol.SiteRollbackURL, "rollback", "rollback", "rollback site")
}

func CheckLogin(ctx context.Context, serverURL, token string) (*protocol.LoginCheckResponse, error) {
	if err := require("serverURL", serverURL, "token", token); err != nil {
		return nil, err
	}
	return request[protocol.LoginCheckResponse](ctx, http.MethodPost, protocol.LoginCheckURL(serverURL), token, nil, "create login check request", "login check", "check login")
}

func ListLogs(ctx context.Context, serverURL, token string, req protocol.LogsRequest) (*protocol.LogsResponse, error) {
	if err := require("serverURL", serverURL, "token", token); err != nil {
		return nil, err
	}
	target, err := protocol.LogsURL(serverURL, req)
	if err != nil {
		return nil, fmt.Errorf("parse logs URL: %w", err)
	}
	return request[protocol.LogsResponse](ctx, http.MethodGet, target, token, nil, "create logs request", "logs", "logs")
}

func StreamLogs(ctx context.Context, serverURL, token string, req protocol.LogsRequest, handle func(protocol.LogEvent) error) error {
	if err := require("serverURL", serverURL, "token", token); err != nil {
		return err
	}
	req.Follow = true
	target, err := protocol.LogsURL(serverURL, req)
	if err != nil {
		return fmt.Errorf("parse logs URL: %w", err)
	}
	httpReq, err := protocol.NewRequest(ctx, http.MethodGet, target, token, nil)
	if err != nil {
		return fmt.Errorf("create logs stream request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("stream logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out, decodeErr := protocol.DecodeResponse[protocol.LogsResponse](resp)
		if decodeErr != nil {
			return decodeErr
		}
		message := out.ErrorMessage()
		if message == "" {
			message = resp.Status
		}
		return &UploadError{Operation: "logs", StatusCode: resp.StatusCode, Status: resp.Status, Message: message}
	}
	return readLogStream(resp.Body, handle)
}

func readLogStream(r io.Reader, handle func(protocol.LogEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				var event protocol.LogEvent
				if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
					return fmt.Errorf("decode log event: %w", err)
				}
				if err := handle(event); err != nil {
					return err
				}
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read log stream: %w", err)
	}
	return nil
}

type UploadError struct {
	Operation  string
	StatusCode int
	Status     string
	Message    string
}

func (e *UploadError) Error() string {
	operation := e.Operation
	if operation == "" {
		operation = "request"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s failed (%s): %s", operation, e.Status, e.Message)
	}
	return fmt.Sprintf("%s failed (%s)", operation, e.Status)
}

func doRequest[T protocol.ErrorGetter](req *http.Request, operation, transportOperation string) (*T, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", transportOperation, err)
	}
	defer resp.Body.Close()

	out, err := protocol.DecodeResponse[T](resp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &out, nil
	}

	message := out.ErrorMessage()
	if message == "" {
		message = resp.Status
	}

	return &out, &UploadError{
		Operation:  operation,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Message:    message,
	}
}
