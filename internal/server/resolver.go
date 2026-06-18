package server

import (
	"context"
)

type EffectiveValue[T any] struct {
	Value    T
	Source   string
	Editable bool
	Reason   string
}

type UploadPolicy struct {
	MaxUploadBytes      EffectiveValue[int64]
	MaxUploadFiles      EffectiveValue[int64]
	MaxRetainedVersions EffectiveValue[int64]
}

type SiteRuntimeStatus string

const (
	SiteRuntimeActive            SiteRuntimeStatus = "active"
	SiteRuntimeDegraded          SiteRuntimeStatus = "degraded"
	SiteRuntimeSuspendedByPolicy SiteRuntimeStatus = "suspended_by_policy"
)

type SiteRuntimeDecision struct {
	Status SiteRuntimeStatus
	Reason string
}

type Resolver struct {
	read SiteReadService
}

func NewResolver(db Database, caches ...HotDataCache) Resolver {
	var cache HotDataCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	return Resolver{read: NewSiteReadService(db, cache)}
}

func (r Resolver) ResolveUploadPolicy(ctx context.Context, actor AdminUser, site string) (UploadPolicy, error) {
	return r.read.UploadPolicy(ctx, actor, site)
}

func (r Resolver) ValidateUploadManifest(ctx context.Context, actor AdminUser, site string, manifest SiteManifest) error {
	return r.read.ValidateUploadManifest(ctx, actor, site, manifest)
}

func (r Resolver) ResolveCurrentSiteRuntime(ctx context.Context, site string) (SiteRuntimeDecision, error) {
	return r.read.CurrentSiteRuntime(ctx, site)
}

func (r Resolver) ReconcilePolicyViolations(ctx context.Context) error {
	return r.read.ReconcilePolicyViolations(ctx)
}

type forbiddenPolicyError struct {
	err error
}

func (e forbiddenPolicyError) Error() string {
	return e.err.Error()
}

func (e forbiddenPolicyError) Unwrap() error {
	return e.err
}
