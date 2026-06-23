package devserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appstorage "quack/internal/storage"
)

const devBlobPrefix = "dev:"

type DevStorage struct {
	RootDir string
}

func NewStorage(rootDir string) DevStorage {
	return DevStorage{RootDir: rootDir}
}

func (s DevStorage) AcceptFile(ctx context.Context, file appstorage.StoredFile) (appstorage.StoredFileResult, error) {
	return appstorage.StoredFileResult{}, fmt.Errorf("accept file is unsupported in dev mode")
}

func (s DevStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, err := devBlobRelativePath(blobPath)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(s.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve dev root: %w", err)
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve dev blob: %w", err)
	}
	if cleanTarget != root && !strings.HasPrefix(cleanTarget, root+string(filepath.Separator)) {
		return nil, fmt.Errorf("dev blob escapes root: %s", blobPath)
	}
	return os.Open(cleanTarget)
}

func (s DevStorage) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("delete site version is unsupported in dev mode")
}

func (s DevStorage) DeleteSite(ctx context.Context, siteSHA string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("delete site is unsupported in dev mode")
}

func devBlobPath(relativePath string) string {
	return devBlobPrefix + strings.TrimLeft(filepath.ToSlash(relativePath), "/")
}

func devBlobRelativePath(blobPath string) (string, error) {
	if !strings.HasPrefix(blobPath, devBlobPrefix) {
		return "", fmt.Errorf("invalid dev blob path: %s", blobPath)
	}
	rel := strings.TrimPrefix(blobPath, devBlobPrefix)
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("invalid dev blob path: %s", blobPath)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid dev blob path: %s", blobPath)
	}
	return clean, nil
}
