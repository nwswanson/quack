package client

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
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

	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", path, err)
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
			return fmt.Errorf("create tar header for %s: %w", path, err)
		}
		header.Name = name

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %s: %w", path, err)
		}
		if mode.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("copy %s into archive: %w", path, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", path, closeErr)
		}
		return nil
	})
}
