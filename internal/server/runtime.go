package server

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

type forbiddenPolicyError struct {
	err error
}

func (e forbiddenPolicyError) Error() string {
	return e.err.Error()
}

func (e forbiddenPolicyError) Unwrap() error {
	return e.err
}
