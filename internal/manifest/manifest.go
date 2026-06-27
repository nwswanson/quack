package manifest

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"

	"quack/internal/protocol"

	"gopkg.in/yaml.v3"
)

const MaxBytes int64 = 64 << 10

type Manifest struct {
	Features     Features     `json:"features" yaml:"features"`
	Capabilities Capabilities `json:"capabilities" yaml:"capabilities"`
	Exclude      []string     `json:"exclude" yaml:"exclude"`
	Routes       []Route      `json:"routes" yaml:"routes"`
	APIProxies   []APIProxy   `json:"api_proxies" yaml:"api_proxies"`
	Pipes        []Pipe       `json:"pipes" yaml:"pipes"`
	Events       []EventRoute `json:"events" yaml:"events"`
}

type Features struct {
	Database FeatureFlag `json:"database" yaml:"database"`
	Camera   FeatureFlag `json:"camera" yaml:"camera"`
}

type FeatureFlag struct {
	Enabled  bool `json:"enabled" yaml:"enabled"`
	Required bool `json:"required" yaml:"required"`
}

type Capabilities struct {
	Camera map[string]CameraCapability `json:"camera" yaml:"camera"`
}

type CameraCapability struct {
	Required    bool                            `json:"required" yaml:"required"`
	Description string                          `json:"description" yaml:"description"`
	Permissions map[string]CapabilityPermission `json:"permissions" yaml:"permissions"`
	Limits      CameraCapabilityLimits          `json:"limits" yaml:"limits"`
}

type CapabilityPermission struct {
	Roles []string `json:"roles" yaml:"roles"`
}

type CameraCapabilityLimits struct {
	MaxWidth             int `json:"max_width" yaml:"max_width"`
	MaxHeight            int `json:"max_height" yaml:"max_height"`
	MaxFPS               int `json:"max_fps" yaml:"max_fps"`
	MaxDurationSeconds   int `json:"max_duration_seconds" yaml:"max_duration_seconds"`
	MaxCapturesPerMinute int `json:"max_captures_per_minute" yaml:"max_captures_per_minute"`
}

type RouteKind string

const (
	RouteStatic    RouteKind = "static"
	RouteHTTP      RouteKind = "http"
	RouteWebSocket RouteKind = "websocket"
)

type Route struct {
	Path         string           `json:"path" yaml:"path"`
	Kind         RouteKind        `json:"kind" yaml:"kind"`
	Root         string           `json:"root,omitempty" yaml:"root,omitempty"`
	File         string           `json:"file,omitempty" yaml:"file,omitempty"`
	Runtime      string           `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Entrypoint   string           `json:"entrypoint" yaml:"entrypoint"`
	Methods      []string         `json:"methods,omitempty" yaml:"methods,omitempty"`
	ExposeErrors *bool            `json:"expose_errors,omitempty" yaml:"expose_errors,omitempty"`
	Filesystem   *RouteFilesystem `json:"filesystem,omitempty" yaml:"filesystem,omitempty"`
}

type RouteFilesystem struct {
	Root string `json:"root,omitempty" yaml:"root,omitempty"`
}

type APIProxy struct {
	Name        string   `json:"name" yaml:"name"`
	Domain      string   `json:"domain,omitempty" yaml:"domain,omitempty"`
	PathFixed   string   `json:"path_fixed,omitempty" yaml:"path_fixed,omitempty"`
	Methods     []string `json:"methods,omitempty" yaml:"methods,omitempty"`
	MethodsAll  bool     `json:"-" yaml:"-"`
	InsecureSSL bool     `json:"insecure_ssl,omitempty" yaml:"insecure_ssl,omitempty"`
}

type Pipe struct {
	Name      string `json:"name" yaml:"name"`
	Retain    int    `json:"retain,omitempty" yaml:"retain,omitempty"`
	Unlimited bool   `json:"unlimited,omitempty" yaml:"unlimited,omitempty"`
	Overflow  string `json:"overflow,omitempty" yaml:"overflow,omitempty"`
}

type EventRoute struct {
	Selector string `json:"selector" yaml:"selector"`
	OnEvent  string `json:"on_event" yaml:"on_event"`
}

func Default() Manifest {
	return Manifest{}
}

func Parse(r io.Reader, size int64) (Manifest, error) {
	if size > MaxBytes {
		return Manifest{}, fmt.Errorf("site.yaml exceeds %d bytes", MaxBytes)
	}
	limited := io.LimitReader(r, MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Manifest{}, fmt.Errorf("read site.yaml: %w", err)
	}
	if int64(len(data)) > MaxBytes {
		return Manifest{}, fmt.Errorf("site.yaml exceeds %d bytes", MaxBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Default(), nil
	}

	var manifest Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid site.yaml: %w", err)
	}
	if manifest.Features.Database.Required && !manifest.Features.Database.Enabled {
		return Manifest{}, fmt.Errorf("database.required cannot be true when database.enabled is false")
	}
	if manifest.Features.Camera.Required && !manifest.Features.Camera.Enabled {
		return Manifest{}, fmt.Errorf("camera.required cannot be true when camera.enabled is false")
	}
	exclude, err := protocol.NormalizeExcludePatterns(manifest.Exclude)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Exclude = exclude
	if err := validateRoutes(manifest.Routes); err != nil {
		return Manifest{}, err
	}
	if err := validateCapabilities(manifest.Capabilities); err != nil {
		return Manifest{}, err
	}
	if err := validateAPIProxies(manifest.APIProxies); err != nil {
		return Manifest{}, err
	}
	if err := validatePipes(manifest.Pipes); err != nil {
		return Manifest{}, err
	}
	if err := validateEvents(manifest.Events); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func SanitizeStaticRoot(root string) (string, error) {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	if strings.HasPrefix(root, "/") {
		return "", fmt.Errorf("static.root must be relative")
	}
	root = strings.Trim(root, "/")
	if root == "" || root == "." {
		return "", nil
	}
	if strings.Contains(root, "../") || strings.Contains(root, "/..") || root == ".." {
		return "", fmt.Errorf("static.root cannot contain ..")
	}
	clean := path.Clean(root)
	if clean == "." {
		return "", nil
	}
	sanitized, err := protocol.SanitizeServingPath(clean)
	if err != nil {
		return "", fmt.Errorf("invalid static.root: %w", err)
	}
	return sanitized, nil
}

func SanitizeStaticFile(file string) (string, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return "", nil
	}
	sanitized, err := protocol.SanitizeServingPath(file)
	if err != nil {
		return "", fmt.Errorf("invalid static file: %w", err)
	}
	return sanitized, nil
}

func SanitizeFilesystemRoot(root string) (string, error) {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	root = strings.Trim(root, "/")
	if root == "" || root == "." {
		return "", nil
	}
	if strings.Contains(root, "../") || strings.Contains(root, "/..") || root == ".." {
		return "", fmt.Errorf("filesystem.root cannot contain ..")
	}
	sanitized, err := protocol.SanitizeServingPath(path.Clean(root))
	if err != nil {
		return "", fmt.Errorf("invalid filesystem.root: %w", err)
	}
	return sanitized, nil
}

func validateRoutes(routes []Route) error {
	for i, route := range routes {
		if route.Path == "" {
			return fmt.Errorf("route.path is required")
		}
		switch route.Kind {
		case "", RouteStatic, RouteHTTP, RouteWebSocket:
		default:
			return fmt.Errorf("unsupported route kind %q", route.Kind)
		}
		switch strings.TrimSpace(route.Runtime) {
		case "", "starlark":
		default:
			return fmt.Errorf("unsupported route runtime %q", route.Runtime)
		}
		if strings.TrimSpace(route.Root) != "" && route.Kind != "" && route.Kind != RouteStatic {
			return fmt.Errorf("route.root is only supported for static routes")
		}
		if strings.TrimSpace(route.File) != "" && route.Kind != "" && route.Kind != RouteStatic {
			return fmt.Errorf("route.file is only supported for static routes")
		}
		if strings.TrimSpace(route.Root) != "" && strings.TrimSpace(route.File) != "" {
			return fmt.Errorf("route.root and route.file cannot both be set")
		}
		if strings.TrimSpace(route.Root) != "" {
			root, err := SanitizeStaticRoot(route.Root)
			if err != nil {
				return fmt.Errorf("invalid route.root: %w", err)
			}
			routes[i].Root = root
		}
		if strings.TrimSpace(route.File) != "" {
			file, err := SanitizeStaticFile(route.File)
			if err != nil {
				return fmt.Errorf("invalid route.file: %w", err)
			}
			routes[i].File = file
		}
		if strings.TrimSpace(route.Runtime) != "" && route.Kind != RouteHTTP && route.Kind != RouteWebSocket {
			return fmt.Errorf("route.runtime is only supported for http and websocket routes")
		}
		if strings.TrimSpace(route.Runtime) != "" && strings.TrimSpace(route.Entrypoint) == "" {
			return fmt.Errorf("route.entrypoint is required when route.runtime is set")
		}
		if route.Filesystem != nil {
			if route.Kind != RouteHTTP || strings.TrimSpace(route.Runtime) != "starlark" {
				return fmt.Errorf("route.filesystem is only supported for starlark http routes")
			}
			root, err := SanitizeFilesystemRoot(route.Filesystem.Root)
			if err != nil {
				return fmt.Errorf("invalid route.filesystem.root: %w", err)
			}
			routes[i].Filesystem.Root = root
		}
		for _, method := range route.Methods {
			if strings.TrimSpace(method) == "" {
				return fmt.Errorf("route.methods cannot contain an empty method")
			}
		}
	}
	return nil
}

func validateCapabilities(capabilities Capabilities) error {
	for alias, camera := range capabilities.Camera {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return fmt.Errorf("capabilities.camera alias is required")
		}
		if strings.Contains(alias, "/") || strings.Contains(alias, "\\") || strings.Contains(alias, ":") {
			return fmt.Errorf("capabilities.camera[%q] must be a logical alias, not a path or URI", alias)
		}
		for name, permission := range camera.Permissions {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("capabilities.camera[%q].permissions cannot contain an empty permission", alias)
			}
			for _, role := range permission.Roles {
				if strings.TrimSpace(role) == "" {
					return fmt.Errorf("capabilities.camera[%q].permissions[%q].roles cannot contain an empty role", alias, name)
				}
			}
		}
	}
	return nil
}

var apiProxyNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateAPIProxies(proxies []APIProxy) error {
	seen := map[string]struct{}{}
	for i := range proxies {
		proxy := &proxies[i]
		proxy.Name = strings.TrimSpace(proxy.Name)
		if proxy.Name == "" {
			return fmt.Errorf("api_proxies[%d].name is required", i)
		}
		if !apiProxyNamePattern.MatchString(proxy.Name) || strings.Contains(proxy.Name, ".") || strings.Contains(proxy.Name, "://") {
			return fmt.Errorf("api_proxies[%d].name must contain only letters, numbers, underscores, and dashes", i)
		}
		if _, ok := seen[proxy.Name]; ok {
			return fmt.Errorf("api_proxies[%d].name duplicates %q", i, proxy.Name)
		}
		seen[proxy.Name] = struct{}{}

		domain := strings.TrimSpace(proxy.Domain)
		pathFixed := strings.TrimSpace(proxy.PathFixed)
		switch {
		case domain == "" && pathFixed == "":
			return fmt.Errorf("api_proxies[%d] must set exactly one of domain or path_fixed", i)
		case domain != "" && pathFixed != "":
			return fmt.Errorf("api_proxies[%d] cannot set both domain and path_fixed", i)
		case domain != "":
			normalized, err := normalizeAPIProxyDomain(domain)
			if err != nil {
				return fmt.Errorf("invalid api_proxies[%d].domain: %w", i, err)
			}
			proxy.Domain = normalized
		case pathFixed != "":
			normalized, err := normalizeAPIProxyPathFixed(pathFixed)
			if err != nil {
				return fmt.Errorf("invalid api_proxies[%d].path_fixed: %w", i, err)
			}
			proxy.PathFixed = normalized
		}

		methods, all, err := normalizeAPIProxyMethods(proxy.Methods)
		if err != nil {
			return fmt.Errorf("invalid api_proxies[%d].methods: %w", i, err)
		}
		proxy.Methods = methods
		proxy.MethodsAll = all
	}
	return nil
}

func validatePipes(pipes []Pipe) error {
	seen := map[string]bool{}
	for _, pipe := range pipes {
		name := strings.TrimSpace(pipe.Name)
		if name == "" {
			return fmt.Errorf("pipe.name is required")
		}
		if err := validateEventName(name, "pipe.name"); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicate pipe %q", name)
		}
		seen[name] = true
		if pipe.Retain < 0 {
			return fmt.Errorf("pipe.retain cannot be negative")
		}
		switch strings.TrimSpace(pipe.Overflow) {
		case "", "drop_oldest", "drop_new":
		default:
			return fmt.Errorf("unsupported pipe overflow %q", pipe.Overflow)
		}
	}
	return nil
}

func validateEvents(events []EventRoute) error {
	for _, event := range events {
		selector := strings.TrimSpace(event.Selector)
		if selector == "" {
			return fmt.Errorf("event.selector is required")
		}
		if strings.Contains(selector, "*") && !strings.HasSuffix(selector, "*") {
			return fmt.Errorf("event.selector wildcard is only supported as the last character")
		}
		base := strings.TrimSuffix(selector, "*")
		base = strings.TrimSuffix(base, ".")
		if base == "" {
			return fmt.Errorf("event.selector must name a pipe prefix")
		}
		if err := validateEventName(base, "event.selector"); err != nil {
			return err
		}
		if _, _, err := SplitEventHandler(event.OnEvent); err != nil {
			return err
		}
	}
	return nil
}

func SplitEventHandler(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	file, handler, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(file) == "" || strings.TrimSpace(handler) == "" {
		return "", "", fmt.Errorf("event.on_event must be formatted as file.star:function")
	}
	file = strings.TrimSpace(file)
	handler = strings.TrimSpace(handler)
	if _, err := SanitizeStaticFile(file); err != nil {
		return "", "", fmt.Errorf("invalid event.on_event file: %w", err)
	}
	if !strings.HasSuffix(file, ".star") {
		return "", "", fmt.Errorf("event.on_event file must be a .star module")
	}
	if !identifierRE.MatchString(handler) {
		return "", "", fmt.Errorf("event.on_event function %q is invalid", handler)
	}
	return file, handler, nil
}

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateEventName(value string, field string) error {
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return fmt.Errorf("%s cannot contain empty name segments", field)
		}
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return fmt.Errorf("%s contains unsupported character %q", field, r)
		}
	}
	return nil
}

func normalizeAPIProxyDomain(value string) (string, error) {
	if strings.Contains(value, "://") {
		return "", fmt.Errorf("must not include a scheme")
	}
	if strings.ContainsAny(value, "/?#") {
		return "", fmt.Errorf("must not include a path, query, or fragment")
	}
	host := value
	port := ""
	if h, p, err := net.SplitHostPort(value); err == nil {
		host, port = h, p
	} else if strings.Contains(err.Error(), "missing port in address") {
		host = value
	} else if strings.Contains(value, ":") && strings.Count(value, ":") == 1 {
		return "", fmt.Errorf("invalid host or port")
	}
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if strings.Contains(host, "*") {
		return "", fmt.Errorf("wildcards are not supported")
	}
	if port != "" {
		if _, err := net.LookupPort("tcp", port); err != nil {
			return "", fmt.Errorf("invalid port")
		}
		return net.JoinHostPort(host, port), nil
	}
	return host, nil
}

func normalizeAPIProxyPathFixed(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("must be an absolute http or https URL")
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	if u.User != nil {
		return "", fmt.Errorf("userinfo is not allowed")
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("fragment is not allowed")
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("query is not allowed")
	}
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("path is required")
	}
	u.Host = strings.ToLower(u.Host)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizeAPIProxyMethods(values []string) ([]string, bool, error) {
	if len(values) == 0 {
		return []string{"GET"}, false, nil
	}
	allowed := map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		method := strings.ToUpper(strings.TrimSpace(value))
		if method == "" {
			return nil, false, fmt.Errorf("cannot contain an empty method")
		}
		if method == "ALL" {
			return []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}, true, nil
		}
		if !allowed[method] {
			return nil, false, fmt.Errorf("unsupported method %q", value)
		}
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		out = append(out, method)
	}
	return out, false, nil
}
