package manifest

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const MaxBytes int64 = 64 << 10

type Manifest struct {
	Features Features `json:"features" yaml:"features"`
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
	Path       string    `json:"path" yaml:"path"`
	Kind       RouteKind `json:"kind" yaml:"kind"`
	Runtime    string    `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Entrypoint string    `json:"entrypoint" yaml:"entrypoint"`
	Methods    []string  `json:"methods,omitempty" yaml:"methods,omitempty"`
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
	if err := validateRoutes(manifest.Routes); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateRoutes(routes []Route) error {
	for _, route := range routes {
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
		if strings.TrimSpace(route.Runtime) != "" && route.Kind != RouteHTTP {
			return fmt.Errorf("route.runtime is only supported for http routes")
		}
		if strings.TrimSpace(route.Runtime) != "" && strings.TrimSpace(route.Entrypoint) == "" {
			return fmt.Errorf("route.entrypoint is required when route.runtime is set")
		}
		for _, method := range route.Methods {
			if strings.TrimSpace(method) == "" {
				return fmt.Errorf("route.methods cannot contain an empty method")
			}
		}
	}
	return nil
}
