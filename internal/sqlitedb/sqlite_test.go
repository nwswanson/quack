package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"quack/internal/domain"
	"quack/internal/hardware"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
)

var testPublisherSeq int64

func createUploadPublisher(t *testing.T, ctx context.Context, db *Database) int64 {
	t.Helper()

	seq := atomic.AddInt64(&testPublisherSeq, 1)
	created, err := db.CreateUser(ctx, fmt.Sprintf("upload-publisher-%d", seq), "user")
	if err != nil {
		t.Fatal(err)
	}
	return created.User.ID
}

func TestFinishUploadPersistsMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quack.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{
		{
			RelativePath: "index.html",
			BlobPath:     "blobs/site:site-sha/1/file:file-sha",
			FileSHA:      "file-sha",
			Bytes:        12,
		},
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	var site string
	var version int64
	var state string
	if err := raw.QueryRowContext(ctx, `
		SELECT s.site, s.current_version, u.state
		FROM sites s
		JOIN uploads u ON u.site_sha = s.site_sha AND u.version = s.current_version
		WHERE s.site_sha = ?
	`, upload.SiteSHA).Scan(&site, &version, &state); err != nil {
		t.Fatal(err)
	}
	if site != upload.Site || version != upload.Version {
		t.Fatalf("site row = (%q, %d), want (%q, %d)", site, version, upload.Site, upload.Version)
	}
	if state != string(domain.UploadStateFinished) {
		t.Fatalf("upload state = %q, want %q", state, domain.UploadStateFinished)
	}

	var relativePath string
	var blobPath string
	if err := raw.QueryRowContext(ctx, `
		SELECT uf.relative_path, uf.blob_path
		FROM upload_files uf
		JOIN uploads u ON u.id = uf.upload_id
		WHERE u.site_sha = ? AND u.version = ?
	`, upload.SiteSHA, upload.Version).Scan(&relativePath, &blobPath); err != nil {
		t.Fatal(err)
	}
	if relativePath != upload.Files[0].RelativePath || blobPath != upload.Files[0].BlobPath {
		t.Fatalf("file row = (%q, %q), want (%q, %q)", relativePath, blobPath, upload.Files[0].RelativePath, upload.Files[0].BlobPath)
	}
}

func TestPruneSiteVersionsRemovesOldFinishedVersions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quack.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	for i := 0; i < 4; i++ {
		upload, err := db.BeginUpload(ctx, "example", "site-sha", publisherID, false)
		if err != nil {
			t.Fatal(err)
		}
		upload.Files = []domain.UploadFileRecord{{
			RelativePath: "index.html",
			BlobPath:     fmt.Sprintf("blobs/site:site-sha/%d/file:file-sha", upload.Version),
			FileSHA:      fmt.Sprintf("file-sha-%d", upload.Version),
			Bytes:        upload.Version,
		}}
		if err := db.SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, map[string]string{
			appsettings.SettingDatabaseFeature:         "false",
			appsettings.SettingDatabaseFeatureRequired: "false",
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.SavePolicyViolation(ctx, domain.PolicyViolation{
			SiteSHA: upload.SiteSHA, UploadVersion: upload.Version, Key: appsettings.SettingDatabaseFeature,
			RequestedValue: "true", PolicyValue: "deny", Severity: "degraded", Reason: "test",
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveRuntimeRoutes(ctx, upload.SiteSHA, upload.Version, []appruntime.RouteMetadata{{
			RoutePath: "/api", RouteKind: appruntime.RouteHTTP, RuntimeKind: appruntime.RuntimeDisabled,
		}}); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishUpload(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}

	pruned, err := db.PruneSiteVersions(ctx, "site-sha", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(pruned), "[2 1]"; got != want {
		t.Fatalf("pruned versions = %s, want %s", got, want)
	}

	rows, err := db.readDB.QueryContext(ctx, `SELECT version FROM uploads WHERE site_sha = ? AND state = ? ORDER BY version`, "site-sha", string(domain.UploadStateFinished))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if got, want := fmt.Sprint(versions), "[3 4]"; got != want {
		t.Fatalf("remaining versions = %s, want %s", got, want)
	}

	var uploadSettings int
	if err := db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM upload_settings WHERE site_sha = ? AND upload_version IN (1, 2)`, "site-sha").Scan(&uploadSettings); err != nil {
		t.Fatal(err)
	}
	if uploadSettings != 0 {
		t.Fatalf("old upload settings count = %d, want 0", uploadSettings)
	}
	var violations int
	if err := db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_policy_violations WHERE site_sha = ? AND upload_version IN (1, 2)`, "site-sha").Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("old violations count = %d, want 0", violations)
	}
	var runtimeRoutes int
	if err := db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_routes WHERE site_sha = ? AND upload_version IN (1, 2)`, "site-sha").Scan(&runtimeRoutes); err != nil {
		t.Fatal(err)
	}
	if runtimeRoutes != 0 {
		t.Fatalf("old runtime route count = %d, want 0", runtimeRoutes)
	}
}

func TestRuntimeRoutesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "example", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRuntimeRoutes(ctx, upload.SiteSHA, upload.Version, []appruntime.RouteMetadata{{
		RuntimeKind:          appruntime.RuntimeDisabled,
		RouteKind:            appruntime.RouteHTTP,
		RoutePath:            "/api",
		Entrypoint:           "main.handler",
		BundleObjectKey:      "bundles/site-sha/1/runtime.tar",
		Methods:              []string{"GET", "POST"},
		FilesystemEnabled:    true,
		FilesystemRoot:       "data",
		RequiredCapabilities: []string{"runtime.http", "database"},
		ResourceLimits:       appruntime.ResourceLimits{MaxRequestBytes: 1024, MaxDurationMillis: 250},
	}}); err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{{
		RelativePath: "index.html",
		BlobPath:     "blobs/site:site-sha/1/file:file-sha",
		FileSHA:      "file-sha",
		Bytes:        12,
	}}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	routes, err := db.ListRuntimeRoutes(ctx, upload.SiteSHA, upload.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %#v, want one route", routes)
	}
	route := routes[0]
	if route.Site != "example" || route.SiteSHA != upload.SiteSHA || route.Version != upload.Version {
		t.Fatalf("route identity = %#v, want current upload identity", route)
	}
	if route.RouteKind != appruntime.RouteHTTP || route.RuntimeKind != appruntime.RuntimeDisabled || route.RoutePath != "/api" || route.Entrypoint != "main.handler" || route.BundleObjectKey == "" {
		t.Fatalf("route metadata = %#v, want persisted runtime metadata", route)
	}
	if !reflect.DeepEqual(route.Methods, []string{"GET", "POST"}) || !reflect.DeepEqual(route.RequiredCapabilities, []string{"runtime.http", "database"}) {
		t.Fatalf("route arrays = %#v, want methods and capabilities", route)
	}
	if !route.FilesystemEnabled || route.FilesystemRoot != "data" {
		t.Fatalf("route filesystem = (%v, %q), want enabled data root", route.FilesystemEnabled, route.FilesystemRoot)
	}
	if route.ResourceLimits.MaxRequestBytes != 1024 || route.ResourceLimits.MaxDurationMillis != 250 || route.CreatedAt == "" {
		t.Fatalf("route limits/timestamp = %#v, want persisted limits and created timestamp", route)
	}

	current, err := db.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, routes) {
		t.Fatalf("current routes = %#v, want %#v", current, routes)
	}
}

func TestSavePolicyRejectsInherit(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.SavePolicy(ctx, domain.PolicyRecord{
		ScopeType: domain.ScopeSystem,
		Key:       appsettings.SettingDatabaseFeature,
		Mode:      "inherit",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported policy mode: inherit") {
		t.Fatalf("SavePolicy error = %v, want unsupported inherit mode", err)
	}
}

func TestHardwareDevicesRoundTripAndConfig(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_01",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Label: "Front desk Logitech C270",
		Site:  "acme",
		Alias: "front_door",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_02",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video4",
		Label: "Unbound camera",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "serial_01",
		Kind:  hardware.AdminKindSerial,
		Path:  "/dev/ttyUSB0",
		Label: "Weather station",
		Site:  "acme",
		Alias: "weather_station",
		Serial: hardware.SerialOptions{
			BaudRate:             115200,
			DataBits:             7,
			Parity:               "even",
			StopBits:             "2",
			ReadTimeoutMillis:    250,
			RequestTimeoutMillis: 3000,
			WriteChunkBytes:      128,
			WriteDelayMillis:     5,
			WriteQueueSize:       32,
			RecentEvents:         128,
			ReconnectMillis:      750,
		},
	}); err != nil {
		t.Fatal(err)
	}

	devices, err := db.ListHardwareDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 {
		t.Fatalf("devices = %#v, want three", devices)
	}
	if devices[0].ID != "cam_01" || devices[0].Site != "acme" || devices[0].Alias != "front_door" {
		t.Fatalf("bound device = %+v, want cam_01 bound to acme as front_door", devices[0])
	}
	if devices[1].ID != "cam_02" || devices[1].Site != "" {
		t.Fatalf("unbound device = %+v, want no site", devices[1])
	}
	if devices[2].ID != "serial_01" || devices[2].Serial.BaudRate != 115200 || devices[2].Serial.Parity != "even" || devices[2].Serial.WriteChunkBytes != 128 || devices[2].Serial.WriteDelayMillis != 5 {
		t.Fatalf("serial device = %+v, want serial settings", devices[2])
	}

	config, err := db.HardwareConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Devices) != 3 || len(config.SiteDeviceBindings) != 2 {
		t.Fatalf("config = %+v, want three devices and two bindings", config)
	}
	if got := config.Devices[0]; got.Kind != hardware.DeviceKindCameraUVC || got.Plugin != hardware.AdminKindUVCCamera || got.Path != "/dev/video2" {
		t.Fatalf("device config = %+v, want normalized UVC device", got)
	}
	if got := config.SiteDeviceBindings[0]; got.Site != "acme" || got.Alias != "front_door" || !got.Permissions.Capture {
		t.Fatalf("binding config = %+v, want acme/front_door capture", got)
	}
	if got := config.Devices[2]; got.Kind != hardware.DeviceKindSerial || got.Serial.BaudRate != 115200 || got.Serial.RequestTimeoutMillis != 3000 {
		t.Fatalf("serial config = %+v, want normalized serial device with options", got)
	}
	if got := config.SiteDeviceBindings[1]; got.Site != "acme" || got.Alias != "weather_station" || !got.Permissions.SerialRead || !got.Permissions.SerialWrite {
		t.Fatalf("serial binding config = %+v, want serial permissions", got)
	}
}

func TestSaveHardwareDeviceRejectsDuplicatePath(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_01",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Site:  "acme",
		Alias: "front_door",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_01",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Label: "Updated label",
		Site:  "beta",
		Alias: "back_door",
	}); err != nil {
		t.Fatalf("updating existing device path failed: %v", err)
	}

	err = db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_02",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Site:  "gamma",
		Alias: "side_door",
	})
	if err == nil || !strings.Contains(err.Error(), `path "/dev/video2" is already used by device "cam_01"`) {
		t.Fatalf("SaveHardwareDevice error = %v, want duplicate path rejection", err)
	}
}

func TestSaveHardwareDeviceRenamesDeviceAndBinding(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:    "cam_01",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video0",
		Site:  "acme",
		Alias: "cam_01",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		OriginalID: "cam_01",
		ID:         "video0",
		Kind:       hardware.AdminKindUVCCamera,
		Path:       "/dev/video0",
		Label:      "Renamed camera",
		Site:       "acme",
		Alias:      "front_door",
	}); err != nil {
		t.Fatalf("rename hardware device failed: %v", err)
	}

	devices, err := db.ListHardwareDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %#v, want one renamed device", devices)
	}
	got := devices[0]
	if got.ID != "video0" || got.Path != "/dev/video0" || got.Label != "Renamed camera" || got.Site != "acme" || got.Alias != "front_door" {
		t.Fatalf("renamed device = %+v, want video0 bound as front_door", got)
	}

	config, err := db.HardwareConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Devices) != 1 || config.Devices[0].ID != "video0" || config.Devices[0].Path != "/dev/video0" {
		t.Fatalf("config devices = %+v, want renamed video0", config.Devices)
	}
	if len(config.SiteDeviceBindings) != 1 || config.SiteDeviceBindings[0].DeviceID != "video0" || config.SiteDeviceBindings[0].Alias != "front_door" {
		t.Fatalf("config bindings = %+v, want binding moved to video0", config.SiteDeviceBindings)
	}
}

func TestSaveHardwareDeviceGeneratesID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Label: "Front desk",
		Site:  "acme",
		Alias: "front_door",
	}); err != nil {
		t.Fatal(err)
	}

	devices, err := db.ListHardwareDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || !strings.HasPrefix(devices[0].ID, "hw_") {
		t.Fatalf("devices = %+v, want generated hardware id", devices)
	}
	config, err := db.HardwareConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.SiteDeviceBindings) != 1 || config.SiteDeviceBindings[0].Alias != "front_door" || config.SiteDeviceBindings[0].DeviceID != devices[0].ID {
		t.Fatalf("config bindings = %+v, want binding to generated id with explicit alias", config.SiteDeviceBindings)
	}
}

func TestSaveHardwareDeviceRenamePreservesKind(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		ID:   "serial_01",
		Kind: hardware.AdminKindSerial,
		Path: "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHardwareDevice(ctx, hardware.AdminDevice{
		OriginalID: "serial_01",
		ID:         "serial_renamed",
		Kind:       hardware.AdminKindUVCCamera,
		Path:       "/dev/ttyUSB1",
	}); err != nil {
		t.Fatal(err)
	}

	devices, err := db.ListHardwareDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Kind != hardware.AdminKindSerial {
		t.Fatalf("devices = %+v, want renamed serial device to keep kind", devices)
	}
}

func TestMetricsSnapshotCountsUsersSitesBytesAndRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}

	v1, err := db.BeginUpload(ctx, "example", "site-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRuntimeRoutes(ctx, v1.SiteSHA, v1.Version, []appruntime.RouteMetadata{{
		RoutePath: "/api", RouteKind: appruntime.RouteHTTP, RuntimeKind: appruntime.RuntimeStarlark,
	}}); err != nil {
		t.Fatal(err)
	}
	v1.Files = []domain.UploadFileRecord{{RelativePath: "index.html", BlobPath: "blob-v1", FileSHA: "sha-v1", Bytes: 10}}
	if err := db.FinishUpload(ctx, v1); err != nil {
		t.Fatal(err)
	}

	v2, err := db.BeginUpload(ctx, "example", "site-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRuntimeRoutes(ctx, v2.SiteSHA, v2.Version, []appruntime.RouteMetadata{
		{RoutePath: "/api", RouteKind: appruntime.RouteHTTP, RuntimeKind: appruntime.RuntimeStarlark},
		{RoutePath: "/ws", RouteKind: appruntime.RouteWebSocket, RuntimeKind: appruntime.RuntimeStarlark},
	}); err != nil {
		t.Fatal(err)
	}
	v2.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blob-v2a", FileSHA: "sha-v2a", Bytes: 20},
		{RelativePath: "app.js", BlobPath: "blob-v2b", FileSHA: "sha-v2b", Bytes: 5},
	}
	if err := db.FinishUpload(ctx, v2); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkUserSite(ctx, alice.User.ID, "site-sha"); err != nil {
		t.Fatal(err)
	}

	snapshot, err := db.MetricsSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UserCount != 2 || snapshot.SiteCount != 1 || snapshot.LiveSiteCount != 1 {
		t.Fatalf("snapshot user/site counts = %#v", snapshot)
	}
	if snapshot.UploadCount != 2 || snapshot.FinishedUploadCount != 2 || snapshot.UploadBytes != 35 || snapshot.CurrentSiteBytes != 25 || snapshot.UploadFileCount != 3 {
		t.Fatalf("snapshot upload counts = %#v", snapshot)
	}
	if snapshot.RuntimeRouteCount != 3 || snapshot.RuntimeHTTPRouteCount != 2 || snapshot.RuntimeWebSocketRouteCount != 1 || snapshot.CurrentRuntimeRouteCount != 2 {
		t.Fatalf("snapshot route counts = %#v", snapshot)
	}

	users := map[string]domain.UserMetrics{}
	for _, user := range snapshot.Users {
		users[user.Username] = user
	}
	if got := users["alice"]; got.ID != alice.User.ID || got.SiteCount != 1 || got.VersionCount != 2 || got.Bytes != 35 {
		t.Fatalf("alice metrics = %#v, want linked site storage bytes", got)
	}
	if got := users["bob"]; got.ID != bob.User.ID || got.SiteCount != 0 || got.VersionCount != 0 || got.Bytes != 0 {
		t.Fatalf("bob metrics = %#v, want zero linked storage", got)
	}
	if len(snapshot.Sites) != 1 {
		t.Fatalf("site metrics = %#v, want one site", snapshot.Sites)
	}
	site := snapshot.Sites[0]
	if site.Site != "example" || site.VersionCount != 2 || site.UploadBytes != 35 || site.CurrentBytes != 25 || site.CurrentFiles != 2 || site.RuntimeRoutes != 3 {
		t.Fatalf("site metrics = %#v, want byte and route totals", site)
	}
}

func TestListCurrentRuntimeRoutesUsesCurrentLiveVersion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	for _, path := range []string{"/old", "/current"} {
		upload, err := db.BeginUpload(ctx, "example", "site-sha", publisherID, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveRuntimeRoutes(ctx, upload.SiteSHA, upload.Version, []appruntime.RouteMetadata{{
			RoutePath: path, RouteKind: appruntime.RouteHTTP, RuntimeKind: appruntime.RuntimeDisabled,
		}}); err != nil {
			t.Fatal(err)
		}
		upload.Files = []domain.UploadFileRecord{{
			RelativePath: "index.html",
			BlobPath:     fmt.Sprintf("blobs/site:site-sha/%d/file:file-sha", upload.Version),
			FileSHA:      fmt.Sprintf("file-sha-%d", upload.Version),
			Bytes:        upload.Version,
		}}
		if err := db.FinishUpload(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}

	routes, err := db.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].RoutePath != "/current" || routes[0].Version != 2 {
		t.Fatalf("current runtime routes = %#v, want only current version", routes)
	}

	if _, err := db.UnpublishSite(ctx, domain.AdminUser{AdminPriv: "admin:*"}, "example", "site-sha"); err != nil {
		t.Fatal(err)
	}
	routes, err = db.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("current runtime routes after unpublish = %#v, want none", routes)
	}
}

func TestListSiteRevisionsAndRollbackEnforceOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.AdminUser{ID: 999, Username: "admin", AdminPriv: "admin:*"}

	for i := 0; i < 3; i++ {
		upload, err := db.BeginUpload(ctx, "example", "site-sha", alice.User.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		upload.Files = []domain.UploadFileRecord{{
			RelativePath: "index.html",
			BlobPath:     fmt.Sprintf("blobs/site:site-sha/%d/file:file-sha", upload.Version),
			FileSHA:      fmt.Sprintf("file-sha-%d", upload.Version),
			Bytes:        upload.Version,
		}}
		if err := db.FinishUpload(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.ListSiteRevisions(ctx, bob.User, "example", "site-sha"); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob list error = %v, want ErrSiteOwnership", err)
	}
	if _, err := db.RollbackSite(ctx, bob.User, "example", "site-sha"); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob rollback error = %v, want ErrSiteOwnership", err)
	}
	if _, err := db.RollbackSiteToVersion(ctx, bob.User, "example", "site-sha", 1); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob rollback to version error = %v, want ErrSiteOwnership", err)
	}

	revisions, err := db.ListSiteRevisions(ctx, alice.User, "example", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 3 || !revisions[0].Current || revisions[0].Version != 3 || revisions[1].Version != 2 || revisions[2].Version != 1 {
		t.Fatalf("revisions = %#v, want current v3 then v2 then v1", revisions)
	}
	noop, err := db.RollbackSiteToVersion(ctx, admin, "example", "site-sha", 3)
	if err != nil {
		t.Fatal(err)
	}
	if noop.RolledBack || noop.CurrentVersion != 3 || noop.Warning != "" {
		t.Fatalf("rollback to current = %#v, want clean no-op at v3", noop)
	}

	rollback, err := db.RollbackSite(ctx, admin, "example", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !rollback.RolledBack || rollback.PreviousVersion != 3 || rollback.CurrentVersion != 2 {
		t.Fatalf("rollback = %#v, want v3 to v2", rollback)
	}
	rollback, err = db.RollbackSiteToVersion(ctx, admin, "example", "site-sha", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !rollback.RolledBack || rollback.PreviousVersion != 2 || rollback.CurrentVersion != 1 {
		t.Fatalf("rollback to version = %#v, want v2 to v1", rollback)
	}
	again, err := db.RollbackSite(ctx, alice.User, "example", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if again.RolledBack || again.Warning == "" {
		t.Fatalf("second rollback = %#v, want warning", again)
	}
}

func TestPublishStateControlsServing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}

	upload, err := db.BeginUpload(ctx, "example", "site-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{{
		RelativePath: "index.html",
		BlobPath:     "blobs/site:site-sha/1/file:file-sha",
		FileSHA:      "file-sha",
		Bytes:        12,
	}}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := db.FindCurrentFile(ctx, "example", "index.html"); err != nil || !ok {
		t.Fatalf("FindCurrentFile before unpublish = (_, %v, %v), want file", ok, err)
	}
	if _, err := db.UnpublishSite(ctx, bob.User, "example", "site-sha"); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob unpublish error = %v, want ErrSiteOwnership", err)
	}
	unpublished, err := db.UnpublishSite(ctx, alice.User, "example", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !unpublished.Unpublished || unpublished.LiveState != "unpublished" {
		t.Fatalf("unpublished = %#v, want unpublished state", unpublished)
	}
	if _, ok, err := db.FindCurrentFile(ctx, "example", "index.html"); err != nil || ok {
		t.Fatalf("FindCurrentFile after unpublish = (_, %v, %v), want no file", ok, err)
	}
	manifests, err := db.ListCurrentSiteManifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 0 {
		t.Fatalf("ListCurrentSiteManifests after unpublish = %#v, want none", manifests)
	}
	sites, err := db.ListPublishedSites(ctx, alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].LiveState != "unpublished" {
		t.Fatalf("sites = %#v, want unpublished live state", sites)
	}
	if _, err := db.PublishSite(ctx, bob.User, "example", "site-sha"); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob publish error = %v, want ErrSiteOwnership", err)
	}
	published, err := db.PublishSite(ctx, alice.User, "example", "site-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !published.Published || published.LiveState != "live" {
		t.Fatalf("published = %#v, want live state", published)
	}
	if _, ok, err := db.FindCurrentFile(ctx, "example", "index.html"); err != nil || !ok {
		t.Fatalf("FindCurrentFile after publish = (_, %v, %v), want file", ok, err)
	}
	manifests, err = db.ListCurrentSiteManifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || manifests[0].Site != "example" || manifests[0].Version != 1 {
		t.Fatalf("ListCurrentSiteManifests after publish = %#v, want example v1", manifests)
	}
	if _, err := db.UnpublishSite(ctx, alice.User, "example", "site-sha"); err != nil {
		t.Fatal(err)
	}

	next, err := db.BeginUpload(ctx, "example", "site-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	next.Files = []domain.UploadFileRecord{{
		RelativePath: "index.html",
		BlobPath:     "blobs/site:site-sha/2/file:file-sha",
		FileSHA:      "file-sha-2",
		Bytes:        13,
	}}
	if err := db.FinishUpload(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.FindCurrentFile(ctx, "example", "index.html"); err != nil || !ok {
		t.Fatalf("FindCurrentFile after republish = (_, %v, %v), want file", ok, err)
	}
}

func TestBootstrapAdminCreatesInitialUserOnce(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := db.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Fatal("first bootstrap did not create admin")
	}
	if first.Username != adminUsername {
		t.Fatalf("username = %q, want %q", first.Username, adminUsername)
	}
	if first.Password == "" {
		t.Fatal("password is empty")
	}
	if first.Token == "" {
		t.Fatal("token is empty")
	}

	second, err := db.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("second bootstrap created another admin")
	}

	var count int
	var passwordHash string
	var tokenHash string
	var adminPriv string
	if err := db.readDB.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(password_hash), MIN(token_hash), MIN(admin_priv)
		FROM users
	`).Scan(&count, &passwordHash, &tokenHash, &adminPriv); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
	if passwordHash == first.Password {
		t.Fatal("password was stored in plaintext")
	}
	if tokenHash == first.Token {
		t.Fatal("token was stored in plaintext")
	}
	if !strings.HasPrefix(passwordHash, "pbkdf2-sha256$") {
		t.Fatalf("password hash = %q, want pbkdf2 prefix", passwordHash)
	}
	if !strings.HasPrefix(tokenHash, "sha256:") {
		t.Fatalf("token hash = %q, want sha256 prefix", tokenHash)
	}
	if adminPriv != adminPermission {
		t.Fatalf("admin priv = %q, want %q", adminPriv, adminPermission)
	}
}

func TestUserSitesTableJoinsUsersToUploadedSites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	admin, err := db.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publisherID := createUploadPublisher(t, ctx, db)
	upload, err := db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	_, err = db.writeDB.ExecContext(ctx, `
		INSERT INTO user_sites (user_id, site_sha)
		SELECT id, ? FROM users WHERE username = ?
	`, upload.SiteSHA, admin.Username)
	if err != nil {
		t.Fatal(err)
	}

	var site string
	if err := db.readDB.QueryRowContext(ctx, `
		SELECT s.site
		FROM user_sites us
		JOIN users u ON u.id = us.user_id
		JOIN sites s ON s.site_sha = us.site_sha
		WHERE u.username = ?
	`, admin.Username).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if site != upload.Site {
		t.Fatalf("site = %q, want %q", site, upload.Site)
	}
}

func TestAuthenticateAdminAndSessions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	admin, err := db.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	user, ok, err := db.AuthenticateAdmin(ctx, admin.Username, admin.Password)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("admin credentials did not authenticate")
	}
	if user.Username != admin.Username || user.ID <= 0 {
		t.Fatalf("user = %#v, want bootstrapped admin", user)
	}

	if _, ok, err := db.AuthenticateAdmin(ctx, admin.Username, "wrong"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("wrong password authenticated")
	}

	sessionToken, err := db.CreateAdminSession(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sessionToken == "" {
		t.Fatal("session token is empty")
	}

	sessionUser, ok, err := db.FindAdminSession(ctx, sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("session not found")
	}
	if sessionUser.Username != admin.Username {
		t.Fatalf("session user = %#v, want admin", sessionUser)
	}

	var storedHash string
	if err := db.readDB.QueryRowContext(ctx, `SELECT token_hash FROM user_sessions`).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == sessionToken {
		t.Fatal("session token was stored in plaintext")
	}
	if !strings.HasPrefix(storedHash, "sha256:") {
		t.Fatalf("session token hash = %q, want sha256 prefix", storedHash)
	}

	if err := db.DeleteAdminSession(ctx, sessionToken); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.FindAdminSession(ctx, sessionToken); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("deleted session still authenticates")
	}
}

func TestCreateUserTokenAndSettings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	created, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	if created.Password == "" || created.Token == "" {
		t.Fatalf("created = %#v, want generated credentials", created)
	}
	user, ok, err := db.FindUserByToken(ctx, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || user.Username != "alice" {
		t.Fatalf("token user = (%#v, %v), want alice", user, ok)
	}
	users, err := db.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "alice" || users[0].AdminPriv != "user" {
		t.Fatalf("users = %#v, want alice user", users)
	}

	if err := db.InitializeServerSettings(ctx, domain.ServerSettings{MaxUploadBytes: 123, MaxUploadFiles: 4}); err != nil {
		t.Fatal(err)
	}
	settings, err := db.GetServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.MaxUploadBytes != 123 || settings.MaxUploadFiles != 4 {
		t.Fatalf("settings = %#v, want initialized values", settings)
	}
	if settings.MaxRuntimeDurationMillis != appsettings.DefaultRuntimeMaxDurationMillis {
		t.Fatalf("max runtime duration = %d, want default", settings.MaxRuntimeDurationMillis)
	}
	if settings.MaxPipesPerSite != appsettings.DefaultMaxPipesPerSite || settings.MaxTopicsPerSite != appsettings.DefaultMaxTopicsPerSite ||
		settings.MaxRetainedEventsPerSite != appsettings.DefaultMaxRetainedEventsPerSite || settings.MaxRetainedBytesPerSite != appsettings.DefaultMaxRetainedBytesPerSite {
		t.Fatalf("pipe settings = (%d, %d, %d, %d), want defaults", settings.MaxPipesPerSite, settings.MaxTopicsPerSite, settings.MaxRetainedEventsPerSite, settings.MaxRetainedBytesPerSite)
	}
	if settings.HTTPCacheMode != "revalidate" || settings.HTTPCacheMaxAgeSeconds != 3600 {
		t.Fatalf("http cache settings = (%q, %d), want revalidate and 3600", settings.HTTPCacheMode, settings.HTTPCacheMaxAgeSeconds)
	}
	if err := db.InitializeServerSettings(ctx, domain.ServerSettings{MaxUploadBytes: 999, MaxUploadFiles: 999}); err != nil {
		t.Fatal(err)
	}
	settings, err = db.GetServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.MaxUploadBytes != 123 || settings.MaxUploadFiles != 4 {
		t.Fatalf("settings = %#v, initialize should not overwrite", settings)
	}
	if err := db.SaveServerSettings(ctx, domain.ServerSettings{
		MaxUploadBytes:           2048,
		MaxUploadFiles:           8,
		MaxRuntimeDurationMillis: 2500,
		MaxPipesPerSite:          44,
		MaxTopicsPerSite:         55,
		MaxRetainedEventsPerSite: 66,
		MaxRetainedBytesPerSite:  777,
		HTTPCacheMode:            "max_age",
		HTTPCacheMaxAgeSeconds:   14400,
		AllowedHosts:             []string{"*.example.com", "admin.example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	settings, err = db.GetServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.MaxUploadBytes != 2048 || settings.MaxUploadFiles != 8 || len(settings.AllowedHosts) != 2 || settings.AllowedHosts[0] != "*.example.com" || settings.AllowedHosts[1] != "admin.example.com" {
		t.Fatalf("settings = %#v, want saved values", settings)
	}
	if settings.HTTPCacheMode != "max_age" || settings.HTTPCacheMaxAgeSeconds != 14400 {
		t.Fatalf("http cache settings = (%q, %d), want max_age and 14400", settings.HTTPCacheMode, settings.HTTPCacheMaxAgeSeconds)
	}
	if settings.MaxRuntimeDurationMillis != 2500 {
		t.Fatalf("max runtime duration = %d, want 2500", settings.MaxRuntimeDurationMillis)
	}
	if settings.MaxPipesPerSite != 44 || settings.MaxTopicsPerSite != 55 || settings.MaxRetainedEventsPerSite != 66 || settings.MaxRetainedBytesPerSite != 777 {
		t.Fatalf("pipe settings = (%d, %d, %d, %d), want 44, 55, 66, 777", settings.MaxPipesPerSite, settings.MaxTopicsPerSite, settings.MaxRetainedEventsPerSite, settings.MaxRetainedBytesPerSite)
	}
}

func TestPublishedSitesShowPublisherAndAdminSeesAll(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}

	siteA, err := db.BeginUpload(ctx, "site-a", "site-a-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	siteA.Files = []domain.UploadFileRecord{{RelativePath: "index.html", BlobPath: "a", FileSHA: "a", Bytes: 1}}
	if err := db.FinishUpload(ctx, siteA); err != nil {
		t.Fatal(err)
	}

	siteB, err := db.BeginUpload(ctx, "site-b", "site-b-sha", bob.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	siteB.Files = []domain.UploadFileRecord{{RelativePath: "index.html", BlobPath: "b", FileSHA: "b", Bytes: 2}}
	if err := db.FinishUpload(ctx, siteB); err != nil {
		t.Fatal(err)
	}

	allSites, err := db.ListPublishedSites(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(allSites) != 2 {
		t.Fatalf("allSites = %d, want 2", len(allSites))
	}
	if allSites[0].PublishedBy == "" || allSites[1].PublishedBy == "" {
		t.Fatalf("allSites = %#v, want publisher names", allSites)
	}

	aliceSites, err := db.ListPublishedSites(ctx, alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceSites) != 1 {
		t.Fatalf("aliceSites = %d, want 1", len(aliceSites))
	}
	if aliceSites[0].Site != "site-a" || aliceSites[0].PublishedBy != "alice" {
		t.Fatalf("aliceSites[0] = %#v, want site-a by alice", aliceSites[0])
	}

	bobByName, err := db.ListPublishedSitesByUsername(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(bobByName) != 1 || bobByName[0].Site != "site-b" || bobByName[0].PublishedBy != "bob" {
		t.Fatalf("bobByName = %#v, want site-b by bob", bobByName)
	}
}

func TestListRuntimeAPIProxiesLoadsUploadSettings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "example", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{{
		RelativePath: "api/app.star",
		BlobPath:     "blobs/site:site-sha/1/file:app",
		FileSHA:      "app-sha",
		Bytes:        1,
	}}
	if err := db.SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, map[string]string{
		appsettings.SettingRuntimeHTTPClientAPIProxies: `[{"name":"ifconfig","domain":"ifconfig.me","methods":["GET"]}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	proxies, err := db.ListRuntimeAPIProxies(ctx, upload.SiteSHA, upload.Version)
	if err != nil {
		t.Fatal(err)
	}
	want := []manifest.APIProxy{{Name: "ifconfig", Domain: "ifconfig.me", Methods: []string{"GET"}}}
	if !reflect.DeepEqual(proxies, want) {
		t.Fatalf("proxies = %#v, want %#v", proxies, want)
	}
}

func TestListCurrentSiteManifestsConcurrentDoesNotExhaustReadPool(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	for i := 0; i < 12; i++ {
		site := fmt.Sprintf("site-%d", i)
		siteSHA := fmt.Sprintf("site-%d-sha", i)
		upload, err := db.BeginUpload(ctx, site, siteSHA, publisherID, false)
		if err != nil {
			t.Fatal(err)
		}
		upload.Files = []domain.UploadFileRecord{{RelativePath: "index.html", BlobPath: site, FileSHA: siteSHA, Bytes: 1}}
		if err := db.SaveUploadSettings(ctx, upload.SiteSHA, upload.Version, map[string]string{
			appsettings.SettingDatabaseFeature:         "false",
			appsettings.SettingDatabaseFeatureRequired: "false",
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishUpload(ctx, upload); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manifests, err := db.ListCurrentSiteManifests(ctx)
			if err != nil {
				errs <- err
				return
			}
			if len(manifests) != 12 {
				errs <- fmt.Errorf("manifest count = %d, want 12", len(manifests))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestBeginUploadRejectsNonOwnerForExistingSite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.CreateUser(ctx, "admin2", adminPermission)
	if err != nil {
		t.Fatal(err)
	}

	first, err := db.BeginUpload(ctx, "foo", "foo-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	first.Files = []domain.UploadFileRecord{{RelativePath: "index.html", BlobPath: "v1", FileSHA: "v1", Bytes: 1}}
	if err := db.FinishUpload(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkUserSite(ctx, alice.User.ID, first.SiteSHA); err != nil {
		t.Fatal(err)
	}

	if _, err := db.BeginUpload(ctx, "foo", "foo-sha", bob.User.ID, false); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob begin upload error = %v, want ErrSiteOwnership", err)
	}

	if _, err := db.BeginUpload(ctx, "foo", "foo-sha", alice.User.ID, false); err != nil {
		t.Fatalf("owner begin upload: %v", err)
	}
	if _, err := db.BeginUpload(ctx, "foo", "foo-sha", admin.User.ID, true); err != nil {
		t.Fatalf("admin begin upload: %v", err)
	}
}

func TestBeginUploadRejectsUnauthenticatedPublisher(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.BeginUpload(ctx, "foo", "foo-sha", 0, false); !errors.Is(err, domain.ErrAuthenticatedUserRequired) {
		t.Fatalf("begin upload error = %v, want ErrAuthenticatedUserRequired", err)
	}

	exists, err := db.SiteExists(ctx, "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("unauthenticated upload created site metadata")
	}
}

func TestBeginUploadIncrementsAndRetainsUploads(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 1 {
		t.Fatalf("initial version = %d, want 1", upload.Version)
	}

	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	upload, err = db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 2 {
		t.Fatalf("second version = %d, want 2", upload.Version)
	}

	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	upload, err = db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if upload.Version != 3 {
		t.Fatalf("third version = %d, want 3", upload.Version)
	}

	var count int
	if err := db.readDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM uploads WHERE site_sha = ?
	`, "site-sha").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("upload rows = %d, want 3", count)
	}
}

func TestConcurrentBeginUploadAllocatesUniqueVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	const uploads = 20
	versions := make(chan int64, uploads)
	errs := make(chan error, uploads)

	var wg sync.WaitGroup
	for i := 0; i < uploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			upload, err := db.BeginUpload(ctx, "example.com", "site-sha", publisherID, false)
			if err != nil {
				errs <- err
				return
			}
			versions <- upload.Version
		}()
	}
	wg.Wait()
	close(versions)
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}

	seen := make(map[int64]bool)
	for version := range versions {
		seen[version] = true
	}
	for version := int64(1); version <= uploads; version++ {
		if !seen[version] {
			t.Fatalf("missing allocated version %d; got %#v", version, seen)
		}
	}
}

func TestFindCurrentFileUsesPublishedCurrentVersion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	v1, err := db.BeginUpload(ctx, "foo", "foo-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	v1.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}
	if err := db.FinishUpload(ctx, v1); err != nil {
		t.Fatal(err)
	}

	v2, err := db.BeginUpload(ctx, "foo", "foo-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 {
		t.Fatalf("second version = %d, want 2", v2.Version)
	}

	file, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected current file")
	}
	if file.BlobPath != "blobs/site:foo-sha/1/file:v1" {
		t.Fatalf("blob path before publish = %q, want v1 blob", file.BlobPath)
	}

	v2.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/2/file:v2", FileSHA: "v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, v2); err != nil {
		t.Fatal(err)
	}

	file, ok, err = db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected current file after publish")
	}
	if file.BlobPath != "blobs/site:foo-sha/2/file:v2" {
		t.Fatalf("blob path after publish = %q, want v2 blob", file.BlobPath)
	}
}

func TestConcurrentUploadsForDifferentSitesServeIndependently(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	type siteUpload struct {
		site    string
		siteSHA string
		blob    string
	}
	initial := []siteUpload{
		{site: "site-a", siteSHA: "site-a-sha", blob: "blobs/site:site-a-sha/1/file:a-v1"},
		{site: "site-b", siteSHA: "site-b-sha", blob: "blobs/site:site-b-sha/1/file:b-v1"},
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(initial))
	for _, item := range initial {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			upload, err := db.BeginUpload(ctx, item.site, item.siteSHA, publisherID, false)
			if err != nil {
				errs <- err
				return
			}
			if upload.Version != 1 {
				errs <- fmt.Errorf("%s version = %d, want 1", item.site, upload.Version)
				return
			}
			upload.Files = []domain.UploadFileRecord{
				{RelativePath: "index.html", BlobPath: item.blob, FileSHA: item.blob, Bytes: 1},
			}
			if err := db.FinishUpload(ctx, upload); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/1/file:b-v1")

	a2, err := db.BeginUpload(ctx, "site-a", "site-a-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := db.BeginUpload(ctx, "site-b", "site-b-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	if a2.Version != 2 || b2.Version != 2 {
		t.Fatalf("second versions = (%d, %d), want (2, 2)", a2.Version, b2.Version)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/1/file:b-v1")

	b2.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:site-b-sha/2/file:b-v2", FileSHA: "b-v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, b2); err != nil {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/1/file:a-v1")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/2/file:b-v2")

	a2.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:site-a-sha/2/file:a-v2", FileSHA: "a-v2", Bytes: 2},
	}
	if err := db.FinishUpload(ctx, a2); err != nil {
		t.Fatal(err)
	}

	assertCurrentBlob(t, ctx, db, "site-a", "blobs/site:site-a-sha/2/file:a-v2")
	assertCurrentBlob(t, ctx, db, "site-b", "blobs/site:site-b-sha/2/file:b-v2")
}

func assertCurrentBlob(t *testing.T, ctx context.Context, db *Database, site string, want string) {
	t.Helper()
	file, ok, err := db.FindCurrentFile(ctx, site, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("site %s has no current index.html", site)
	}
	if file.BlobPath != want {
		t.Fatalf("site %s blob = %q, want %q", site, file.BlobPath, want)
	}
}

func TestFindCurrentFileIgnoresUploadingAndErrorVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "foo", "foo-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}

	_, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("uploading version should not serve")
	}

	if err := db.FailUpload(ctx, upload, "test failure"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("error version should not serve")
	}
}

func TestDeleteSiteRemovesMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisherID := createUploadPublisher(t, ctx, db)

	upload, err := db.BeginUpload(ctx, "foo", "foo-sha", publisherID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.DeleteSite(ctx, domain.AdminUser{AdminPriv: "admin:*"}, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("deleted = false, want true")
	}

	_, ok, err := db.FindCurrentFile(ctx, "foo", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("current file still exists after delete")
	}

	var siteCount int
	if err := db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE site_sha = ?`, "foo-sha").Scan(&siteCount); err != nil {
		t.Fatal(err)
	}
	if siteCount != 0 {
		t.Fatalf("site count = %d, want 0", siteCount)
	}
}

func TestDeleteSiteEnforcesOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "quack.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser(ctx, "bob", "user")
	if err != nil {
		t.Fatal(err)
	}

	upload, err := db.BeginUpload(ctx, "foo", "foo-sha", alice.User.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	upload.Files = []domain.UploadFileRecord{
		{RelativePath: "index.html", BlobPath: "blobs/site:foo-sha/1/file:v1", FileSHA: "v1", Bytes: 1},
	}
	if err := db.FinishUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}

	if _, err := db.DeleteSite(ctx, bob.User, "foo", "foo-sha"); !errors.Is(err, domain.ErrSiteOwnership) {
		t.Fatalf("bob delete error = %v, want ErrSiteOwnership", err)
	}
	if _, ok, err := db.FindCurrentFile(ctx, "foo", "index.html"); err != nil || !ok {
		t.Fatalf("current file after unauthorized delete = (_, %v, %v), want file", ok, err)
	}

	deleted, err := db.DeleteSite(ctx, alice.User, "foo", "foo-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("alice deleted = false, want true")
	}
}
