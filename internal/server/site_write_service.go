package server

import (
	"context"
	"log/slog"
)

type SiteWriteService interface {
	SaveServerSettings(ctx context.Context, settings ServerSettings) error
	SavePolicy(ctx context.Context, policy PolicyRecord) error
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
	FinishUpload(ctx context.Context, upload UploadRecord) error
	RollbackSite(ctx context.Context, user AdminUser, site string, siteSHA string) (RollbackRecord, error)
	UnpublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (UnpublishRecord, error)
	PublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (PublishRecord, error)
	DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error)
	ReconcilePolicyViolations(ctx context.Context) error
}

type siteWriteDatabase interface {
	SaveServerSettings(ctx context.Context, settings ServerSettings) error
	SavePolicy(ctx context.Context, policy PolicyRecord) error
	SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error
	FinishUpload(ctx context.Context, upload UploadRecord) error
	RollbackSite(ctx context.Context, user AdminUser, site string, siteSHA string) (RollbackRecord, error)
	UnpublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (UnpublishRecord, error)
	PublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (PublishRecord, error)
	DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error)
	SavePolicyViolation(ctx context.Context, violation PolicyViolation) error
	ResolvePolicyViolation(ctx context.Context, siteSHA string, version int64, key string) error
}

type siteWriteService struct {
	db          siteWriteDatabase
	hot         HotDataReader
	invalidator HotDataInvalidator
}

func NewSiteWriteService(db siteWriteDatabase, hot HotDataReader, invalidator HotDataInvalidator) SiteWriteService {
	if invalidator == nil {
		invalidator = NewNoopHotDataInvalidator()
	}
	return siteWriteService{db: db, hot: hot, invalidator: invalidator}
}

func (s siteWriteService) SaveServerSettings(ctx context.Context, settings ServerSettings) error {
	if err := s.db.SaveServerSettings(ctx, settings); err != nil {
		return err
	}
	s.logInvalidation(ctx, "server_settings", s.invalidator.InvalidateServerSettings(ctx))
	return nil
}

func (s siteWriteService) SavePolicy(ctx context.Context, policy PolicyRecord) error {
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

func (s siteWriteService) FinishUpload(ctx context.Context, upload UploadRecord) error {
	if err := s.db.FinishUpload(ctx, upload); err != nil {
		return err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, upload.Site))
	s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, upload.SiteSHA, upload.Version))
	return nil
}

func (s siteWriteService) RollbackSite(ctx context.Context, user AdminUser, site string, siteSHA string) (RollbackRecord, error) {
	record, err := s.db.RollbackSite(ctx, user, site, siteSHA)
	if err != nil {
		return RollbackRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s siteWriteService) UnpublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (UnpublishRecord, error) {
	record, err := s.db.UnpublishSite(ctx, user, site, siteSHA)
	if err != nil {
		return UnpublishRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s siteWriteService) PublishSite(ctx context.Context, user AdminUser, site string, siteSHA string) (PublishRecord, error) {
	record, err := s.db.PublishSite(ctx, user, site, siteSHA)
	if err != nil {
		return PublishRecord{}, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	return record, nil
}

func (s siteWriteService) DeleteSite(ctx context.Context, site string, siteSHA string) (bool, error) {
	deleted, err := s.db.DeleteSite(ctx, site, siteSHA)
	if err != nil {
		return false, err
	}
	s.logInvalidation(ctx, "site", s.invalidator.InvalidateSite(ctx, site))
	s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, siteSHA, 0))
	return deleted, nil
}

func (s siteWriteService) ReconcilePolicyViolations(ctx context.Context) error {
	manifests, err := s.hot.ListCurrentSiteManifests(ctx)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		enabled := parseBoolSetting(manifest.Settings[SettingDatabaseFeature])
		required := parseBoolSetting(manifest.Settings[SettingDatabaseFeatureRequired])
		allowed, reason, err := databaseAllowed(ctx, s.hot, AdminUser{}, manifest.Site)
		if err != nil {
			return err
		}
		if enabled && !allowed {
			severity := "degraded"
			if required {
				severity = "suspended"
			}
			if reason == "" {
				reason = "database is disabled by administrator policy"
			}
			if err := s.db.SavePolicyViolation(ctx, PolicyViolation{
				SiteSHA: manifest.SiteSHA, UploadVersion: manifest.Version, Key: SettingDatabaseFeature,
				RequestedValue: "true", PolicyValue: "deny", Severity: severity, Reason: reason,
			}); err != nil {
				return err
			}
			s.logInvalidation(ctx, "site_version", s.invalidator.InvalidateSiteVersion(ctx, manifest.SiteSHA, manifest.Version))
			continue
		}
		if err := s.db.ResolvePolicyViolation(ctx, manifest.SiteSHA, manifest.Version, SettingDatabaseFeature); err != nil {
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
