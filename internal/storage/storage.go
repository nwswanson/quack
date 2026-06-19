package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Storage interface {
	AcceptFile(ctx context.Context, file StoredFile) (StoredFileResult, error)
	OpenBlob(ctx context.Context, blobPath string) (*os.File, error)
	DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error
	DeleteSite(ctx context.Context, siteSHA string) error
}

type StoredFile struct {
	SiteSHA      string
	Version      int64
	RelativePath string
	Mode         int64
	Size         int64
	Body         io.Reader
}

type StoredFileResult struct {
	BlobPath string
	FileSHA  string
	Bytes    int64
}

type BlobStorage struct {
	root string
}

func NewBlobStorage(root string) (*BlobStorage, error) {
	if root == "" {
		return nil, fmt.Errorf("root is required")
	}
	return &BlobStorage{
		root: root,
	}, nil
}

func (s *BlobStorage) AcceptFile(ctx context.Context, file StoredFile) (StoredFileResult, error) {
	if err := ctx.Err(); err != nil {
		return StoredFileResult{}, err
	}
	if file.SiteSHA == "" {
		return StoredFileResult{}, fmt.Errorf("site sha is required")
	}
	if file.Version <= 0 {
		return StoredFileResult{}, fmt.Errorf("version is required")
	}

	versionDir := filepath.Join(s.root, "blobs", "site:"+file.SiteSHA, fmt.Sprintf("%d", file.Version))
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return StoredFileResult{}, fmt.Errorf("create blob directory: %w", err)
	}

	tmp, err := os.CreateTemp(versionDir, "incoming-*")
	if err != nil {
		return StoredFileResult{}, fmt.Errorf("create temp blob: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hash := sha256.New()
	w := io.MultiWriter(tmp, hash)
	bytes, copyErr := io.Copy(w, file.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		return StoredFileResult{}, fmt.Errorf("write temp blob: %w", copyErr)
	}
	if closeErr != nil {
		return StoredFileResult{}, fmt.Errorf("close temp blob: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return StoredFileResult{}, err
	}
	if file.Size >= 0 && bytes != file.Size {
		return StoredFileResult{}, fmt.Errorf("blob size mismatch for %s: got %d want %d", file.RelativePath, bytes, file.Size)
	}

	fileSHA := hex.EncodeToString(hash.Sum(nil))
	blobPath := filepath.Join(versionDir, "file:"+fileSHA)
	if err := os.Rename(tmpPath, blobPath); err != nil {
		return StoredFileResult{}, fmt.Errorf("commit blob: %w", err)
	}

	relBlobPath, err := filepath.Rel(s.root, blobPath)
	if err != nil {
		return StoredFileResult{}, fmt.Errorf("relative blob path: %w", err)
	}
	return StoredFileResult{
		BlobPath: filepath.ToSlash(relBlobPath),
		FileSHA:  fileSHA,
		Bytes:    bytes,
	}, nil
}

func (s *BlobStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clean := filepath.Clean(filepath.FromSlash(blobPath))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return nil, fmt.Errorf("invalid blob path: %s", blobPath)
	}
	return os.Open(filepath.Join(s.root, clean))
}

func (s *BlobStorage) DeleteSite(ctx context.Context, siteSHA string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if siteSHA == "" || strings.Contains(siteSHA, string(filepath.Separator)) || strings.Contains(siteSHA, "/") || strings.Contains(siteSHA, "..") {
		return fmt.Errorf("invalid site sha: %s", siteSHA)
	}
	return os.RemoveAll(filepath.Join(s.root, "blobs", "site:"+siteSHA))
}

func (s *BlobStorage) DeleteSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if siteSHA == "" || strings.Contains(siteSHA, string(filepath.Separator)) || strings.Contains(siteSHA, "/") || strings.Contains(siteSHA, "..") {
		return fmt.Errorf("invalid site sha: %s", siteSHA)
	}
	if version <= 0 {
		return fmt.Errorf("invalid version: %d", version)
	}
	return os.RemoveAll(filepath.Join(s.root, "blobs", "site:"+siteSHA, fmt.Sprintf("%d", version)))
}
