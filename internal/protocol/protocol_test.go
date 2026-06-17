package protocol

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRouteBuilders(t *testing.T) {
	const baseURL = "http://example.test/"

	if got, want := UploadArchiveURL(baseURL), "http://example.test/v1/uploads/archive"; got != want {
		t.Fatalf("UploadArchiveURL = %q, want %q", got, want)
	}
	if got, want := SiteURL(baseURL, "foo/bar"), "http://example.test/v1/sites/foo%2Fbar"; got != want {
		t.Fatalf("SiteURL = %q, want %q", got, want)
	}

	sitesURL, err := SitesURL(baseURL, ListSitesRequest{Username: " alice ", IncludeAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sitesURL, "http://example.test/v1/sites?all=true&user=alice"; got != want {
		t.Fatalf("SitesURL = %q, want %q", got, want)
	}
}

func TestDecodeResponseUsesBodyFallbackForNonJSONError(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{
		StatusCode: http.StatusRequestEntityTooLarge,
		Status:     http.StatusText(http.StatusRequestEntityTooLarge),
		Body:       io.NopCloser(strings.NewReader("request body too large\n")),
		Request:    req,
	}

	out, err := DecodeResponse[UploadArchiveResponse](resp)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out.Error, "request body too large"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestParseSiteManifestRejectsInvalidDatabaseRequirement(t *testing.T) {
	_, err := ParseSiteManifest(strings.NewReader("features:\n  database:\n    required: true\n"), 45)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "database.required cannot be true") {
		t.Fatalf("error = %q, want database requirement detail", err.Error())
	}
}

func TestSanitizeServingPath(t *testing.T) {
	got, err := SanitizeServingPath("docs/My File.html")
	if err != nil {
		t.Fatal(err)
	}
	if want := "docs/My_File.html"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	if err := ValidateArchivePath("../index.html"); err == nil {
		t.Fatal("expected invalid archive path")
	}
}

func TestPlanTransfers(t *testing.T) {
	plan := PlanTransfers(
		[]FileIdentity{
			{Path: "a.txt", SHA: "same", Size: 1},
			{Path: "old.txt", SHA: "old", Size: 3},
		},
		[]FileIdentity{
			{Path: "a.txt", SHA: "same", Size: 1},
			{Path: "b.txt", SHA: "new", Size: 2},
		},
	)

	actions := map[string]TransferAction{}
	for _, file := range plan.Files {
		actions[file.Path] = file.Action
	}
	if actions["a.txt"] != TransferCopyFromPrevious {
		t.Fatalf("a.txt action = %q, want copy", actions["a.txt"])
	}
	if actions["b.txt"] != TransferUpload {
		t.Fatalf("b.txt action = %q, want upload", actions["b.txt"])
	}
	if actions["old.txt"] != TransferDelete {
		t.Fatalf("old.txt action = %q, want delete", actions["old.txt"])
	}
}
