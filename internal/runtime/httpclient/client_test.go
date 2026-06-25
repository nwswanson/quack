package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"quack/internal/logbuffer"
	"quack/internal/manifest"
)

func TestDomainProxyAllowsOnlyMatchingHost(t *testing.T) {
	client := testClient([]manifest.APIProxy{{Name: "api", Domain: "api.example.com", Methods: []string{"GET"}}})
	_, _, err := client.resolve("GET", "https://other.example.com/v1")
	if err == nil {
		t.Fatal("expected raw URL mismatch error")
	}
}

func TestPathFixedAllowsQueryButNotPathChange(t *testing.T) {
	proxy := manifest.APIProxy{Name: "local", PathFixed: "http://example.com/api/v1/widget", Methods: []string{"GET"}}
	client := testClient([]manifest.APIProxy{proxy})
	target, _, err := client.resolve("GET", "api://local?id=1")
	if err != nil {
		t.Fatal(err)
	}
	if target.String() != "http://example.com/api/v1/widget?id=1" {
		t.Fatalf("target = %q", target.String())
	}
	_, _, err = client.resolve("GET", "api://local/extra?id=1")
	if err == nil || !strings.Contains(err.Error(), "does not allow changing path") {
		t.Fatalf("error = %v, want path denial", err)
	}
}

func TestPrivateIPLiteralDeniedByDefaultAndAllowedByCIDR(t *testing.T) {
	proxy := manifest.APIProxy{Name: "lan", PathFixed: "http://192.168.1.50/api", Methods: []string{"GET"}}
	client := testClient([]manifest.APIProxy{proxy})
	err := client.allowedAddress(netip.MustParseAddr("192.168.1.50"), "lan", "GET", "http://192.168.1.50/api")
	if !errors.Is(err, ErrHTTPClientTargetDenied) {
		t.Fatalf("err = %v, want target denied", err)
	}
	client.Policy.AllowedPrivateCIDRs = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	if err := client.allowedAddress(netip.MustParseAddr("192.168.1.50"), "lan", "GET", "http://192.168.1.50/api"); err != nil {
		t.Fatalf("allowedAddress error = %v", err)
	}
}

func TestLoopbackDeniedEvenWhenPrivateCIDRAllowedUnlessSelfAllowed(t *testing.T) {
	client := testClient([]manifest.APIProxy{{Name: "local", PathFixed: "http://127.0.0.1/api", Methods: []string{"GET"}}})
	client.Policy.AllowedPrivateCIDRs = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	err := client.allowedAddress(netip.MustParseAddr("127.0.0.1"), "local", "GET", "http://127.0.0.1/api")
	if !errors.Is(err, ErrHTTPClientTargetDenied) {
		t.Fatalf("err = %v, want loopback denied", err)
	}
	client.Policy.AllowSelf = true
	if err := client.allowedAddress(netip.MustParseAddr("127.0.0.1"), "local", "GET", "http://127.0.0.1/api"); err != nil {
		t.Fatalf("allowedAddress with self error = %v", err)
	}
}

func TestInsecureSSLDeniedUnlessGlobalAllowEnabled(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	proxy := manifest.APIProxy{Name: "tls", PathFixed: server.URL, Methods: []string{"GET"}, InsecureSSL: true}
	client := testClient([]manifest.APIProxy{proxy})
	client.Policy.AllowSelf = true
	_, err := client.Do(context.Background(), Request{Method: "GET", URL: "api://tls"})
	if err == nil || !strings.Contains(err.Error(), "insecure SSL is disabled") {
		t.Fatalf("err = %v, want insecure SSL denied", err)
	}
	client.Policy.AllowInsecureSSL = true
	_, err = client.Do(context.Background(), Request{Method: "GET", URL: "api://tls"})
	if err != nil {
		t.Fatalf("Do with insecure allow error = %v", err)
	}
}

func TestTimeoutClampLogs(t *testing.T) {
	logs := logbuffer.New(10)
	client := testClient([]manifest.APIProxy{{Name: "api", Domain: "example.com", Methods: []string{"GET"}}})
	client.Logs = logs
	client.Policy.MaxTimeout = 25 * time.Millisecond
	if got := client.effectiveTimeout(250 * time.Millisecond); got != 25*time.Millisecond {
		t.Fatalf("timeout = %v", got)
	}
	events := logs.Tail(logbuffer.Filter{IncludeSystem: true}, 10)
	if len(events) != 1 || events[0].Message != "http client timeout clamped" {
		t.Fatalf("events = %+v, want clamp log", events)
	}
}

func TestMaxBytesAbortsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("too large"))
	}))
	defer server.Close()
	client := testClient([]manifest.APIProxy{{Name: "api", PathFixed: server.URL, Methods: []string{"GET"}}})
	client.Policy.AllowSelf = true
	client.Policy.MaxBytes = 3
	_, err := client.Do(context.Background(), Request{Method: "GET", URL: "api://api"})
	if !errors.Is(err, ErrHTTPClientResponseTooLarge) {
		t.Fatalf("err = %v, want response too large", err)
	}
}

func TestRedirectFalseBlocksRedirectAndTrueRevalidatesTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	client := testClient([]manifest.APIProxy{{Name: "api", PathFixed: redirect.URL, Methods: []string{"GET"}}})
	client.Policy.AllowSelf = true
	resp, err := client.Do(context.Background(), Request{Method: "GET", URL: "api://api"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want redirect returned without follow", resp.StatusCode)
	}
	_, err = client.Do(context.Background(), Request{Method: "GET", URL: "api://api", FollowRedirects: true})
	if err == nil || !strings.Contains(err.Error(), "only allows configured fixed path") {
		t.Fatalf("err = %v, want redirect target denied", err)
	}
}

func testClient(proxies []manifest.APIProxy) Client {
	return Client{
		Policy: Policy{
			Proxies:           proxies,
			MaxBytes:          1 << 20,
			MaxTimeout:        time.Second,
			HTTPClientAllowed: true,
		},
	}
}
