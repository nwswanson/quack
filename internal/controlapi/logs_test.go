package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quack/internal/domain"
	"quack/internal/logbuffer"
	"quack/internal/protocol"
	"quack/internal/releases"
)

func TestLogsEndpointAllowsUserOwnedSiteOnly(t *testing.T) {
	logs := logbuffer.New(10)
	logs.Add(logbuffer.Event{Site: "owned", Source: "starlark", Message: "visible"})
	logs.Add(logbuffer.Event{Site: "other", Source: "starlark", Message: "hidden"})
	h := New(Options{
		Users:    logUsers{users: map[string]domain.AdminUser{"user-token": {ID: 7, Username: "alice", AdminPriv: "user"}}},
		Releases: logReleases{sites: []domain.PublishedSite{{Site: "owned"}}},
		Logs:     logs,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, protocol.LogsPath+"?site=owned", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "visible") || strings.Contains(rec.Body.String(), "hidden") {
		t.Fatalf("body = %s, want only owned site logs", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, protocol.LogsPath+"?site=other", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unowned status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestLogsEndpointAllowsAdminAllLogs(t *testing.T) {
	logs := logbuffer.New(10)
	logs.Add(logbuffer.Event{Source: "access", Message: "system"})
	logs.Add(logbuffer.Event{Site: "owned", Source: "starlark", Message: "site"})
	h := New(Options{
		Users:    logUsers{users: map[string]domain.AdminUser{"admin-token": {ID: 1, Username: "admin", AdminPriv: "admin:*"}}},
		Releases: logReleases{},
		Logs:     logs,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, protocol.LogsPath+"?all=true", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "system") || !strings.Contains(rec.Body.String(), "site") {
		t.Fatalf("body = %s, want admin all logs", rec.Body.String())
	}
}

type logUsers struct {
	users map[string]domain.AdminUser
}

func (u logUsers) FindUserByToken(ctx context.Context, token string) (domain.AdminUser, bool, error) {
	user, ok := u.users[token]
	return user, ok, nil
}

type logReleases struct {
	sites []domain.PublishedSite
}

func (r logReleases) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	return append([]domain.PublishedSite(nil), r.sites...), nil
}

func (r logReleases) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	return nil, nil
}

func (r logReleases) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	return nil, nil
}

func (r logReleases) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, nil
}

func (r logReleases) RollbackSiteToVersion(ctx context.Context, user domain.AdminUser, site string, siteSHA string, version int64) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, nil
}

func (r logReleases) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	return domain.UnpublishRecord{}, nil
}

func (r logReleases) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	return domain.PublishRecord{}, nil
}

func (r logReleases) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return false, nil
}

func (r logReleases) LookupRoute(ctx context.Context, site string, urlPath string) (releases.RouteDecision, bool, error) {
	return releases.RouteDecision{}, false, nil
}
