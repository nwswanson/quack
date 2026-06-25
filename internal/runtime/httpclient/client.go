package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"quack/internal/logbuffer"
	"quack/internal/manifest"
)

var (
	ErrHTTPClientPolicyDenied     = errors.New("http client policy denied")
	ErrHTTPClientProxyNotFound    = errors.New("http client proxy not found")
	ErrHTTPClientTargetDenied     = errors.New("http client target denied")
	ErrHTTPClientTimeout          = errors.New("http client timed out")
	ErrHTTPClientResponseTooLarge = errors.New("http client response too large")
)

type Policy struct {
	Proxies              []manifest.APIProxy
	MaxBytes             int64
	MaxTimeout           time.Duration
	AllowedPrivateCIDRs  []netip.Prefix
	AllowSelf            bool
	AllowInsecureSSL     bool
	HTTPClientAllowed    bool
	HTTPClientDenyReason string
}

type Client struct {
	Policy  Policy
	Logs    *logbuffer.Service
	Site    string
	Version int64
	Route   string
}

type Request struct {
	Method          string
	URL             string
	Headers         map[string][]string
	Body            []byte
	Timeout         time.Duration
	FollowRedirects bool
}

type Response struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type Denial struct {
	Code    string
	Message string
	Proxy   string
	Method  string
	Target  string
}

func (d Denial) Error() string {
	if d.Message != "" {
		return "http client denied: " + d.Message
	}
	return "http client denied"
}

func (c Client) Do(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !c.Policy.HTTPClientAllowed {
		reason := strings.TrimSpace(c.Policy.HTTPClientDenyReason)
		if reason == "" {
			reason = "outbound HTTP is disabled by administrator policy"
		}
		err := Denial{Code: "policy_denied", Message: reason, Method: method}
		c.logDenied(method, "", "", err)
		return Response{}, fmt.Errorf("%w: %w", ErrHTTPClientPolicyDenied, err)
	}
	if len(c.Policy.Proxies) == 0 {
		err := Denial{Code: "no_proxies", Message: "no api_proxies are configured for this site", Method: method}
		c.logDenied(method, "", "", err)
		return Response{}, fmt.Errorf("%w: %w", ErrHTTPClientPolicyDenied, err)
	}
	target, proxy, err := c.resolve(method, req.URL)
	if err != nil {
		c.logDenied(method, "", "", err)
		return Response{}, err
	}
	proxyName := proxy.Name
	safeTarget := sanitizeURL(target)
	if proxy.InsecureSSL && !c.Policy.AllowInsecureSSL {
		err := Denial{Code: "insecure_ssl_denied", Message: fmt.Sprintf("proxy %q requested insecure_ssl but insecure SSL is disabled by server policy", proxyName), Proxy: proxyName, Method: method, Target: safeTarget}
		c.logDenied(method, proxyName, safeTarget, err)
		return Response{}, fmt.Errorf("%w: %w", ErrHTTPClientPolicyDenied, err)
	}

	timeout := c.effectiveTimeout(req.Timeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(callCtx, method, target.String(), bytes.NewReader(req.Body))
	if err != nil {
		return Response{}, err
	}
	for key, values := range req.Headers {
		if !allowedHeaderName(key) {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return c.dialContext(ctx, network, address, proxyName, method, safeTarget)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: proxy.InsecureSSL}, //nolint:gosec
	}
	client := &http.Client{Transport: transport}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !req.FollowRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}
		if err := c.validateURLForProxy(next.URL, proxy); err != nil {
			return err
		}
		return nil
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			err = ErrHTTPClientTimeout
		}
		c.logDenied(method, proxyName, safeTarget, err)
		return Response{}, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, c.Policy.MaxBytes)
	if err != nil {
		c.logDenied(method, proxyName, safeTarget, err)
		return Response{}, err
	}
	out := Response{StatusCode: resp.StatusCode, Headers: cloneHeaders(resp.Header), Body: body}
	c.logRequest(method, proxyName, safeTarget, resp.StatusCode, len(body), time.Since(start))
	return out, nil
}

func (c Client) resolve(method string, value string) (*url.URL, manifest.APIProxy, error) {
	if len(c.Policy.Proxies) == 0 {
		return nil, manifest.APIProxy{}, Denial{Code: "no_proxies", Message: "no api_proxies are configured for this site", Method: method}
	}
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return nil, manifest.APIProxy{}, err
	}
	var proxy manifest.APIProxy
	if u.Scheme == "api" {
		name := u.Host
		for _, candidate := range c.Policy.Proxies {
			if candidate.Name == name {
				proxy = candidate
				break
			}
		}
		if proxy.Name == "" {
			return nil, manifest.APIProxy{}, fmt.Errorf("%w: %q", ErrHTTPClientProxyNotFound, name)
		}
		target, err := c.buildNamedURL(u, proxy)
		if err != nil {
			return nil, manifest.APIProxy{}, err
		}
		if err := c.validate(method, target, proxy); err != nil {
			return nil, manifest.APIProxy{}, err
		}
		return target, proxy, nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, manifest.APIProxy{}, Denial{Code: "scheme_denied", Message: "target URL must use api, http, or https", Method: method}
	}
	var matches []manifest.APIProxy
	for _, candidate := range c.Policy.Proxies {
		if c.validate(method, u, candidate) == nil {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return nil, manifest.APIProxy{}, fmt.Errorf("%w: raw URL must match exactly one configured proxy", ErrHTTPClientProxyNotFound)
	}
	return u, matches[0], nil
}

func (c Client) buildNamedURL(u *url.URL, proxy manifest.APIProxy) (*url.URL, error) {
	if proxy.Domain != "" {
		out := &url.URL{Scheme: "https", Host: proxy.Domain, Path: u.EscapedPath(), RawQuery: u.RawQuery}
		if out.Path == "" {
			out.Path = "/"
		}
		return out, nil
	}
	base, err := url.Parse(proxy.PathFixed)
	if err != nil {
		return nil, err
	}
	if u.EscapedPath() != "" && u.EscapedPath() != "/" {
		return nil, Denial{Code: "path_denied", Message: fmt.Sprintf("proxy %q does not allow changing path", proxy.Name), Proxy: proxy.Name, Target: sanitizeURL(base)}
	}
	out := *base
	out.RawQuery = u.RawQuery
	return &out, nil
}

func (c Client) validate(method string, u *url.URL, proxy manifest.APIProxy) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return Denial{Code: "scheme_denied", Message: "target URL must use http or https", Proxy: proxy.Name, Method: method, Target: sanitizeURL(u)}
	}
	if !methodAllowed(method, proxy.Methods) {
		return Denial{Code: "method_denied", Message: fmt.Sprintf("proxy %q does not allow %s", proxy.Name, method), Proxy: proxy.Name, Method: method, Target: sanitizeURL(u)}
	}
	return c.validateURLForProxy(u, proxy)
}

func (c Client) validateURLForProxy(u *url.URL, proxy manifest.APIProxy) error {
	if proxy.Domain != "" {
		if hostOnly(u.Host) != hostOnly(proxy.Domain) {
			return Denial{Code: "host_denied", Message: fmt.Sprintf("proxy %q does not allow host %q", proxy.Name, u.Host), Proxy: proxy.Name, Target: sanitizeURL(u)}
		}
		return nil
	}
	base, err := url.Parse(proxy.PathFixed)
	if err != nil {
		return err
	}
	if u.Scheme != base.Scheme || strings.ToLower(u.Host) != strings.ToLower(base.Host) || u.EscapedPath() != base.EscapedPath() {
		return Denial{Code: "path_denied", Message: fmt.Sprintf("proxy %q only allows configured fixed path", proxy.Name), Proxy: proxy.Name, Target: sanitizeURL(u)}
	}
	return nil
}

func (c Client) dialContext(ctx context.Context, network, address, proxy, method, safeTarget string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var firstDenied error
	var dialer net.Dialer
	for _, addr := range addrs {
		if err := c.allowedAddress(addr, proxy, method, safeTarget); err != nil {
			if firstDenied == nil {
				firstDenied = err
			}
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		firstDenied = err
	}
	if firstDenied != nil {
		return nil, firstDenied
	}
	return nil, fmt.Errorf("%w: no resolved addresses", ErrHTTPClientTargetDenied)
}

func resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{addr}, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

func (c Client) allowedAddress(addr netip.Addr, proxy, method, target string) error {
	if addr.IsLoopback() {
		if c.Policy.AllowSelf {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrHTTPClientTargetDenied, Denial{Code: "self_denied", Message: fmt.Sprintf("proxy %q resolves to loopback address %s", proxy, addr), Proxy: proxy, Method: method, Target: target})
	}
	if addr.IsUnspecified() || addr.IsMulticast() {
		return fmt.Errorf("%w: %w", ErrHTTPClientTargetDenied, Denial{Code: "address_denied", Message: fmt.Sprintf("proxy %q resolves to disallowed address %s", proxy, addr), Proxy: proxy, Method: method, Target: target})
	}
	if isPrivateOrLinkLocal(addr) && !prefixContains(c.Policy.AllowedPrivateCIDRs, addr) {
		return fmt.Errorf("%w: %w", ErrHTTPClientTargetDenied, Denial{Code: "private_ip_denied", Message: fmt.Sprintf("proxy %q resolves to private address %s", proxy, addr), Proxy: proxy, Method: method, Target: target})
	}
	return nil
}

func (c Client) effectiveTimeout(requested time.Duration) time.Duration {
	max := c.Policy.MaxTimeout
	if max <= 0 {
		max = time.Second
	}
	if requested <= 0 {
		return max
	}
	if requested > max {
		c.log("warn", "http client timeout clamped", map[string]string{
			"requested_timeout_ms": strconv.FormatInt(requested.Milliseconds(), 10),
			"effective_timeout_ms": strconv.FormatInt(max.Milliseconds(), 10),
			"global_timeout_ms":    strconv.FormatInt(max.Milliseconds(), 10),
		})
		return max
	}
	return requested
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = 1 << 20
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, ErrHTTPClientResponseTooLarge
	}
	return data, nil
}

func methodAllowed(method string, methods []string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, allowed := range methods {
		if strings.ToUpper(allowed) == method {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	out := *u
	out.RawQuery = ""
	out.Fragment = ""
	return out.String()
}

func allowedHeaderName(key string) bool {
	key = http.CanonicalHeaderKey(strings.TrimSpace(key))
	switch key {
	case "", "Host", "Connection", "Proxy-Authorization", "Proxy-Authenticate", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return false
	default:
		return true
	}
}

func cloneHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func isPrivateOrLinkLocal(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLinkLocalUnicast()
}

func prefixContains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (c Client) logDenied(method, proxy, target string, err error) {
	attrs := map[string]string{"method": method}
	if proxy != "" {
		attrs["proxy"] = proxy
	}
	if target != "" {
		attrs["target"] = target
	}
	var denial Denial
	if errors.As(err, &denial) {
		attrs["code"] = denial.Code
		if denial.Proxy != "" {
			attrs["proxy"] = denial.Proxy
		}
		if denial.Method != "" {
			attrs["method"] = denial.Method
		}
		if denial.Target != "" {
			attrs["target"] = denial.Target
		}
	} else {
		attrs["code"] = "request_failed"
		attrs["error"] = err.Error()
	}
	c.log("warn", "http client request denied", attrs)
}

func (c Client) logRequest(method, proxy, target string, status int, bytes int, duration time.Duration) {
	c.log("info", "http client request", map[string]string{
		"proxy":       proxy,
		"method":      method,
		"target":      target,
		"status":      strconv.Itoa(status),
		"bytes":       strconv.Itoa(bytes),
		"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
	})
}

func (c Client) log(level, message string, attrs map[string]string) {
	if c.Logs == nil {
		return
	}
	c.Logs.Add(logbuffer.Event{
		Level:      level,
		Source:     "http_client",
		Site:       c.Site,
		Version:    c.Version,
		Route:      c.Route,
		Message:    message,
		Attributes: attrs,
	})
}
