package client

import (
	"context"
	"fmt"
	"io"
	"net/http"

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
		err := protocol.WriteTar(ctx, directory, pw)
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

	req, err := protocol.NewRequest(ctx, http.MethodDelete, protocol.SiteURL(serverURL, site), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create delete request: %w", err)
	}
	return doRequest[protocol.DeleteSiteResponse](req, "delete", "delete site")
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

	req, err := protocol.NewRequest(ctx, http.MethodPost, protocol.SiteUnpublishURL(serverURL, site), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create unpublish request: %w", err)
	}
	return doRequest[protocol.UnpublishSiteResponse](req, "unpublish", "unpublish site")
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

	req, err := protocol.NewRequest(ctx, http.MethodPost, protocol.SitePublishURL(serverURL, site), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create publish request: %w", err)
	}
	return doRequest[protocol.PublishSiteResponse](req, "publish", "publish site")
}

func SetDefaultSite(ctx context.Context, serverURL, token, site string) (*protocol.SetDefaultSiteResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	req, err := protocol.NewJSONRequest(ctx, http.MethodPost, protocol.SettingsDefaultSiteURL(serverURL), token, protocol.SetDefaultSiteRequest{DefaultSite: site})
	if err != nil {
		return nil, fmt.Errorf("create default site request: %w", err)
	}
	return doRequest[protocol.SetDefaultSiteResponse](req, "set default site", "set default site")
}

func ListSites(ctx context.Context, serverURL, token, username string, includeAll bool) (*protocol.ListSitesResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	target, err := protocol.SitesURL(serverURL, protocol.ListSitesRequest{Username: username, IncludeAll: includeAll})
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	req, err := protocol.NewRequest(ctx, http.MethodGet, target, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create site list request: %w", err)
	}
	return doRequest[protocol.ListSitesResponse](req, "list sites", "list sites")
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

	req, err := protocol.NewRequest(ctx, http.MethodGet, protocol.SiteRevisionsURL(serverURL, site), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create revision list request: %w", err)
	}
	return doRequest[protocol.ListRevisionsResponse](req, "list revisions", "list revisions")
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

	req, err := protocol.NewRequest(ctx, http.MethodPost, protocol.SiteRollbackURL(serverURL, site), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create rollback request: %w", err)
	}
	return doRequest[protocol.RollbackSiteResponse](req, "rollback", "rollback site")
}

func CheckLogin(ctx context.Context, serverURL, token string) (*protocol.LoginCheckResponse, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	req, err := protocol.NewRequest(ctx, http.MethodPost, protocol.LoginCheckURL(serverURL), token, nil)
	if err != nil {
		return nil, fmt.Errorf("create login check request: %w", err)
	}
	return doRequest[protocol.LoginCheckResponse](req, "login check", "check login")
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

func doRequest[T protocol.ErrorGetter](req *http.Request, operation string, transportOperation string) (*T, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", transportOperation, err)
	}
	defer resp.Body.Close()

	out, err := protocol.DecodeResponse[T](resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
	return &out, nil
}
