package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"quack/internal/protocol"
)

var httpClient = http.DefaultClient

func UploadDirectory(ctx context.Context, serverURL, token, site, directory string) (*protocol.UploadArchiveResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("server is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}
	if err := validateDirectory(directory); err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		err := WriteTar(ctx, directory, pw)
		_ = pw.CloseWithError(err)
	}()

	url := strings.TrimRight(serverURL, "/") + protocol.UploadArchivePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", protocol.ContentTypeTar)
	req.Header.Set(protocol.HeaderSite, site)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload archive: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeUploadResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return &out, &UploadError{
			Operation:  "upload",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    out.Error,
		}
	}
	return &out, nil
}

func DeleteSite(ctx context.Context, serverURL, token, site string) (*protocol.DeleteSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.DeleteSitePathPrefix + url.PathEscape(site)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("delete site: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeDeleteSiteResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "delete",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func UnpublishSite(ctx context.Context, serverURL, token, site string) (*protocol.UnpublishSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.DeleteSitePathPrefix + url.PathEscape(site) + protocol.SiteUnpublishPathSuffix
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create unpublish request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unpublish site: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeUnpublishSiteResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "unpublish",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func PublishSite(ctx context.Context, serverURL, token, site string) (*protocol.PublishSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.DeleteSitePathPrefix + url.PathEscape(site) + protocol.SitePublishPathSuffix
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create publish request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish site: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodePublishSiteResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "publish",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func SetDefaultSite(ctx context.Context, serverURL, token, site string) (*protocol.SetDefaultSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	body, err := json.Marshal(map[string]string{"default_site": site})
	if err != nil {
		return nil, fmt.Errorf("encode default site request: %w", err)
	}

	target := strings.TrimRight(serverURL, "/") + protocol.SettingsDefaultSitePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create default site request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("set default site: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeSetDefaultSiteResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "set default site",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func ListSites(ctx context.Context, serverURL, token, username string, includeAll bool) (*protocol.ListSitesResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	target, err := url.Parse(strings.TrimRight(serverURL, "/") + protocol.SitesPath)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	query := target.Query()
	if includeAll {
		query.Set("all", "true")
	}
	if strings.TrimSpace(username) != "" {
		query.Set("user", strings.TrimSpace(username))
	}
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create site list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeListSitesResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "list sites",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func ListRevisions(ctx context.Context, serverURL, token, site string) (*protocol.ListRevisionsResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.DeleteSitePathPrefix + url.PathEscape(site) + protocol.SiteRevisionPathSuffix
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create revision list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeListRevisionsResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "list revisions",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func RollbackSite(ctx context.Context, serverURL, token, site string) (*protocol.RollbackSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if site == "" {
		return nil, fmt.Errorf("site is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.DeleteSitePathPrefix + url.PathEscape(site) + protocol.SiteRollbackPathSuffix
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create rollback request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rollback site: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeRollbackSiteResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "rollback",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
}

func CheckLogin(ctx context.Context, serverURL, token string) (*protocol.LoginCheckResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	target := strings.TrimRight(serverURL, "/") + protocol.LoginCheckPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create login check request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check login: %w", err)
	}
	defer resp.Body.Close()

	out, err := decodeLoginCheckResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = resp.Status
		}
		return &out, &UploadError{
			Operation:  "login check",
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    message,
		}
	}
	return &out, nil
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

func decodeUploadResponse(resp *http.Response) (protocol.UploadArchiveResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.UploadArchiveResponse{}, err
	}

	var out protocol.UploadArchiveResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.UploadArchiveResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeDeleteSiteResponse(resp *http.Response) (protocol.DeleteSiteResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.DeleteSiteResponse{}, err
	}

	var out protocol.DeleteSiteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.DeleteSiteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeUnpublishSiteResponse(resp *http.Response) (protocol.UnpublishSiteResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.UnpublishSiteResponse{}, err
	}

	var out protocol.UnpublishSiteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.UnpublishSiteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodePublishSiteResponse(resp *http.Response) (protocol.PublishSiteResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.PublishSiteResponse{}, err
	}

	var out protocol.PublishSiteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.PublishSiteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeSetDefaultSiteResponse(resp *http.Response) (protocol.SetDefaultSiteResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.SetDefaultSiteResponse{}, err
	}

	var out protocol.SetDefaultSiteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.SetDefaultSiteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeListSitesResponse(resp *http.Response) (protocol.ListSitesResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.ListSitesResponse{}, err
	}

	var out protocol.ListSitesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.ListSitesResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeListRevisionsResponse(resp *http.Response) (protocol.ListRevisionsResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.ListRevisionsResponse{}, err
	}

	var out protocol.ListRevisionsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.ListRevisionsResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeRollbackSiteResponse(resp *http.Response) (protocol.RollbackSiteResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.RollbackSiteResponse{}, err
	}

	var out protocol.RollbackSiteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.RollbackSiteResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func decodeLoginCheckResponse(resp *http.Response) (protocol.LoginCheckResponse, error) {
	body, err := readResponseBody(resp)
	if err != nil {
		return protocol.LoginCheckResponse{}, err
	}

	var out protocol.LoginCheckResponse
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fallbackResponseMessage(resp, body)
			return out, nil
		}
		return protocol.LoginCheckResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return body, nil
}

func fallbackResponseMessage(resp *http.Response, body []byte) string {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return message
}
