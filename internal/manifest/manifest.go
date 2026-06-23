package manifest

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	"quack/internal/protocol"

	"gopkg.in/yaml.v3"
)

const MaxBytes int64 = 64 << 10

type Manifest struct {
	Features Features `json:"features" yaml:"features"`
	Exclude  []string `json:"exclude" yaml:"exclude"`
	Routes   []Route  `json:"routes" yaml:"routes"`
}

type Features struct {
	Database FeatureFlag `json:"database" yaml:"database"`
}

type FeatureFlag struct {
	Enabled  bool `json:"enabled" yaml:"enabled"`
	Required bool `json:"required" yaml:"required"`
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
	exclude, err := protocol.NormalizeExcludePatterns(manifest.Exclude)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Exclude = exclude
	if err := validateRoutes(manifest.Routes); err != nil {
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
