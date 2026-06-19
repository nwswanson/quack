package hotdata

import (
	"context"
	"strings"
	"testing"
	"time"

	"quack/internal/domain"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
)

func BenchmarkHotDataReaderFindCurrentSiteFile(b *testing.B) {
	source := staticHotDataReader{
		file: domain.UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        40643,
		},
		fileOK:     true,
		siteExists: true,
	}
	benchCurrentFileReader(b, "passthrough", NewPassthroughHotDataReader(source))
	benchCurrentFileReader(b, "memory", NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
	benchCurrentFileReader(b, "otter", NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func BenchmarkHotDataReaderServeMetadata(b *testing.B) {
	source := staticHotDataReader{
		settings: domain.ServerSettings{DefaultSite: "", MaxUploadBytes: appsettings.DefaultMaxUploadBytes, MaxUploadFiles: appsettings.DefaultMaxUploadFiles},
		manifests: []domain.CurrentSiteManifest{{
			Site:     "example.com",
			SiteSHA:  "site-sha",
			Version:  1,
			Settings: map[string]string{appsettings.SettingDatabaseFeature: "false", appsettings.SettingDatabaseFeatureRequired: "false"},
		}},
		file: domain.UploadFileRecord{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        40643,
		},
		fileOK:     true,
		siteExists: true,
	}
	benchServeMetadataReader(b, "passthrough", NewPassthroughHotDataReader(source))
	benchServeMetadataReader(b, "memory", NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
	benchServeMetadataReader(b, "otter", NewOtterHotDataReader(source, OtterHotDataReaderOptions{TTL: time.Minute, NegativeTTL: time.Minute}))
}

func benchCurrentFileReader(b *testing.B, name string, reader HotDataReader) {
	ctx := context.Background()
	if _, _, _, err := reader.FindCurrentSiteFile(ctx, "example.com", "index.html"); err != nil {
		b.Fatal(err)
	}
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				file, ok, siteExists, err := reader.FindCurrentSiteFile(ctx, "example.com", "index.html")
				if err != nil || !ok || !siteExists || file.BlobPath == "" {
					b.Fatalf("FindCurrentSiteFile = (%+v, %v, %v, %v)", file, ok, siteExists, err)
				}
			}
		})
	})
}

func benchServeMetadataReader(b *testing.B, name string, reader HotDataReader) {
	ctx := context.Background()
	read := sites.NewSiteReadService(reader)
	if _, err := read.ServerSettings(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := read.CurrentSiteRuntime(ctx, "example.com"); err != nil {
		b.Fatal(err)
	}
	if _, _, _, err := read.CurrentSiteFile(ctx, "example.com", "index.html"); err != nil {
		b.Fatal(err)
	}
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := read.ServerSettings(ctx); err != nil {
					b.Fatal(err)
				}
				if _, err := read.CurrentSiteRuntime(ctx, "example.com"); err != nil {
					b.Fatal(err)
				}
				file, ok, siteExists, err := read.CurrentSiteFile(ctx, "example.com", "index.html")
				if err != nil || !ok || !siteExists || file.BlobPath == "" {
					b.Fatalf("CurrentSiteFile = (%+v, %v, %v, %v)", file, ok, siteExists, err)
				}
			}
		})
	})
}

type staticHotDataReader struct {
	settings   domain.ServerSettings
	manifests  []domain.CurrentSiteManifest
	file       domain.UploadFileRecord
	fileOK     bool
	siteExists bool
}

func (r staticHotDataReader) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return r.settings, nil
}

func (r staticHotDataReader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	return nil, nil
}

func (r staticHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	return nil, nil
}

func (r staticHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return r.manifests, nil
}

func (r staticHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	return nil, nil
}

func (r staticHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return r.file, r.fileOK, r.siteExists, nil
}

func (r staticHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	if !r.fileOK {
		return nil, r.siteExists, nil
	}
	return []domain.UploadFileRecord{r.file}, r.siteExists, nil
}

func (r staticHotDataReader) ServeSiteFile(ctx context.Context, site string, urlPath string) (sites.ServeSiteFileDecision, error) {
	return sites.ResolveSiteFile(ctx, r, site, urlPath, strings.TrimSpace(r.settings.DefaultSite), false)
}
