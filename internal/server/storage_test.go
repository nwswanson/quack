package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobStorageAcceptFileWritesHashedBlob(t *testing.T) {
	root := t.TempDir()
	store, err := NewBlobStorage(root)
	if err != nil {
		t.Fatal(err)
	}

	siteSHA := sha256String("example.com")
	content := "hello from quack\n"
	result, err := store.AcceptFile(context.Background(), StoredFile{
		SiteSHA:      siteSHA,
		Version:      1,
		RelativePath: "index.html",
		Size:         int64(len(content)),
		Body:         strings.NewReader(content),
	})
	if err != nil {
		t.Fatal(err)
	}

	fileSHA := sha256String(content)
	wantRel := filepath.ToSlash(filepath.Join("blobs", "site:"+siteSHA, "1", "file:"+fileSHA))
	if result.BlobPath != wantRel {
		t.Fatalf("blob path = %q, want %q", result.BlobPath, wantRel)
	}
	if result.FileSHA != fileSHA {
		t.Fatalf("file sha = %q, want %q", result.FileSHA, fileSHA)
	}
	if result.Bytes != int64(len(content)) {
		t.Fatalf("bytes = %d, want %d", result.Bytes, len(content))
	}

	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.BlobPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("blob content = %q, want %q", string(got), content)
	}
}

func TestSanitizeServingPath(t *testing.T) {
	tests := map[string]string{
		"index.html":               "index.html",
		"docs/My Page!.html":       "docs/My_Page_.html",
		"assets/css/site main.css": "assets/css/site_main.css",
		".../file":                 "_/file",
	}

	for input, want := range tests {
		got, err := sanitizeServingPath(input)
		if err != nil {
			t.Fatalf("sanitizeServingPath(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("sanitizeServingPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
