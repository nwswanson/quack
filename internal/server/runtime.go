package server

import "quack/internal/domain"

type EffectiveValue[T any] = domain.EffectiveValue[T]

type UploadPolicy = domain.UploadPolicy

type SiteRuntimeStatus = domain.SiteRuntimeStatus

const (
	SiteRuntimeActive            = domain.SiteRuntimeActive
	SiteRuntimeDegraded          = domain.SiteRuntimeDegraded
	SiteRuntimeSuspendedByPolicy = domain.SiteRuntimeSuspendedByPolicy
)

type SiteRuntimeDecision = domain.SiteRuntimeDecision

type forbiddenPolicyError struct {
	err error
}

func (e forbiddenPolicyError) Error() string {
	return e.err.Error()
}

func (e forbiddenPolicyError) Unwrap() error {
	return e.err
}
