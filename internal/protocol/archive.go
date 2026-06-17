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

func WriteTar(ctx context.Context, root string, w io.Writer) error {
	if err := validateDirectory(root); err != nil {
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
	return path.Clean(header.Name) == "site.yaml" && (header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA)
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
