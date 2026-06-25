package secrets

import (
	"context"
	"errors"
	"testing"

	"quack/internal/domain"
	"quack/internal/sites"
)

func TestServiceUnlockSetGetAndReset(t *testing.T) {
	ctx := context.Background()
	repo := &memoryRepo{sites: map[string]bool{sites.HashName("example"): true}}
	svc := NewService(repo)
	user := domain.AdminUser{ID: 1, Username: "alice"}

	if err := svc.Set(ctx, user, "example", domain.SecretScopeSite, "api_key", "before"); !errors.Is(err, domain.ErrSecretsLocked) {
		t.Fatalf("set before unlock error = %v, want ErrSecretsLocked", err)
	}
	if err := svc.Initialize(ctx, "old-password", user.ID); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if !svc.Unlocked() {
		t.Fatal("service should be unlocked after initialize")
	}
	if err := svc.Set(ctx, user, "example", domain.SecretScopeSite, "api_key", "secret-value"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := svc.Get(ctx, "example", domain.SecretScopeSite, "api_key")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "secret-value" {
		t.Fatalf("get = %q, want secret-value", got)
	}
	if err := svc.ResetPassphrase(ctx, "old-password", "new-password", user.ID); err != nil {
		t.Fatalf("reset passphrase: %v", err)
	}

	restarted := NewService(repo)
	if err := restarted.Unlock(ctx, "old-password"); err == nil {
		t.Fatal("old password should not unlock after reset")
	}
	if err := restarted.Unlock(ctx, "new-password"); err != nil {
		t.Fatalf("new password unlock: %v", err)
	}
	got, err = restarted.Get(ctx, "example", domain.SecretScopeSite, "api_key")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got != "secret-value" {
		t.Fatalf("get after restart = %q, want secret-value", got)
	}
}

func TestServiceSetRequiresExistingSite(t *testing.T) {
	ctx := context.Background()
	repo := &memoryRepo{}
	svc := NewService(repo)
	admin := domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}

	if err := svc.Initialize(ctx, "password", admin.ID); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := svc.Set(ctx, admin, "missing", domain.SecretScopeSite, "hello", "world"); err == nil {
		t.Fatal("set for missing site succeeded, want error")
	}
}

type memoryRepo struct {
	keys    []UnlockKeyRecord
	secrets map[string]SecretRecord
	sites   map[string]bool
}

func (r *memoryRepo) LoadUnlockKeys(ctx context.Context) ([]UnlockKeyRecord, error) {
	return append([]UnlockKeyRecord(nil), r.keys...), nil
}

func (r *memoryRepo) SaveUnlockKey(ctx context.Context, record UnlockKeyRecord) error {
	for i := range r.keys {
		if r.keys[i].KeyID == record.KeyID {
			r.keys[i] = record
			return nil
		}
	}
	r.keys = append(r.keys, record)
	return nil
}

func (r *memoryRepo) UpsertSecret(ctx context.Context, record SecretRecord) error {
	if r.secrets == nil {
		r.secrets = map[string]SecretRecord{}
	}
	r.secrets[testSecretKey(record.Scope, record.ScopeID, record.Name)] = record
	return nil
}

func (r *memoryRepo) GetSecret(ctx context.Context, scope domain.SecretScope, scopeID string, name string) (SecretRecord, bool, error) {
	record, ok := r.secrets[testSecretKey(scope, scopeID, name)]
	return record, ok, nil
}

func (r *memoryRepo) ListSecretsForUser(ctx context.Context, userID int64, siteSHA string) ([]SecretRecord, error) {
	return nil, nil
}

func (r *memoryRepo) DeleteSecretForUser(ctx context.Context, userID int64, scope domain.SecretScope, scopeID string, name string) (bool, error) {
	return false, nil
}

func (r *memoryRepo) SiteExists(ctx context.Context, siteSHA string) (bool, error) {
	return r.sites[siteSHA], nil
}

func (r *memoryRepo) UserCanAccessSite(ctx context.Context, userID int64, siteSHA string) (bool, error) {
	return siteSHA == sites.HashName("example"), nil
}

func testSecretKey(scope domain.SecretScope, scopeID string, name string) string {
	return string(scope) + "\x00" + scopeID + "\x00" + name
}
