package sites

import (
	"context"
	"log/slog"

	"quack/internal/domain"
	"quack/internal/policy"
	appsettings "quack/internal/settings"
)

type SiteWriteService interface {
	SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error
	SavePolicy(ctx context.Context, policy domain.PolicyRecord) error
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
	FinishUpload(ctx context.Context, upload domain.UploadRecord) error
	ReconcilePolicyViolations(ctx context.Context) error
}

type SiteWriteRepository interface {
	SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error
	SavePolicy(ctx context.Context, policy domain.PolicyRecord) error
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
	FinishUpload(ctx context.Context, upload domain.UploadRecord) error
	SavePolicyViolation(ctx context.Context, violation domain.PolicyViolation) error
	ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error
}

type HotDataInvalidator interface {
	InvalidateServerSettings(ctx context.Context) error
	InvalidateSite(ctx context.Context, site string) error
	InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error
	InvalidatePolicies(ctx context.Context) error
}

func NewNoopHotDataInvalidator() HotDataInvalidator {
	return noopHotDataInvalidator{}
}

type noopHotDataInvalidator struct{}

func (noopHotDataInvalidator) InvalidateServerSettings(ctx context.Context) error {
	return nil
}

func (noopHotDataInvalidator) InvalidateSite(ctx context.Context, site string) error {
	return nil
}

func (noopHotDataInvalidator) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	return nil
}

func (noopHotDataInvalidator) InvalidatePolicies(ctx context.Context) error {
	return nil
}

type siteWriteService struct {
	db          SiteWriteRepository
	hot         HotDataReader
	invalidator HotDataInvalidator
}

func NewSiteWriteService(db SiteWriteRepository, hot HotDataReader, invalidator HotDataInvalidator) SiteWriteService {
	if invalidator == nil {
		invalidator = NewNoopHotDataInvalidator()
	}
	return siteWriteService{db: db, hot: hot, invalidator: invalidator}
}

func (s siteWriteService) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	if err := s.db.SaveServerSettings(ctx, settings); err != nil {
		return err
	}
	s.logInvalidation(ctx, "server_settings", s.invalidator.InvalidateServerSettings(ctx))
	return nil
}

func (s siteWriteService) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	if err := s.db.SavePolicy(ctx, policy); err != nil {
		return err
	}
	s.logInvalidation(ctx, "policies", s.invalidator.InvalidatePolicies(ctx))
	return nil
}

func (s siteWriteService) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	if err := s.db.SaveUploadSettings(ctx, siteSHA, version, settings); err != nil {
		return err
	}
	s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, siteSHA, version))
	return nil
}

func (s siteWriteService) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
	if err := s.db.FinishUpload(ctx, upload); err != nil {
		return err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, upload.Site))
	s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, upload.SiteSHA, upload.Version))
	return nil
}

func (s siteWriteService) ReconcilePolicyViolations(ctx context.Context) error {
	manifests, err := s.hot.ListCurrentSiteManifests(ctx)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		allowed, reason, err := policy.DatabaseAllowed(ctx, s.hot, domain.AdminUser{}, manifest.Site)
		if err != nil {
			return err
		}
		violation, violated := policy.DatabaseViolationFromSettings(manifest.Settings, allowed, reason)
		if violated {
			violation.SiteSHA = manifest.SiteSHA
			violation.UploadVersion = manifest.Version
			if err := s.db.SavePolicyViolation(ctx, violation); err != nil {
				return err
			}
			s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, manifest.SiteSHA, manifest.Version))
			continue
		}
		if err := s.db.ResolvePolicyViolation(ctx, manifest.SiteSHA, manifest.Version, appsettings.SettingDatabaseFeature); err != nil {
			return err
		}
		s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, manifest.SiteSHA, manifest.Version))
	}
	return nil
}

func (s siteWriteService) logInvalidation(ctx context.Context, key string, err error) {
	if err != nil {
		slog.WarnContext(ctx, "hot data invalidation failed", "key", key, "error", err)
	}
}
