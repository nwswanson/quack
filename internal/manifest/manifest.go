package manifest

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const MaxBytes int64 = 64 << 10

type Manifest struct {
	Features Features `yaml:"features"`
	Routes   []Route  `yaml:"routes"`
}

type Features struct {
	Database FeatureFlag `yaml:"database"`
}

type FeatureFlag struct {
	Enabled  bool `yaml:"enabled"`
	Required bool `yaml:"required"`
}

type RouteKind string

const (
	RouteStatic RouteKind = "static"
)

type Route struct {
	Path       string    `yaml:"path"`
	Kind       RouteKind `yaml:"kind"`
	Entrypoint string    `yaml:"entrypoint"`
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
		case "", RouteStatic:
		default:
			return fmt.Errorf("unsupported route kind %q", route.Kind)
		}
	}
	return nil
}
