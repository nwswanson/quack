package releases

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"quack/internal/domain"
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
	calls []string
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
