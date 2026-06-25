package uploads

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/protocol"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

func TestServiceUploadArchiveFinishesAndPrunes(t *testing.T) {
	db := &uploadServiceDB{prunedVersions: []int64{1, 2}}
	store := &uploadServiceStore{}
	write := &uploadServiceWrite{}
	service := NewService(db, store, uploadServiceRead{}, write)

	resp, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		User: domain.AdminUser{ID: 7, Username: "alice", AdminPriv: "user"},
		Policy: domain.UploadPolicy{
			MaxUploadFiles:      domain.EffectiveValue[int64]{Value: 10},
			MaxRetainedVersions: domain.EffectiveValue[int64]{Value: 3},
		},
		Body: tarArchive(t, map[string]string{
			"index.html": "hello",
			"site.yaml":  "features:\n  database:\n    enabled: true\nroutes:\n  - path: /\n    kind: static\n    root: public\n  - path: /api\n    kind: http\n    entrypoint: main\n    methods: [GET, POST]\n",
		}),
	})
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}
	if !resp.OK || resp.Files != 1 || resp.Bytes != int64(len("hello")) {
		t.Fatalf("response = %#v, want one uploaded file", resp)
	}
	if !write.finished {
		t.Fatal("upload was not finished")
	}
	if got, want := write.settings["features.database.enabled"], "true"; got != want {
		t.Fatalf("manifest setting = %q, want %q", got, want)
	}
	if _, ok := write.settings[appsettings.SettingStaticRoot]; ok {
		t.Fatalf("static root setting was persisted from manifest: %#v", write.settings)
	}
	if got, want := write.settings["routes"], `[{"path":"/","kind":"static","root":"public","entrypoint":""},{"path":"/api","kind":"http","entrypoint":"main","methods":["GET","POST"]}]`; got != want {
		t.Fatalf("routes setting = %q, want %q", got, want)
	}
	if len(db.runtimeRoutes) != 1 {
		t.Fatalf("runtime routes = %#v, want one persisted dynamic route", db.runtimeRoutes)
	}
	route := db.runtimeRoutes[0]
	if route.RoutePath != "/api" || route.RouteKind != appruntime.RouteHTTP || route.Entrypoint != "main" {
		t.Fatalf("runtime route = %#v, want /api http main", route)
	}
	if route.RuntimeKind != appruntime.RuntimeDisabled || route.BundleObjectKey != "" {
		t.Fatalf("runtime route executable fields = %#v, want disabled route without bundle", route)
	}
	if !reflect.DeepEqual(route.Methods, []string{"GET", "POST"}) || !reflect.DeepEqual(route.RequiredCapabilities, []string{"runtime.http"}) {
		t.Fatalf("runtime route policy fields = %#v, want methods and capability", route)
	}
	if db.linkedUserID != 7 || db.linkedSiteSHA == "" {
		t.Fatalf("linked site = (%d, %q), want user 7 and site sha", db.linkedUserID, db.linkedSiteSHA)
	}
	if got, want := store.deletedVersions, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted versions = %#v, want %#v", got, want)
	}
}

func TestRuntimeRoutesFromManifestPersistsStarlarkWebSocketRoute(t *testing.T) {
	upload := domain.UploadRecord{
		Site: "example.com", SiteSHA: "site-sha", Version: 2,
		Files: []domain.UploadFileRecord{{RelativePath: "api/somesocket.star", BlobPath: "socket-blob"}},
	}
	routes, err := RuntimeRoutesFromManifest(upload, manifest.Manifest{
		Routes: []manifest.Route{{
			Path: "/api/somesocket", Kind: manifest.RouteWebSocket, Runtime: "starlark", Entrypoint: "api/somesocket.star",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %#v, want one websocket runtime route", routes)
	}
	route := routes[0]
	if route.RouteKind != appruntime.RouteWebSocket || route.RuntimeKind != appruntime.RuntimeStarlark || route.BundleObjectKey != "socket-blob" {
		t.Fatalf("route = %#v, want starlark websocket metadata", route)
	}
	if route.ExposeErrors {
		t.Fatalf("route = %#v, want errors hidden by default", route)
	}
	if !reflect.DeepEqual(route.RequiredCapabilities, []string{"runtime.websocket"}) {
		t.Fatalf("capabilities = %#v, want runtime.websocket", route.RequiredCapabilities)
	}
}

func TestRuntimeRoutesFromManifestPersistsExposeErrors(t *testing.T) {
	upload := domain.UploadRecord{
		Site: "example.com", SiteSHA: "site-sha", Version: 2,
		Files: []domain.UploadFileRecord{{RelativePath: "api/app.star", BlobPath: "blob:app"}},
	}
	expose := true
	routes, err := RuntimeRoutesFromManifest(upload, manifest.Manifest{
		Routes: []manifest.Route{{
			Path: "/api", Kind: manifest.RouteHTTP, Runtime: "starlark", Entrypoint: "api/app.star", ExposeErrors: &expose,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || !routes[0].ExposeErrors {
		t.Fatalf("routes = %#v, want exposed error metadata", routes)
	}
}

func TestRuntimeRoutesFromManifestAddsCameraCapabilityForLogicalDeclarations(t *testing.T) {
	upload := domain.UploadRecord{
		Site: "example.com", SiteSHA: "site-sha", Version: 2,
		Files: []domain.UploadFileRecord{{RelativePath: "api/app.star", BlobPath: "blob:app"}},
	}
	routes, err := RuntimeRoutesFromManifest(upload, manifest.Manifest{
		Capabilities: manifest.Capabilities{
			Camera: map[string]manifest.CameraCapability{"front_door": {}},
		},
		Routes: []manifest.Route{{
			Path: "/api", Kind: manifest.RouteHTTP, Runtime: "starlark", Entrypoint: "api/app.star",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(routes[0].RequiredCapabilities, []string{"runtime.http", "hardware.camera"}) {
		t.Fatalf("capabilities = %#v, want runtime HTTP and camera", routes[0].RequiredCapabilities)
	}
}

func TestManifestSettingsPersistsAPIProxies(t *testing.T) {
	settings := ManifestSettings(manifest.Manifest{
		APIProxies: []manifest.APIProxy{{Name: "api", Domain: "api.example.com", Methods: []string{"GET"}}},
	})
	if got := settings[appsettings.SettingRuntimeHTTPClientAPIProxies]; got == "" || !strings.Contains(got, `"name":"api"`) {
		t.Fatalf("api proxy setting = %q, want marshaled proxies", got)
	}
}

func TestServiceUploadArchiveCountsOnlyTarEntriesAfterExclusions(t *testing.T) {
	dir := t.TempDir()
	writeUploadArchiveFile(t, dir, "site.yml", "exclude:\n  - \"*.swp\"\n  - \"node_modules\"\n")
	writeUploadArchiveFile(t, dir, "index.html", "hello")
	writeUploadArchiveFile(t, dir, "draft.swp", "swap")
	writeUploadArchiveFile(t, dir, "node_modules/pkg/index.js", "module")

	var body bytes.Buffer
	if err := protocol.WriteTarWithOptions(context.Background(), dir, &body, protocol.WriteTarOptions{
		Exclude: []string{"*.swp", "node_modules"},
	}); err != nil {
		t.Fatal(err)
	}

	db := &uploadServiceDB{}
	store := &uploadServiceStore{}
	service := NewService(db, store, uploadServiceRead{}, &uploadServiceWrite{})
	resp, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		Policy: domain.UploadPolicy{
			MaxUploadFiles: domain.EffectiveValue[int64]{Value: 10},
		},
		Body: bytes.NewReader(body.Bytes()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Files != 1 || resp.Bytes != int64(len("hello")) {
		t.Fatalf("response = %#v, want only non-excluded file counted", resp)
	}
}

func writeUploadArchiveFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestServiceUploadArchivePersistsStarlarkRuntimeBundleMetadata(t *testing.T) {
	db := &uploadServiceDB{}
	store := &uploadServiceStore{blobPaths: map[string]string{"api/app.star": "blob:app"}}
	service := NewService(db, store, uploadServiceRead{}, &uploadServiceWrite{})

	_, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		Policy: domain.UploadPolicy{
			MaxUploadFiles: domain.EffectiveValue[int64]{Value: 10},
		},
		Body: tarArchive(t, map[string]string{
			"site.yaml":    "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: api/app.star\n    methods: [GET]\n    filesystem:\n      root: data\n",
			"api/app.star": "def handle(req): return (200, {}, 'ok')\n",
		}),
	})
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}
	if len(db.runtimeRoutes) != 1 {
		t.Fatalf("runtime routes = %#v, want one route", db.runtimeRoutes)
	}
	route := db.runtimeRoutes[0]
	if route.RuntimeKind != appruntime.RuntimeStarlark || route.BundleObjectKey != "blob:app" {
		t.Fatalf("runtime route = %#v, want starlark blob metadata", route)
	}
	if !route.FilesystemEnabled || route.FilesystemRoot != "data" {
		t.Fatalf("runtime route filesystem = (%v, %q), want enabled data root", route.FilesystemEnabled, route.FilesystemRoot)
	}
	if route.ResourceLimits.MaxRequestBytes != appruntime.DefaultMaxRequestBytes ||
		route.ResourceLimits.MaxResponseBytes != appruntime.DefaultMaxResponseBytes ||
		route.ResourceLimits.MaxDurationMillis != appruntime.DefaultMaxDuration.Milliseconds() ||
		route.ResourceLimits.MaxMemoryBytes != appruntime.DefaultMaxMemoryBytes ||
		route.ResourceLimits.MaxConcurrency != appruntime.DefaultMaxConcurrentInvocations ||
		route.ResourceLimits.MaxExecutionSteps != appruntime.DefaultMaxExecutionSteps {
		t.Fatalf("resource limits = %#v, want runtime defaults", route.ResourceLimits)
	}
}

func TestServiceUploadArchiveRejectsMissingStarlarkEntrypoint(t *testing.T) {
	db := &uploadServiceDB{}
	service := NewService(db, &uploadServiceStore{}, uploadServiceRead{}, &uploadServiceWrite{})

	_, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		Policy: domain.UploadPolicy{
			MaxUploadFiles: domain.EffectiveValue[int64]{Value: 10},
		},
		Body: tarArchive(t, map[string]string{
			"site.yaml": "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: missing.star\n",
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "runtime entrypoint") {
		t.Fatalf("UploadArchive error = %v, want missing runtime entrypoint error", err)
	}
	if db.failedReason == "" {
		t.Fatal("failed upload was not marked failed")
	}
}

func TestServiceUploadArchiveMarksFailedWhenManifestRejected(t *testing.T) {
	db := &uploadServiceDB{}
	service := NewService(db, &uploadServiceStore{}, uploadServiceRead{validateErr: errRejectedManifest}, &uploadServiceWrite{})

	_, err := service.UploadArchive(context.Background(), Request{
		Site: "example.com",
		Policy: domain.UploadPolicy{
			MaxUploadFiles: domain.EffectiveValue[int64]{Value: 10},
		},
		Body: tarArchive(t, map[string]string{"index.html": "hello"}),
	})
	if !errors.Is(err, errRejectedManifest) {
		t.Fatalf("UploadArchive error = %v, want rejected manifest", err)
	}
	if db.failedReason == "" {
		t.Fatal("failed upload was not marked failed")
	}
}

var errRejectedManifest = errors.New("manifest rejected")

type uploadServiceDB struct {
	prunedVersions []int64
	failedReason   string
	linkedUserID   int64
	linkedSiteSHA  string
	runtimeRoutes  []appruntime.RouteMetadata
}

func (db *uploadServiceDB) BeginUpload(ctx context.Context, site string, siteSHA string, publisherUserID int64, publisherIsAdmin bool) (domain.UploadRecord, error) {
	return domain.UploadRecord{Site: site, SiteSHA: siteSHA, Version: 4, State: domain.UploadStateUploading}, nil
}

func (db *uploadServiceDB) FailUpload(ctx context.Context, upload domain.UploadRecord, reason string) error {
	db.failedReason = reason
	return nil
}

func (db *uploadServiceDB) PruneSiteVersions(ctx context.Context, siteSHA string, maxRetainedVersions int64) ([]int64, error) {
	return db.prunedVersions, nil
}

func (db *uploadServiceDB) LinkUserSite(ctx context.Context, userID int64, siteSHA string) error {
	db.linkedUserID = userID
	db.linkedSiteSHA = siteSHA
	return nil
}

func (db *uploadServiceDB) SaveRuntimeRoutes(ctx context.Context, siteSHA string, version int64, routes []appruntime.RouteMetadata) error {
	db.runtimeRoutes = append([]appruntime.RouteMetadata(nil), routes...)
	return nil
}

type uploadServiceStore struct {
	deletedVersions []int64
	blobPaths       map[string]string
}

func (s *uploadServiceStore) AcceptFile(ctx context.Context, file appstorage.StoredFile) (appstorage.StoredFileResult, error) {
	n, err := io.Copy(io.Discard, file.Body)
	if err != nil {
		return appstorage.StoredFileResult{}, err
	}
	blobPath := "blob"
	if s.blobPaths != nil && s.blobPaths[file.RelativePath] != "" {
		blobPath = s.blobPaths[file.RelativePath]
	}
	return appstorage.StoredFileResult{BlobPath: blobPath, FileSHA: "sha", Bytes: n}, nil
}

func (s *uploadServiceStore) OpenBlob(ctx context.Context, blobPath string) (file *os.File, err error) {
	return nil, errors.New("not implemented")
}

func (s *uploadServiceStore) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	s.deletedVersions = append(s.deletedVersions, version)
	return nil
}

func (s *uploadServiceStore) DeleteSite(ctx context.Context, siteSHA string) error {
	return nil
}

type uploadServiceRead struct {
	validateErr error
}

func (r uploadServiceRead) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return domain.ServerSettings{}, nil
}

func (r uploadServiceRead) UploadPolicy(ctx context.Context, actor domain.AdminUser, site string) (domain.UploadPolicy, error) {
	return domain.UploadPolicy{}, nil
}

func (r uploadServiceRead) ValidateUploadManifest(ctx context.Context, actor domain.AdminUser, site string, siteManifest manifest.Manifest) error {
	return r.validateErr
}

func (r uploadServiceRead) CurrentSiteServingStatus(ctx context.Context, site string) (domain.SiteServingDecision, error) {
	return domain.SiteServingDecision{}, nil
}

func (r uploadServiceRead) CurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return domain.UploadFileRecord{}, false, false, nil
}

func (r uploadServiceRead) ServeSiteFile(ctx context.Context, site string, urlPath string, routePath string, staticRoot string, staticFile string) (sites.ServeSiteFileDecision, error) {
	return sites.ServeSiteFileDecision{}, nil
}

func (r uploadServiceRead) SystemDatabasePolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

func (r uploadServiceRead) SystemRuntimeHTTPPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

func (r uploadServiceRead) SystemRuntimeHTTPClientPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

func (r uploadServiceRead) SystemRuntimeWebSocketPolicy(ctx context.Context) (domain.PolicyRecord, error) {
	return domain.PolicyRecord{}, nil
}

type uploadServiceWrite struct {
	finished bool
	settings map[string]string
}

func (w *uploadServiceWrite) SaveServerSettings(ctx context.Context, settings domain.ServerSettings) error {
	return nil
}

func (w *uploadServiceWrite) SavePolicy(ctx context.Context, policy domain.PolicyRecord) error {
	return nil
}

func (w *uploadServiceWrite) SaveUploadSettings(ctx context.Context, siteSHA string, version int64, settings map[string]string) error {
	w.settings = settings
	return nil
}

func (w *uploadServiceWrite) FinishUpload(ctx context.Context, upload domain.UploadRecord) error {
	w.finished = true
	return nil
}

func (w *uploadServiceWrite) ReconcilePolicyViolations(ctx context.Context) error {
	return nil
}

func tarArchive(t *testing.T, files map[string]string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}
