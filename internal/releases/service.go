package releases

import (
	"context"
	"log/slog"

	"quack/internal/domain"
)

type Repository interface {
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error)
	ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error)
	ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error)
	RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error)
	RollbackSiteToVersion(ctx context.Context, user domain.AdminUser, site string, siteSHA string, version int64) (domain.RollbackRecord, error)
	UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error)
	PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error)
	DeleteSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (bool, error)
}

type Invalidator interface {
	InvalidateSite(ctx context.Context, site string) error
	InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error
}

type Service interface {
	ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error)
	ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error)
	ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error)
	RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error)
	RollbackSiteToVersion(ctx context.Context, user domain.AdminUser, site string, siteSHA string, version int64) (domain.RollbackRecord, error)
	UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error)
	PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error)
	DeleteSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (bool, error)
	LookupRoute(ctx context.Context, site string, urlPath string) (RouteDecision, bool, error)
}

type service struct {
	repo          Repository
	routes        RouteSource
	runtimeRoutes RuntimeRouteSource
	invalidator   Invalidator
}

func NewService(repo Repository, invalidator Invalidator) Service {
	routes, _ := invalidator.(RouteSource)
	if routes == nil {
		routes, _ = repo.(RouteSource)
	}
	runtimeRoutes, _ := invalidator.(RuntimeRouteSource)
	if runtimeRoutes == nil {
		runtimeRoutes, _ = repo.(RuntimeRouteSource)
	}
	return service{repo: repo, routes: routes, runtimeRoutes: runtimeRoutes, invalidator: invalidator}
}

func (s service) ListPublishedSites(ctx context.Context, userID int64, includeAll bool) ([]domain.PublishedSite, error) {
	return s.repo.ListPublishedSites(ctx, userID, includeAll)
}

func (s service) ListPublishedSitesByUsername(ctx context.Context, username string) ([]domain.PublishedSite, error) {
	return s.repo.ListPublishedSitesByUsername(ctx, username)
}

func (s service) ListSiteRevisions(ctx context.Context, user domain.AdminUser, site string, siteSHA string) ([]domain.RevisionRecord, error) {
	return s.repo.ListSiteRevisions(ctx, user, site, siteSHA)
}

func (s service) RollbackSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.RollbackRecord, error) {
	record, err := s.repo.RollbackSite(ctx, user, site, siteSHA)
	if err != nil {
		return domain.RollbackRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s service) RollbackSiteToVersion(ctx context.Context, user domain.AdminUser, site string, siteSHA string, version int64) (domain.RollbackRecord, error) {
	record, err := s.repo.RollbackSiteToVersion(ctx, user, site, siteSHA, version)
	if err != nil {
		return domain.RollbackRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s service) UnpublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.UnpublishRecord, error) {
	record, err := s.repo.UnpublishSite(ctx, user, site, siteSHA)
	if err != nil {
		return domain.UnpublishRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s service) PublishSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (domain.PublishRecord, error) {
	record, err := s.repo.PublishSite(ctx, user, site, siteSHA)
	if err != nil {
		return domain.PublishRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s service) DeleteSite(ctx context.Context, user domain.AdminUser, site string, siteSHA string) (bool, error) {
	deleted, err := s.repo.DeleteSite(ctx, user, site, siteSHA)
	if err != nil {
		return false, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, siteSHA, 0))
	return deleted, nil
}

func (s service) logInvalidation(ctx context.Context, key string, err error) {
	if err != nil {
		slog.WarnContext(ctx, "hot data invalidation failed", "key", key, "error", err)
	}
}
