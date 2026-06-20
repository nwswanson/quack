package releases

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"quack/internal/domain"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
)

func TestServiceDelegatesRevisionReads(t *testing.T) {
	repo := &releaseRepo{revisions: []domain.RevisionRecord{{Version: 2, Current: true}}}
	service := NewService(repo, &releaseInvalidator{})

	got, err := service.ListSiteRevisions(context.Background(), domain.AdminUser{ID: 7}, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, repo.revisions) {
		t.Fatalf("revisions = %+v, want %+v", got, repo.revisions)
	}
}

func TestServiceInvalidatesAfterReleaseWrites(t *testing.T) {
	repo := &releaseRepo{}
	invalidator := &releaseInvalidator{}
	service := NewService(repo, invalidator)
	ctx := context.Background()

	if _, err := service.PublishSite(ctx, domain.AdminUser{}, "foo", "foo-sha"); err != nil {
		t.Fatalf("PublishSite error = %v", err)
	}
	if _, err := service.RollbackSite(ctx, domain.AdminUser{}, "foo", "foo-sha"); err != nil {
		t.Fatalf("RollbackSite error = %v", err)
	}
	if _, err := service.UnpublishSite(ctx, domain.AdminUser{}, "foo", "foo-sha"); err != nil {
		t.Fatalf("UnpublishSite error = %v", err)
	}
	if _, err := service.DeleteSite(ctx, "foo", "foo-sha"); err != nil {
		t.Fatalf("DeleteSite error = %v", err)
	}

	assertReleaseContains(t, invalidator.calls, "site:foo")
	assertReleaseContains(t, invalidator.calls, "version:foo-sha:0")
}

func TestServiceDoesNotInvalidateFailedReleaseWrite(t *testing.T) {
	repo := &releaseRepo{err: fmt.Errorf("write failed")}
	invalidator := &releaseInvalidator{}
	service := NewService(repo, invalidator)

	if _, err := service.PublishSite(context.Background(), domain.AdminUser{}, "foo", "foo-sha"); err == nil {
		t.Fatal("PublishSite error = nil, want error")
	}
	if len(invalidator.calls) != 0 {
		t.Fatalf("invalidations = %v, want none", invalidator.calls)
	}
}

func TestServiceLookupRouteDefaultsToStatic(t *testing.T) {
	invalidator := &releaseInvalidator{manifests: []domain.CurrentSiteManifest{{Site: "foo", Version: 3}}}
	service := NewService(&releaseRepo{}, invalidator)

	decision, ok, err := service.LookupRoute(context.Background(), "foo", "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteStatic || decision.Version != 3 || decision.Path != "/docs/" {
		t.Fatalf("decision = %+v ok=%v, want static route for current release", decision, ok)
	}
}

func TestServiceLookupRouteUsesLongestRoutePrecedence(t *testing.T) {
	invalidator := &releaseInvalidator{manifests: []domain.CurrentSiteManifest{{
		Site: "foo", Version: 3, Settings: map[string]string{
			appsettings.SettingRoutes: `[{"path":"/","kind":"static"},{"path":"/api","kind":"http"},{"path":"/api/socket","kind":"websocket"}]`,
		},
	}}}
	service := NewService(&releaseRepo{}, invalidator)

	decision, ok, err := service.LookupRoute(context.Background(), "foo", "/api/socket/connect")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteWebSocket || decision.Path != "/api/socket/connect" {
		t.Fatalf("decision = %+v ok=%v, want websocket route by longest prefix", decision, ok)
	}
}

func TestServiceLookupRouteIncludesStaticRootAndMatchedRoutePath(t *testing.T) {
	invalidator := &releaseInvalidator{manifests: []domain.CurrentSiteManifest{{
		Site: "foo", Version: 3, Settings: map[string]string{
			appsettings.SettingRoutes: `[{"path":"/","kind":"static","root":"public"},{"path":"/assets","kind":"static","root":"dist/assets"}]`,
		},
	}}}
	service := NewService(&releaseRepo{}, invalidator)

	decision, ok, err := service.LookupRoute(context.Background(), "foo", "/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteStatic || decision.Path != "/assets/app.css" || decision.RoutePath != "/assets" || decision.StaticRoot != "dist/assets" {
		t.Fatalf("decision = %+v ok=%v, want static route root from longest matching route", decision, ok)
	}
}

func TestServiceLookupRouteUsesRuntimeMetadata(t *testing.T) {
	invalidator := &releaseInvalidator{
		manifests: []domain.CurrentSiteManifest{{
			Site: "foo", SiteSHA: "foo-sha", Version: 3, Settings: map[string]string{
				appsettings.SettingRoutes: `[{"path":"/","kind":"static"}]`,
			},
		}},
		runtimeRoutes: []appruntime.RouteMetadata{{
			Site: "foo", SiteSHA: "foo-sha", Version: 3, RoutePath: "/api", RouteKind: appruntime.RouteHTTP, Methods: []string{"GET"},
		}},
	}
	service := NewService(&releaseRepo{}, invalidator)

	decision, ok, err := service.LookupRoute(context.Background(), "foo", "/api/users")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteHTTP || decision.Path != "/api/users" || !reflect.DeepEqual(decision.Methods, []string{"GET"}) {
		t.Fatalf("decision = %+v ok=%v, want runtime HTTP route with methods", decision, ok)
	}
}

func TestServiceLookupRouteUnknownPathFallsBackToStatic(t *testing.T) {
	invalidator := &releaseInvalidator{manifests: []domain.CurrentSiteManifest{{
		Site: "foo", Version: 3, Settings: map[string]string{
			appsettings.SettingRoutes: `[{"path":"/api","kind":"http"}]`,
		},
	}}}
	service := NewService(&releaseRepo{}, invalidator)

	decision, ok, err := service.LookupRoute(context.Background(), "foo", "/missing")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteStatic {
		t.Fatalf("decision = %+v ok=%v, want static fallback", decision, ok)
	}
}

type releaseRepo struct {
	revisions []domain.RevisionRecord
	err       error
}

func (r *releaseRepo) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	return nil, r.err
}

func (r *releaseRepo) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	return nil, r.err
}

func (r *releaseRepo) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	return r.revisions, r.err
}

func (r *releaseRepo) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{RolledBack: true}, r.err
}

func (r *releaseRepo) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	return domain.UnpublishRecord{Unpublished: true}, r.err
}

func (r *releaseRepo) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	return domain.PublishRecord{Published: true}, r.err
}

func (r *releaseRepo) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	return true, r.err
}

type releaseInvalidator struct {
	calls         []string
	manifests     []domain.CurrentSiteManifest
	runtimeRoutes []appruntime.RouteMetadata
}

func (i *releaseInvalidator) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return i.manifests, nil
}

func (i *releaseInvalidator) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	return i.runtimeRoutes, nil
}

func (i *releaseInvalidator) InvalidateSite(ctx context.Context, site string) error {
	i.calls = append(i.calls, "site:"+site)
	return nil
}

func (i *releaseInvalidator) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	i.calls = append(i.calls, "version:"+siteSHA+":"+strconv.FormatInt(version, 10))
	return nil
}

func assertReleaseContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("values = %v, want %q", values, want)
}
