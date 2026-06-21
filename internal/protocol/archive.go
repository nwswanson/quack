package protocol

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type WriteTarOptions struct {
	Exclude []string
}

func WriteTar(ctx context.Context, root string, w io.Writer) error {
	return WriteTarWithOptions(ctx, root, w, WriteTarOptions{})
}

func WriteTarWithOptions(ctx context.Context, root string, w io.Writer, options WriteTarOptions) error {
	if err := validateDirectory(root); err != nil {
		return err
	}
	excludes, err := NewExcludeMatcher(options.Exclude)
	if err != nil {
		return err
	}

	root = filepath.Clean(root)
	tw := tar.NewWriter(w)
	defer tw.Close()

	return filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", filePath, err)
		}

		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", filePath, err)
		}
		if rel == "." {
			return nil
		}

		name := filepath.ToSlash(rel)
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			return nil
		}
		if !isSiteManifestPath(name) && excludes.Match(name, mode.IsDir()) {
			if mode.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("create tar header for %s: %w", filePath, err)
		}
		header.Name = name

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %s: %w", filePath, err)
		}
		if mode.IsDir() {
			return nil
		}

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("open %s: %w", filePath, err)
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("copy %s into archive: %w", filePath, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", filePath, closeErr)
		}
		return nil
	})
}

type ExcludeMatcher struct {
	patterns []excludePattern
}

type excludePattern struct {
	pattern    string
	tree       bool
	pathScoped bool
}

func NewExcludeMatcher(patterns []string) (ExcludeMatcher, error) {
	normalized, err := NormalizeExcludePatterns(patterns)
	if err != nil {
		return ExcludeMatcher{}, err
	}

	matcher := ExcludeMatcher{patterns: make([]excludePattern, 0, len(normalized))}
	for _, pattern := range normalized {
		if strings.HasSuffix(pattern, "/**") {
			matcher.patterns = append(matcher.patterns, excludePattern{
				pattern:    strings.TrimSuffix(pattern, "/**"),
				tree:       true,
				pathScoped: true,
			})
			continue
		}
		matcher.patterns = append(matcher.patterns, excludePattern{pattern: pattern})
	}
	return matcher, nil
}

func NormalizeExcludePatterns(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		normalized, err := normalizeExcludePattern(pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func (m ExcludeMatcher) Match(name string, isDir bool) bool {
	name = strings.Trim(path.Clean(strings.ReplaceAll(name, "\\", "/")), "/")
	if name == "." || isSiteManifestPath(name) {
		return false
	}

	base := path.Base(name)
	for _, pattern := range m.patterns {
		if pattern.tree {
			if matchesTreePattern(pattern, name) {
				return true
			}
			continue
		}

		if strings.Contains(pattern.pattern, "/") {
			if ok, _ := path.Match(pattern.pattern, name); ok {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern.pattern, base); ok {
			return true
		}
	}
	return false
}

func normalizeExcludePattern(pattern string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if normalized == "" {
		return "", errors.New("exclude patterns cannot be empty")
	}
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("exclude pattern must be relative: %s", pattern)
	}

	trailingSlash := strings.HasSuffix(normalized, "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" || normalized == "." {
		return "", fmt.Errorf("exclude pattern must name a relative path or glob: %s", pattern)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", fmt.Errorf("exclude pattern cannot contain ..: %s", pattern)
		}
	}

	matchPattern := normalized
	if strings.HasSuffix(matchPattern, "/**") {
		matchPattern = strings.TrimSuffix(matchPattern, "/**")
	} else if strings.HasSuffix(matchPattern, "/") {
		matchPattern = strings.TrimSuffix(matchPattern, "/") + "/**"
	}
	if _, err := path.Match(matchPattern, matchPattern); err != nil {
		return "", fmt.Errorf("invalid exclude pattern %q: %w", pattern, err)
	}

	if trailingSlash {
		normalized += "/**"
	}
	return normalized, nil
}

func matchesTreePattern(pattern excludePattern, name string) bool {
	if pattern.pathScoped {
		for candidate := name; candidate != ""; {
			if ok, _ := path.Match(pattern.pattern, candidate); ok {
				return true
			}
			i := strings.LastIndex(candidate, "/")
			if i < 0 {
				break
			}
			candidate = candidate[:i]
		}
		return false
	}

	if ok, _ := path.Match(pattern.pattern, path.Base(name)); ok {
		return true
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if ok, _ := path.Match(pattern.pattern, part); ok {
			return true
		}
	}
	return false
}

func ValidateArchivePath(name string) error {
	if name == "" {
		return errors.New("archive path is empty")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("archive path must be relative: %s", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("archive path cannot contain ..: %s", name)
		}
	}
	if clean := path.Clean(name); clean == "." {
		return fmt.Errorf("archive path cannot contain ..: %s", name)
	}
	return nil
}

func SanitizeServingPath(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if err := ValidateArchivePath(clean); err != nil {
		return "", err
	}

	parts := strings.Split(clean, "/")
	for i, part := range parts {
		parts[i] = sanitizePathPart(part)
	}
	return strings.Join(parts, "/"), nil
}

func IsSiteManifestArchiveEntry(header *tar.Header) bool {
	name := path.Clean(header.Name)
	return isSiteManifestPath(name) && (header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA)
}

func isSiteManifestPath(name string) bool {
	name = path.Clean(name)
	return name == "site.yaml" || name == "site.yml"
}

func validateDirectory(directory string) error {
	if directory == "" {
		return fmt.Errorf("directory is required")
	}

	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("directory is not a directory: %s", directory)
	}
	return nil
}

func sanitizePathPart(part string) string {
	var b strings.Builder
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
}
