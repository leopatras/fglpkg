package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func TestNewestAndNewestStable(t *testing.T) {
	vs := []semver.Version{
		semver.MustParse("1.0.0"),
		semver.MustParse("1.2.0"),
		semver.MustParse("2.0.0-rc.1"),
		semver.MustParse("1.5.0"),
	}
	n := newest(vs)
	if n == nil || n.String() != "2.0.0-rc.1" {
		t.Errorf("newest = %v, want 2.0.0-rc.1", n)
	}
	s := newestStable(vs)
	if s == nil || s.String() != "1.5.0" {
		t.Errorf("newestStable = %v, want 1.5.0", s)
	}
	if newest([]semver.Version{}) != nil {
		t.Error("newest of empty slice should be nil")
	}
	if newestStable([]semver.Version{semver.MustParse("1.0.0-alpha")}) != nil {
		t.Error("newestStable of prerelease-only should be nil")
	}
}

// outdatedStub serves a single package named "demo" with three versions
// via the new consumer protocol (/registry/packages/<slug>).
func outdatedStub(t *testing.T) *httptest.Server {
	t.Helper()
	detail := map[string]any{
		"slug": "demo",
		"name": "demo",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "a", "download_url": "https://example.com/demo-1.0.0.zip"},
			}},
			{"version": "1.2.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "b", "download_url": "https://example.com/demo-1.2.0.zip"},
			}},
			{"version": "2.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "c", "download_url": "https://example.com/demo-2.0.0.zip"},
			}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/packages/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/registry/packages/")
		if slug != "demo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(detail)
	})
	return httptest.NewServer(mux)
}

func TestBuildOutdatedRowStatuses(t *testing.T) {
	ts := outdatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	cases := []struct {
		name       string
		constraint string
		current    string
		wantStatus string
		wantWanted string
		wantLatest string
	}{
		{"update_in_range", "^1.0.0", "1.0.0", "update available", "1.2.0", "2.0.0"},
		{"major_outside_range", "^1.0.0", "1.2.0", "major available", "1.2.0", "2.0.0"},
		{"already_latest_in_range", "^2.0.0", "2.0.0", "ok", "2.0.0", "2.0.0"},
		{"not_installed", "^1.0.0", "", "not installed", "1.2.0", "2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := buildOutdatedRow(nil, "demo", tc.constraint, tc.current, "")
			if row.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", row.Status, tc.wantStatus)
			}
			if row.Wanted != tc.wantWanted {
				t.Errorf("wanted = %q, want %q", row.Wanted, tc.wantWanted)
			}
			if row.Latest != tc.wantLatest {
				t.Errorf("latest = %q, want %q", row.Latest, tc.wantLatest)
			}
		})
	}
}

func TestBuildOutdatedRowRegistryError(t *testing.T) {
	t.Setenv("FGLPKG_REGISTRY", "http://127.0.0.1:1") // unreachable
	row := buildOutdatedRow(nil, "demo", "^1.0.0", "1.0.0", "")
	if row.Status != "registry error" {
		t.Errorf("status = %q, want %q", row.Status, "registry error")
	}
}

// TestCmdOutdatedEndToEnd exercises the full command: parses a manifest,
// reads the lockfile, talks to a stub registry, emits the table, and
// returns a non-nil error so CI can exit non-zero.
func TestCmdOutdatedEndToEnd(t *testing.T) {
	ts := outdatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "proj",
  "version": "0.1.0",
  "dependencies": { "fgl": { "demo": "^1.0.0" } }
}`)
	write("fglpkg.lock", `{
  "lockfileVersion": 1,
  "generatedAt": "2026-04-23T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": { "name": "proj", "version": "0.1.0" },
  "packages": [
    {
      "name": "demo",
      "version": "1.0.0",
      "downloadUrl": "https://example.com/x.zip",
      "requiredBy": ["<root>"]
    }
  ],
  "jars": []
}`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	stdout, cmdErr := captureStdout(t, func() error {
		return cmdOutdated([]string{})
	})
	if cmdErr == nil {
		t.Fatal("expected non-nil error (demo is outdated), got nil")
	}
	if !strings.Contains(cmdErr.Error(), "out of date") {
		t.Errorf("error %q should contain 'out of date'", cmdErr.Error())
	}
	for _, sub := range []string{"demo", "1.0.0", "1.2.0", "2.0.0", "update available"} {
		if !strings.Contains(stdout, sub) {
			t.Errorf("table missing %q\n---\n%s", sub, stdout)
		}
	}
}

func TestCmdOutdatedJSON(t *testing.T) {
	ts := outdatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(`{
  "name": "proj", "version": "0.1.0",
  "dependencies": { "fgl": { "demo": "^2.0.0" } }
}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	stdout, _ := captureStdout(t, func() error {
		return cmdOutdated([]string{"--json"})
	})
	var rows []outdatedRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if len(rows) != 1 || rows[0].Name != "demo" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestCmdOutdatedNoDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(`{
  "name": "empty", "version": "0.0.1",
  "dependencies": { "fgl": {} }
}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := cmdOutdated([]string{}); err != nil {
		t.Errorf("expected nil error for empty deps, got %v", err)
	}
}
