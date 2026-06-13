package client

import (
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
