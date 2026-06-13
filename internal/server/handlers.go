package server

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"quack/internal/protocol"
)

type handler struct {
	token string
	store Storage
}

func (h *handler) routes(mux *http.ServeMux) {
	mux.HandleFunc(protocol.UploadArchivePath, h.handleUploadArchive)
}

func (h *handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !authorized(r, h.token) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != protocol.ContentTypeTar {
		writeError(w, http.StatusBadRequest, "content type must be application/x-tar")
		return
	}
	site := strings.TrimSpace(r.Header.Get(protocol.HeaderSite))
	if site == "" {
		writeError(w, http.StatusBadRequest, "site is required")
		return
	}

	version := int64(1)
	files, bytes, err := h.acceptArchive(r, site, version)
	if err != nil {
		var badRequest badArchiveError
		if errors.As(err, &badRequest) {
			writeError(w, http.StatusBadRequest, badRequest.Error())
			return
		}
		log.Printf("upload failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	log.Printf("accepted upload: files=%d bytes=%d", files, bytes)
	writeJSON(w, http.StatusOK, protocol.UploadArchiveResponse{
		OK:      true,
		Site:    site,
		Version: version,
		Files:   files,
		Bytes:   bytes,
	})
}

func (h *handler) acceptArchive(r *http.Request, site string, version int64) (int64, int64, error) {
	tr := tar.NewReader(r.Body)
	siteSHA := sha256Hex(site)
	upload := UploadRecord{
		Site:    site,
		SiteSHA: siteSHA,
		Version: version,
	}
	var files int64
	var bytes int64

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			if err := h.store.SaveUpload(r.Context(), upload); err != nil {
				return 0, 0, fmt.Errorf("save upload metadata: %w", err)
			}
			return files, bytes, nil
		}
		if err != nil {
			return 0, 0, badArchiveError{err: fmt.Errorf("read tar archive: %w", err)}
		}
		if err := validateArchivePath(header.Name); err != nil {
			return 0, 0, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
			sanitizedPath, err := sanitizeServingPath(header.Name)
			if err != nil {
				return 0, 0, err
			}
			result, err := h.store.AcceptFile(r.Context(), StoredFile{
				SiteSHA:      siteSHA,
				Version:      version,
				RelativePath: sanitizedPath,
				Mode:         header.Mode,
				Size:         header.Size,
				Body:         tr,
			})
			if err != nil {
				return 0, 0, fmt.Errorf("accept file %s: %w", header.Name, err)
			}
			upload.Files = append(upload.Files, UploadFileRecord{
				RelativePath: sanitizedPath,
				BlobPath:     result.BlobPath,
				FileSHA:      result.FileSHA,
				Bytes:        result.Bytes,
			})
			files++
			bytes += result.Bytes
		default:
			return 0, 0, badArchiveError{err: fmt.Errorf("unsupported archive entry type for %s", header.Name)}
		}
	}
}

func validateArchivePath(name string) error {
	if name == "" {
		return badArchiveError{err: errors.New("archive path is empty")}
	}
	if strings.HasPrefix(name, "/") {
		return badArchiveError{err: fmt.Errorf("archive path must be relative: %s", name)}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return badArchiveError{err: fmt.Errorf("archive path cannot contain ..: %s", name)}
		}
	}
	if clean := path.Clean(name); clean == "." {
		return badArchiveError{err: fmt.Errorf("archive path cannot contain ..: %s", name)}
	}
	return nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sanitizeServingPath(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if err := validateArchivePath(clean); err != nil {
		return "", err
	}

	parts := strings.Split(clean, "/")
	for i, part := range parts {
		parts[i] = sanitizePathPart(part)
	}
	return strings.Join(parts, "/"), nil
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

type badArchiveError struct {
	err error
}

func (e badArchiveError) Error() string {
	return e.err.Error()
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.UploadArchiveResponse{
		OK:    false,
		Error: message,
	})
}

func writeJSON(w http.ResponseWriter, status int, body protocol.UploadArchiveResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
