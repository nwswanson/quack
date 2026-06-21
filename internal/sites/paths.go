package sites

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"quack/internal/protocol"
)

func NameFromHost(host string) string {
	host = NormalizeHost(host)
	if host == "" {
		return ""
	}
	host = strings.TrimPrefix(host, "www.")
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	site, err := CanonicalName(host)
	if err != nil {
		return ""
	}
	return site
}

func CanonicalName(value string) (string, error) {
	site := strings.TrimSpace(strings.ToLower(value))
	site = strings.Trim(site, ".")
	if site == "" {
		return "", fmt.Errorf("site is required")
	}
	if len(site) > 63 {
		return "", fmt.Errorf("site must be 63 characters or fewer")
	}
	if strings.Contains(site, ".") {
		return "", fmt.Errorf("site must be a single DNS label")
	}
	if strings.HasPrefix(site, "-") || strings.HasSuffix(site, "-") {
		return "", fmt.Errorf("site cannot start or end with hyphen")
	}
	switch site {
	case "v1", "serve":
		return "", fmt.Errorf("site name is reserved")
	}
	for _, r := range site {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return "", fmt.Errorf("site must contain only lowercase letters, numbers, and hyphens")
		}
	}
	return site, nil
}

func RequestedRelativePath(urlPath string) (string, bool) {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if clean == "/" || clean == "." {
		return "index.html", true
	}
	relative := strings.TrimPrefix(clean, "/")
	if strings.HasSuffix(urlPath, "/") {
		relative = path.Join(relative, "index.html")
	}
	sanitized, err := protocol.SanitizeServingPath(relative)
	if err != nil {
		return "index.html", true
	}
	return sanitized, sanitized == "index.html"
}

func ShouldTryDirectoryIndex(urlPath string, relativePath string, wantsIndex bool) bool {
	if wantsIndex || strings.HasSuffix(urlPath, "/") {
		return false
	}
	return path.Base(relativePath) != "index.html"
}

func DirectoryRedirectPath(r *http.Request, prefix string, urlPath string) string {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if clean == "/" {
		clean = ""
	}
	target := prefix + clean + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target
}

func SiteAndPathFromServePath(urlPath string) (string, string, bool) {
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

func SiteFromDeletePath(urlPath string) (string, bool) {
	site := strings.TrimPrefix(urlPath, protocol.DeleteSitePathPrefix)
	if site == urlPath || site == "" || strings.Contains(site, "/") {
		return "", false
	}
	site, err := url.PathUnescape(site)
	if err != nil {
		return "", false
	}
	site = strings.TrimSpace(site)
	site, err = CanonicalName(site)
	if err != nil {
		return "", false
	}
	return site, true
}

func SiteFromSuffixedSitePath(urlPath string, suffix string) (string, bool) {
	withoutSuffix, ok := strings.CutSuffix(urlPath, suffix)
	if !ok {
		return "", false
	}
	site := strings.TrimPrefix(withoutSuffix, protocol.DeleteSitePathPrefix)
	if site == withoutSuffix || site == "" || strings.Contains(site, "/") {
		return "", false
	}
	site, err := url.PathUnescape(site)
	if err != nil {
		return "", false
	}
	site = strings.TrimSpace(site)
	site, err = CanonicalName(site)
	if err != nil {
		return "", false
	}
	return site, true
}

func HashName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
