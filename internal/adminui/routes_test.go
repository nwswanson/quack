package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"quack/internal/domain"
	"quack/internal/hardware"
	"quack/internal/releases"
	appsettings "quack/internal/settings"
)

func TestSameOriginAdminRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")

	if !sameOriginAdminRequest(req) {
		t.Fatal("same origin request rejected")
	}

	req.Header.Set("Origin", "https://other.example.com")
	if sameOriginAdminRequest(req) {
		t.Fatal("cross origin request accepted")
	}
}

func TestAdminSessionCookieSecureBehindProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	cookie := adminSessionCookie(req, "session", 60)
	if cookie.Name != SessionCookieName {
		t.Fatalf("cookie = %q, want %q", cookie.Name, SessionCookieName)
	}
	if !cookie.Secure {
		t.Fatal("cookie should be secure behind https proxy")
	}
}

func TestPolicyFromFormRejectsInherit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/policy", strings.NewReader(url.Values{
		"database_policy_mode": {"inherit"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}

	if _, ok := policyFromForm(req, appsettings.SettingDatabaseFeature, "database_policy", 1); ok {
		t.Fatal("policyFromForm accepted inherit")
	}
}

func TestPolicyFromFormRequiresExplicitMode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/policy", strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}

	if _, ok := policyFromForm(req, appsettings.SettingDatabaseFeature, "database_policy", 1); ok {
		t.Fatal("policyFromForm accepted missing mode")
	}
}

func TestPolicyFromFormAcceptsAllowAndDeny(t *testing.T) {
	for _, mode := range []string{"allow", "deny"} {
		req := httptest.NewRequest(http.MethodPost, "/policy", strings.NewReader(url.Values{
			"database_policy_mode":   {mode},
			"database_policy_reason": {"because"},
		}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		policy, ok := policyFromForm(req, appsettings.SettingDatabaseFeature, "database_policy", 1)
		if !ok || policy.Mode != mode || policy.Reason != "because" {
			t.Fatalf("policyFromForm(%q) = %+v ok=%v, want mode and reason", mode, policy, ok)
		}
	}
}

func TestAdminHardwareFormSavesDevice(t *testing.T) {
	repo := &adminHardwareRepo{}
	handler := New(Options{
		Sessions: adminSessionRepo{user: domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}},
		Releases: adminReleaseRepo{sites: []domain.PublishedSite{{
			Site: "acme", SiteSHA: "site-sha", CurrentVersion: 1,
		}}},
		Hardware: repo,
	})
	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(url.Values{
		"kind":  {hardware.AdminKindUVCCamera},
		"path":  {"/dev/video2"},
		"label": {"Front desk Logitech C270"},
		"site":  {"acme"},
		"alias": {"front_door"},
	}.Encode()))
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	resp := httptest.NewRecorder()

	handler.handleAdminHardware(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.Code)
	}
	if repo.saved.ID != "" || repo.saved.Kind != hardware.AdminKindUVCCamera || repo.saved.Path != "/dev/video2" || repo.saved.Label != "Front desk Logitech C270" || repo.saved.Site != "acme" || repo.saved.Alias != "front_door" {
		t.Fatalf("saved device = %+v, want form fields", repo.saved)
	}
}

func TestAdminHardwareFormSavesSerialOptions(t *testing.T) {
	repo := &adminHardwareRepo{}
	handler := New(Options{
		Sessions: adminSessionRepo{user: domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}},
		Releases: adminReleaseRepo{sites: []domain.PublishedSite{{
			Site: "acme", SiteSHA: "site-sha", CurrentVersion: 1,
		}}},
		Hardware: repo,
	})
	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(url.Values{
		"kind":                      {hardware.AdminKindSerial},
		"path":                      {"/dev/ttyUSB0"},
		"site":                      {"acme"},
		"alias":                     {"weather_station"},
		"serial_baud":               {"115200"},
		"serial_data_bits":          {"7"},
		"serial_parity":             {"even"},
		"serial_stop_bits":          {"2"},
		"serial_read_timeout_ms":    {"250"},
		"serial_request_timeout_ms": {"3000"},
		"serial_write_chunk_bytes":  {"128"},
		"serial_write_delay_ms":     {"5"},
		"serial_write_queue_size":   {"32"},
		"serial_recent_events":      {"128"},
		"serial_reconnect_ms":       {"750"},
	}.Encode()))
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	resp := httptest.NewRecorder()

	handler.handleAdminHardware(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.Code)
	}
	if repo.saved.Kind != hardware.AdminKindSerial || repo.saved.Serial.BaudRate != 115200 || repo.saved.Serial.DataBits != 7 || repo.saved.Serial.Parity != "even" || repo.saved.Serial.StopBits != "2" {
		t.Fatalf("saved device = %+v, want serial options", repo.saved)
	}
	if repo.saved.Serial.ReadTimeoutMillis != 250 || repo.saved.Serial.RequestTimeoutMillis != 3000 || repo.saved.Serial.WriteChunkBytes != 128 || repo.saved.Serial.WriteDelayMillis != 5 || repo.saved.Serial.WriteQueueSize != 32 || repo.saved.Serial.RecentEvents != 128 || repo.saved.Serial.ReconnectMillis != 750 {
		t.Fatalf("saved serial options = %+v, want timeout and queue settings", repo.saved.Serial)
	}
}

func TestAdminHardwareEditKeepsExistingKind(t *testing.T) {
	repo := &adminHardwareRepo{devices: []hardware.AdminDevice{{
		ID:   "serial_01",
		Kind: hardware.AdminKindSerial,
		Path: "/dev/ttyUSB0",
	}}}
	handler := New(Options{
		Sessions: adminSessionRepo{user: domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}},
		Hardware: repo,
	})
	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(url.Values{
		"action":      {"save"},
		"original_id": {"serial_01"},
		"kind":        {hardware.AdminKindUVCCamera},
		"path":        {"/dev/ttyUSB1"},
	}.Encode()))
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	resp := httptest.NewRecorder()

	handler.handleAdminHardware(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.Code)
	}
	if repo.saved.Kind != hardware.AdminKindSerial {
		t.Fatalf("saved kind = %q, want existing serial kind", repo.saved.Kind)
	}
	if repo.saved.ID != "serial_01" {
		t.Fatalf("saved id = %q, want original id fallback", repo.saved.ID)
	}
}

func TestAdminHardwareFormCanUnbindAndEditDevice(t *testing.T) {
	repo := &adminHardwareRepo{}
	handler := New(Options{
		Sessions: adminSessionRepo{user: domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}},
		Releases: adminReleaseRepo{sites: []domain.PublishedSite{{
			Site: "acme", SiteSHA: "site-sha", CurrentVersion: 1,
		}}},
		Hardware: repo,
	})
	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(url.Values{
		"action":      {"save"},
		"original_id": {"cam_01"},
		"kind":        {hardware.AdminKindUVCCamera},
		"path":        {"/dev/video4"},
		"label":       {"Moved camera"},
		"site":        {""},
		"alias":       {"cam_01"},
	}.Encode()))
	req.Host = "admin.example.com"
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	resp := httptest.NewRecorder()

	handler.handleAdminHardware(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.Code)
	}
	if repo.saved.OriginalID != "cam_01" || repo.saved.ID != "cam_01" || repo.saved.Path != "/dev/video4" || repo.saved.Label != "Moved camera" || repo.saved.Site != "" || repo.saved.Alias != "cam_01" {
		t.Fatalf("saved device = %+v, want edited unbound device with original id", repo.saved)
	}
}

func TestAdminHardwarePageRendersEditableRows(t *testing.T) {
	repo := &adminHardwareRepo{devices: []hardware.AdminDevice{{
		ID:    "cam_01",
		Kind:  hardware.AdminKindUVCCamera,
		Path:  "/dev/video2",
		Label: "Front desk",
		Site:  "acme",
		Alias: "front_door",
	}}}
	handler := New(Options{
		Sessions: adminSessionRepo{user: domain.AdminUser{ID: 1, Username: "admin", AdminPriv: "admin:*"}},
		Releases: adminReleaseRepo{sites: []domain.PublishedSite{
			{Site: "acme", SiteSHA: "site-sha", CurrentVersion: 1},
			{Site: "beta", SiteSHA: "site-sha-2", CurrentVersion: 1},
		}},
		Hardware: repo,
	})
	req := httptest.NewRequest(http.MethodGet, "/hardware", nil)
	req.Host = "admin.example.com"
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	resp := httptest.NewRecorder()

	handler.handleAdminHardware(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want OK; body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{
		`<article class="hardware-card">`,
		`<input type="hidden" name="original_id" value="cam_01">`,
		`<input type="hidden" name="id" value="cam_01">`,
		`<input type="hidden" name="kind" value="uvc-camera">`,
		`<input name="path" value="/dev/video2" required>`,
		`<input name="label" value="Front desk">`,
		`<input name="alias" value="front_door" placeholder="front_door">`,
		`<option value="acme" selected>acme</option>`,
		`<option value="beta" >beta</option>`,
		`<button class="small-button" form="hardware-save-cam_01" type="submit">Save</button>`,
		`<button class="small-button warning-button" type="submit">Close Active Request</button>`,
		`<label class="tab-label" for="create-uvc">UVC Camera</label>`,
		`<label class="tab-label" for="create-serial">Serial Port</label>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
	}
}

type adminSessionRepo struct {
	user domain.AdminUser
}

func (r adminSessionRepo) CreateAdminSession(context.Context, int64) (string, error) {
	return "session", nil
}

func (r adminSessionRepo) FindAdminSession(context.Context, string) (domain.AdminUser, bool, error) {
	return r.user, r.user.ID > 0, nil
}

func (r adminSessionRepo) DeleteAdminSession(context.Context, string) error {
	return nil
}

type adminHardwareRepo struct {
	saved   hardware.AdminDevice
	devices []hardware.AdminDevice
}

func (r *adminHardwareRepo) ListHardwareDevices(context.Context) ([]hardware.AdminDevice, error) {
	return append([]hardware.AdminDevice(nil), r.devices...), nil
}

func (r *adminHardwareRepo) SaveHardwareDevice(_ context.Context, device hardware.AdminDevice) error {
	r.saved = device
	return nil
}

func (r *adminHardwareRepo) DeleteHardwareDevice(context.Context, string) (bool, error) {
	return true, nil
}

type adminReleaseRepo struct {
	sites []domain.PublishedSite
}

func (r adminReleaseRepo) ListPublishedSites(context.Context, int64, bool) ([]domain.PublishedSite, error) {
	return append([]domain.PublishedSite(nil), r.sites...), nil
}

func (r adminReleaseRepo) ListPublishedSitesByUsername(context.Context, string) ([]domain.PublishedSite, error) {
	return append([]domain.PublishedSite(nil), r.sites...), nil
}

func (r adminReleaseRepo) ListSiteRevisions(context.Context, domain.AdminUser, string, string) ([]domain.RevisionRecord, error) {
	return nil, nil
}

func (r adminReleaseRepo) RollbackSite(context.Context, domain.AdminUser, string, string) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, nil
}

func (r adminReleaseRepo) RollbackSiteToVersion(context.Context, domain.AdminUser, string, string, int64) (domain.RollbackRecord, error) {
	return domain.RollbackRecord{}, nil
}

func (r adminReleaseRepo) UnpublishSite(context.Context, domain.AdminUser, string, string) (domain.UnpublishRecord, error) {
	return domain.UnpublishRecord{}, nil
}

func (r adminReleaseRepo) PublishSite(context.Context, domain.AdminUser, string, string) (domain.PublishRecord, error) {
	return domain.PublishRecord{}, nil
}

func (r adminReleaseRepo) DeleteSite(context.Context, domain.AdminUser, string, string) (bool, error) {
	return true, nil
}

func (r adminReleaseRepo) LookupRoute(context.Context, string, string) (releases.RouteDecision, bool, error) {
	return releases.RouteDecision{}, false, nil
}
