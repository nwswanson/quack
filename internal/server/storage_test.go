package server

import (
	"testing"

	"quack/internal/protocol"
)

func TestSanitizeServingPath(t *testing.T) {
	tests := map[string]string{
		"index.html":               "index.html",
		"docs/My Page!.html":       "docs/My_Page_.html",
		"assets/css/site main.css": "assets/css/site_main.css",
		".../file":                 "_/file",
	}

	for input, want := range tests {
		got, err := protocol.SanitizeServingPath(input)
		if err != nil {
			t.Fatalf("sanitizeServingPath(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("sanitizeServingPath(%q) = %q, want %q", input, got, want)
		}
	}
}
