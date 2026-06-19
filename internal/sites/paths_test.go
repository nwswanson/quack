package sites

import "testing"

func TestNameFromHost(t *testing.T) {
	tests := map[string]string{
		"foo.bar.domain.com": "foo",
		"domain.com":         "domain",
		"foo.domain.com":     "foo",
		"foo.example.com:80": "foo",
		"LOCALHOST:8080":     "localhost",
		"www.foo.com":        "foo",
		"WWW.foo.com:443":    "foo",
		"bad_site.example":   "",
		"v1.example.com":     "",
	}

	for input, want := range tests {
		got := NameFromHost(input)
		if got != want {
			t.Fatalf("NameFromHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalName(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
		ok    bool
	}{
		"lowercases": {input: " Foo ", want: "foo", ok: true},
		"hyphen":     {input: "foo-bar", want: "foo-bar", ok: true},
		"dots":       {input: "foo.example", ok: false},
		"underscore": {input: "foo_bar", ok: false},
		"reserved":   {input: "serve", ok: false},
		"leading":    {input: "-foo", ok: false},
		"trailing":   {input: "foo-", ok: false},
	}

	for name, tc := range tests {
		got, err := CanonicalName(tc.input)
		if tc.ok && err != nil {
			t.Fatalf("%s: CanonicalName returned error: %v", name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: CanonicalName returned nil error", name)
		}
		if got != tc.want {
			t.Fatalf("%s: CanonicalName = %q, want %q", name, got, tc.want)
		}
	}
}

func TestRequestedRelativePath(t *testing.T) {
	tests := map[string]struct {
		path       string
		want       string
		wantsIndex bool
	}{
		"root":      {path: "/", want: "index.html", wantsIndex: true},
		"file":      {path: "/file.js", want: "file.js", wantsIndex: false},
		"nested":    {path: "/assets/app.js", want: "assets/app.js", wantsIndex: false},
		"directory": {path: "/docs/", want: "docs/index.html", wantsIndex: false},
		"sanitized": {path: "/My File!.html", want: "My_File_.html", wantsIndex: false},
		"traversal": {path: "/../file.js", want: "file.js", wantsIndex: false},
	}

	for name, tc := range tests {
		got, wantsIndex := RequestedRelativePath(tc.path)
		if got != tc.want || wantsIndex != tc.wantsIndex {
			t.Fatalf("%s: RequestedRelativePath(%q) = (%q, %v), want (%q, %v)", name, tc.path, got, wantsIndex, tc.want, tc.wantsIndex)
		}
	}
}

func TestSiteAndPathFromServePath(t *testing.T) {
	tests := map[string]struct {
		path     string
		site     string
		filePath string
		ok       bool
	}{
		"missing site": {path: "/serve/", ok: false},
		"site root":    {path: "/serve/foo", site: "foo", filePath: "/", ok: true},
		"site slash":   {path: "/serve/foo/", site: "foo", filePath: "/", ok: true},
		"site file":    {path: "/serve/foo/file.js", site: "foo", filePath: "/file.js", ok: true},
		"nested file":  {path: "/serve/foo/assets/app.js", site: "foo", filePath: "/assets/app.js", ok: true},
		"escaped site": {path: "/serve/foo%20bar/file.js", site: "foo bar", filePath: "/file.js", ok: true},
	}

	for name, tc := range tests {
		site, filePath, ok := SiteAndPathFromServePath(tc.path)
		if site != tc.site || filePath != tc.filePath || ok != tc.ok {
			t.Fatalf("%s: SiteAndPathFromServePath(%q) = (%q, %q, %v), want (%q, %q, %v)", name, tc.path, site, filePath, ok, tc.site, tc.filePath, tc.ok)
		}
	}
}
