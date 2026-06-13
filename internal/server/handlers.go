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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"quack/internal/protocol"
)

type handler struct {
	token          string
	store          Storage
	db             Database
	maxUploadBytes int64
	maxUploadFiles int64
}

func (h *handler) routes(mux *http.ServeMux) {
	mux.HandleFunc(protocol.UploadArchivePath, h.handleUploadArchive)
	mux.HandleFunc(protocol.DeleteSitePathPrefix, h.handleDeleteSite)
	mux.HandleFunc("/serve/", h.handleServeExplicitSite)
	mux.HandleFunc("/", h.handleServeFile)
}

func (h *handler) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !authorized(r, h.token) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	site, ok := siteFromDeletePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "site is required")
		return
	}
	siteSHA := sha256Hex(site)
	deleted, err := h.db.DeleteSite(r.Context(), site, siteSHA)
	if err != nil {
		log.Printf("delete site metadata failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if deleted {
		if err := h.store.DeleteSite(r.Context(), siteSHA); err != nil {
			log.Printf("delete site blobs failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	writeJSON(w, http.StatusOK, protocol.DeleteSiteResponse{
		OK:      true,
		Site:    site,
		Deleted: deleted,
	})
}

func (h *handler) handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site := siteFromHost(r.Host)
	if site == "" {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, r.URL.Path)
}

func (h *handler) handleServeExplicitSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site, filePath, ok := siteAndPathFromServePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, filePath)
}

func (h *handler) serveSiteFile(w http.ResponseWriter, r *http.Request, site string, urlPath string) {
	relativePath, wantsIndex := requestedRelativePath(urlPath)
	file, ok, err := h.db.FindCurrentFile(r.Context(), site, relativePath)
	if err != nil {
		log.Printf("lookup file failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		if wantsIndex {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
		return
	}

	blob, err := h.store.OpenBlob(r.Context(), file.BlobPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		log.Printf("open blob failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer blob.Close()

	http.ServeContent(w, r, relativePath, time.Time{}, blob)
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
	if h.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	}

	siteSHA := sha256Hex(site)
	version, err := h.db.AllocateVersion(r.Context(), site, siteSHA)
	if err != nil {
		log.Printf("allocate upload version: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	files, bytes, err := h.acceptArchive(r, site, siteSHA, version)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		var limitErr uploadLimitError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds maximum size")
			return
		}
		if errors.As(err, &limitErr) {
			writeError(w, http.StatusRequestEntityTooLarge, limitErr.Error())
			return
		}
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

func (h *handler) acceptArchive(r *http.Request, site string, siteSHA string, version int64) (int64, int64, error) {
	tr := tar.NewReader(r.Body)
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
			if err := h.db.SaveUpload(r.Context(), upload); err != nil {
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
			if h.maxUploadFiles > 0 && files >= h.maxUploadFiles {
				return 0, 0, uploadLimitError{err: fmt.Errorf("upload exceeds maximum file count: %d", h.maxUploadFiles)}
			}
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

func siteFromHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	} else if strings.Count(host, ":") == 1 {
		host = strings.Split(host, ":")[0]
	}
	host = strings.Trim(host, ".")
	if host == "" {
		return ""
	}
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	return host
}

func requestedRelativePath(urlPath string) (string, bool) {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if clean == "/" || clean == "." {
		return "index.html", true
	}
	relative := strings.TrimPrefix(clean, "/")
	if strings.HasSuffix(urlPath, "/") {
		relative = path.Join(relative, "index.html")
	}
	sanitized, err := sanitizeServingPath(relative)
	if err != nil {
		return "index.html", true
	}
	return sanitized, sanitized == "index.html"
}

func siteAndPathFromServePath(urlPath string) (string, string, bool) {
	rest := strings.TrimPrefix(urlPath, "/serve/")
	if rest == urlPath || rest == "" {
		return "", "", false
	}

	site, filePath, found := strings.Cut(rest, "/")
	if site == "" {
		return "", "", false
	}

	site, err := url.PathUnescape(site)
	if err != nil {
		return "", "", false
	}
	site = strings.TrimSpace(site)
	if site == "" {
		return "", "", false
	}

	if !found || filePath == "" {
		return site, "/", true
	}
	return site, "/" + filePath, true
}

func siteFromDeletePath(urlPath string) (string, bool) {
	site := strings.TrimPrefix(urlPath, protocol.DeleteSitePathPrefix)
	if site == urlPath || site == "" || strings.Contains(site, "/") {
		return "", false
	}
	site, err := url.PathUnescape(site)
	if err != nil {
		return "", false
	}
	site = strings.TrimSpace(site)
	return site, site != ""
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

func (e badArchiveError) Unwrap() error {
	return e.err
}

type uploadLimitError struct {
	err error
}

func (e uploadLimitError) Error() string {
	return e.err.Error()
}

func (e uploadLimitError) Unwrap() error {
	return e.err
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.UploadArchiveResponse{
		OK:    false,
		Error: message,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
