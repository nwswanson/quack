package devserver

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/protocol"
	appruntime "quack/internal/runtime"
	"quack/internal/sites"
	"quack/internal/uploads"
)

type DevSiteSource struct {
	Site       string
	SiteSHA    string
	Generation int64
	RootDir    string
	Manifest   manifest.Manifest
	Settings   map[string]string
	Files      map[string]domain.UploadFileRecord
	Routes     []appruntime.RouteMetadata
	LoadedAt   time.Time
}

func LoadSiteSource(ctx context.Context, rootDir string, site string, generation int64) (*DevSiteSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve build directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat build directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("build directory is not a directory: %s", root)
	}
	if generation <= 0 {
		generation = 1
	}
	siteManifest, manifestName, err := readManifest(root)
	if err != nil {
		return nil, err
	}
	site = strings.TrimSpace(site)
	if site == "" {
		site = filepath.Base(root)
	}
	if site == "." || site == string(filepath.Separator) || strings.ContainsAny(site, `/\`) {
		return nil, fmt.Errorf("site name is required")
	}
	files, err := scanFiles(ctx, root, siteManifest.Exclude)
	if err != nil {
		return nil, err
	}
	if manifestName != "" {
		if file, ok := fileRecord(root, manifestName); ok {
			files[manifestName] = file
		}
	}
	source := &DevSiteSource{
		Site:       site,
		SiteSHA:    sites.HashName(site),
		Generation: generation,
		RootDir:    root,
		Manifest:   siteManifest,
		Settings:   uploads.ManifestSettings(siteManifest),
		Files:      files,
		LoadedAt:   time.Now(),
	}
	routes, err := runtimeRoutes(source)
	if err != nil {
		return nil, err
	}
	source.Routes = routes
	return source, nil
}

func readManifest(root string) (manifest.Manifest, string, error) {
	for _, name := range []string{"site.yml", "site.yaml"} {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return manifest.Manifest{}, "", fmt.Errorf("stat %s: %w", name, err)
		}
		if info.IsDir() {
			return manifest.Manifest{}, "", fmt.Errorf("%s is a directory", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return manifest.Manifest{}, "", fmt.Errorf("open %s: %w", name, err)
		}
		parsed, parseErr := manifest.Parse(file, info.Size())
		closeErr := file.Close()
		if parseErr != nil {
			return manifest.Manifest{}, "", parseErr
		}
		if closeErr != nil {
			return manifest.Manifest{}, "", fmt.Errorf("close %s: %w", name, closeErr)
		}
		return parsed, name, nil
	}
	return manifest.Manifest{}, "", fmt.Errorf("site.yml or site.yaml is required")
}

func scanFiles(ctx context.Context, root string, excludes []string) (map[string]domain.UploadFileRecord, error) {
	out := map[string]domain.UploadFileRecord{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == root {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldIgnore(rel, entry.IsDir(), excludes) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		record, ok := fileRecord(root, rel)
		if ok {
			out[record.RelativePath] = record
		}
		return nil
	})
	return out, err
}

func fileRecord(root string, rel string) (domain.UploadFileRecord, bool) {
	clean, err := protocol.SanitizeServingPath(rel)
	if err != nil {
		return domain.UploadFileRecord{}, false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil || info.IsDir() {
		return domain.UploadFileRecord{}, false
	}
	return domain.UploadFileRecord{
		RelativePath: clean,
		BlobPath:     devBlobPath(clean),
		FileSHA:      weakFileSHA(clean, info),
		Bytes:        info.Size(),
	}, true
}

func shouldIgnore(rel string, isDir bool, excludes []string) bool {
	base := path.Base(rel)
	defaults := []string{".git", "node_modules", ".quack-dev", ".DS_Store", "*.swp", "*.tmp"}
	for _, pattern := range defaults {
		if ignorePattern(rel, base, pattern, isDir) {
			return true
		}
	}
	for _, pattern := range excludes {
		if ignorePattern(rel, base, pattern, isDir) {
			return true
		}
	}
	return false
}

func ignorePattern(rel string, base string, pattern string, isDir bool) bool {
	pattern = strings.Trim(strings.ReplaceAll(pattern, "\\", "/"), "/")
	if pattern == "" {
		return false
	}
	if matched, _ := path.Match(pattern, rel); matched {
		return true
	}
	if matched, _ := path.Match(pattern, base); matched {
		return true
	}
	return rel == pattern || strings.HasPrefix(rel, pattern+"/") || (isDir && base == pattern)
}

func weakFileSHA(rel string, info os.FileInfo) string {
	return "dev-" + strconv.FormatInt(info.ModTime().UnixNano(), 36) + "-" + strconv.FormatInt(info.Size(), 36) + "-" + strings.NewReplacer("/", "_", `"`, "_").Replace(rel)
}

func runtimeRoutes(source *DevSiteSource) ([]appruntime.RouteMetadata, error) {
	var out []appruntime.RouteMetadata
	for _, route := range source.Manifest.Routes {
		switch route.Kind {
		case manifest.RouteHTTP, manifest.RouteWebSocket:
		default:
			continue
		}
		runtimeKind := appruntime.RuntimeDisabled
		bundleObjectKey := ""
		if route.Runtime == string(appruntime.RuntimeStarlark) {
			runtimeKind = appruntime.RuntimeStarlark
			entrypointPath, err := protocol.SanitizeServingPath(route.Entrypoint)
			if err != nil {
				return nil, err
			}
			file, ok := source.Files[entrypointPath]
			if !ok {
				return nil, fmt.Errorf("runtime entrypoint %q was not found in build directory", route.Entrypoint)
			}
			bundleObjectKey = file.BlobPath
		}
		filesystemEnabled := false
		filesystemRoot := ""
		if route.Filesystem != nil {
			filesystemEnabled = true
			filesystemRoot = route.Filesystem.Root
		}
		exposeErrors := true
		if route.ExposeErrors != nil {
			exposeErrors = *route.ExposeErrors
		}
		out = append(out, appruntime.RouteMetadata{
			Site:                 source.Site,
			SiteSHA:              source.SiteSHA,
			Version:              source.Generation,
			RuntimeKind:          runtimeKind,
			RouteKind:            appruntime.RouteKind(route.Kind),
			RoutePath:            route.Path,
			Entrypoint:           route.Entrypoint,
			BundleObjectKey:      bundleObjectKey,
			Methods:              append([]string(nil), route.Methods...),
			ExposeErrors:         exposeErrors,
			FilesystemEnabled:    filesystemEnabled,
			FilesystemRoot:       filesystemRoot,
			RequiredCapabilities: runtimeCapabilities(route.Kind, source.Manifest),
			WASM:                 cloneWASMModules(source.Manifest.WASM.Modules),
			ResourceLimits:       routeResourceLimits(route.Limits),
			CreatedAt:            source.LoadedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

func routeResourceLimits(limits *manifest.RouteLimits) appruntime.ResourceLimits {
	out := appruntime.ResourceLimits{
		MaxRequestBytes:                appruntime.DefaultMaxRequestBytes,
		MaxResponseBytes:               appruntime.DefaultMaxResponseBytes,
		MaxDurationMillis:              appruntime.DefaultMaxDuration.Milliseconds(),
		MaxMemoryBytes:                 appruntime.DefaultMaxMemoryBytes,
		MaxConcurrency:                 appruntime.DefaultMaxConcurrentInvocations,
		MaxExecutionSteps:              appruntime.DefaultMaxExecutionSteps,
		MaxScriptBytes:                 appruntime.DefaultMaxScriptBytes,
		MaxWebSocketConnections:        appruntime.DefaultMaxWebSocketConnections,
		MaxWebSocketConnectionsPerSite: appruntime.DefaultMaxWebSocketConnectionsPerSite,
	}
	if limits == nil {
		return out
	}
	if limits.MaxRequestBytes > 0 {
		out.MaxRequestBytes = limits.MaxRequestBytes
	}
	if limits.MaxResponseBytes > 0 {
		out.MaxResponseBytes = limits.MaxResponseBytes
	}
	if limits.MaxDurationMS > 0 {
		out.MaxDurationMillis = limits.MaxDurationMS
	}
	if limits.MaxMemoryBytes > 0 {
		out.MaxMemoryBytes = limits.MaxMemoryBytes
	}
	if limits.MaxConcurrency > 0 {
		out.MaxConcurrency = limits.MaxConcurrency
	}
	if limits.MaxExecutionSteps > 0 {
		out.MaxExecutionSteps = limits.MaxExecutionSteps
	}
	if limits.MaxScriptBytes > 0 {
		out.MaxScriptBytes = limits.MaxScriptBytes
	}
	if limits.MaxWebSocketConnections > 0 {
		out.MaxWebSocketConnections = limits.MaxWebSocketConnections
	}
	if limits.MaxWebSocketConnectionsPerSite > 0 {
		out.MaxWebSocketConnectionsPerSite = limits.MaxWebSocketConnectionsPerSite
	}
	return out
}

func cloneWASMModules(in map[string]manifest.WASMModule) map[string]manifest.WASMModule {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]manifest.WASMModule, len(in))
	for name, module := range in {
		module.Imports = append([]string(nil), module.Imports...)
		if module.Execution.Interruptible != nil {
			interruptible := *module.Execution.Interruptible
			module.Execution.Interruptible = &interruptible
		}
		out[name] = module
	}
	return out
}

func runtimeCapabilities(kind manifest.RouteKind, siteManifest manifest.Manifest) []string {
	var out []string
	switch kind {
	case manifest.RouteHTTP:
		out = append(out, "runtime.http")
	case manifest.RouteWebSocket:
		out = append(out, "runtime.websocket")
	}
	if siteManifest.Features.Camera.Enabled || len(siteManifest.Capabilities.Camera) > 0 {
		out = append(out, "hardware.camera")
	}
	return out
}
