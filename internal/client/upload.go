package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"quack/internal/protocol"
)

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload archive: %w", err)
	}
	defer resp.Body.Close()

	var out protocol.UploadArchiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return &out, fmt.Errorf("upload failed: %s", out.Error)
	}
	return &out, nil
}
