package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// mustBuildPackage stages + zips the project once, matching the built package
// that enforceLint hands to publishPackage in production.
func mustBuildPackage(t *testing.T, m *manifest.Manifest) *builtPackage {
	t.Helper()
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	entries, err := listZipEntries(zipData)
	if err != nil {
		t.Fatalf("listZipEntries: %v", err)
	}
	return &builtPackage{zip: zipData, checksum: checksum, entries: entries}
}

// TestPublishPackageDryRunNoNetwork verifies that publishPackage with
// dryRun=true returns successfully without performing any network I/O.
// The tokens passed in are deliberately bogus; if the function tried to
// contact GitHub or the registry it would fail with an auth or connection
// error, which this test would surface.
func TestPublishPackageDryRunNoNetwork(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "dryrun-test",
  "version": "1.0.0",
  "description": "test",
  "author": "me",
  "license": "UNLICENSED",
  "dependencies": { "fgl": {} }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = publishPackage(
		m,
		"http://127.0.0.1:1", // registryURL — unreachable; if dry-run violates the contract the test will fail
		"6",                  // generoMajor
		true,                 // dryRun
		"",                   // visibilityOverride — use manifest default
		"",                   // changelogText
		mustBuildPackage(t, m),
	)
	if err != nil {
		t.Fatalf("dry-run publishPackage returned error: %v", err)
	}

	// The zip is held only in memory during dry-run; no files should be
	// left behind in the working directory beyond what the test created.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	allowed := map[string]bool{"fglpkg.json": true, "Main.42m": true}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Errorf("unexpected file left behind after dry-run: %s", e.Name())
		}
	}
}

// TestPublishPackageDryRunListsMetadata verifies the dry-run preview prints
// the rich metadata block: scalar fields, dependency counts, README size,
// and a (truncated) flag for an oversized USERGUIDE.
func TestPublishPackageDryRunListsMetadata(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "meta-test",
  "version": "1.0.0",
  "description": "test",
  "author": "Acme <dev@acme.com>",
  "license": "MIT",
  "repository": "https://github.com/acme/meta-test",
  "genero": "^6.0.0",
  "dependencies": { "fgl": { "json-path": "^1.0.0" }, "java": [ { "groupId": "com.acme", "artifactId": "x", "version": "1.2.3" } ] }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")
	write("README.md", "# Meta Test")
	write("USERGUIDE.md", strings.Repeat("a", maxReadmeBytes+100)) // forces truncation

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Capture stdout for the duration of the dry-run.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	runErr := publishPackage(m, "http://127.0.0.1:1", "6", true, "", "", mustBuildPackage(t, m))
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if runErr != nil {
		t.Fatalf("dry-run publishPackage returned error: %v", runErr)
	}

	wantSubstrings := []string{
		"metadata:",
		"repository:   https://github.com/acme/meta-test",
		"author:       Acme <dev@acme.com>",
		"license:      MIT",
		"genero:       ^6.0.0",
		"dependencies: 1 fgl, 1 java",
		"readme:       11 B", // "# Meta Test" is 11 bytes, shown in bytes not KB
		"userguide:",         // size line present
		"(truncated)",        // oversized USERGUIDE flagged
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n---output---\n%s", want, out)
		}
	}
}

// TestPublishPackageDryRunChangelog verifies that a CHANGELOG.md section for
// the version being published shows a non-empty changelog size in the dry-run
// preview.
func TestPublishPackageDryRunChangelog(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "cl-test",
  "version": "1.2.0",
  "description": "test",
  "author": "me",
  "license": "MIT",
  "dependencies": { "fgl": {} }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")
	write("CHANGELOG.md", "## [1.2.0] - 2026-07-13\n\n### Added\n- The changelog feature.\n\n## [1.1.0]\n\n- Older.\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out, runErr := captureDryRun(t, func() error {
		return publishPackage(m, "http://127.0.0.1:1", "6", true, "", "", mustBuildPackage(t, m))
	})
	if runErr != nil {
		t.Fatalf("dry-run publishPackage returned error: %v", runErr)
	}
	if !strings.Contains(out, "changelog:") {
		t.Errorf("dry-run output missing changelog line\n---output---\n%s", out)
	}
	// "## [1.1.0]" section must NOT leak into the 1.2.0 changelog.
	if strings.Contains(out, "Older") {
		t.Errorf("dry-run changelog leaked a different version's section\n---output---\n%s", out)
	}
	if strings.Contains(out, "changelog:(none)") || strings.Contains(out, "changelog:0.0 KB (none)") {
		t.Errorf("expected a non-empty changelog size, got:\n%s", out)
	}
}

// TestPublishPackageDryRunChangelogMissingSection verifies the soft warning
// when CHANGELOG.md exists but has no entry for the published version.
func TestPublishPackageDryRunChangelogMissingSection(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "cl-miss",
  "version": "2.0.0",
  "description": "test",
  "author": "me",
  "license": "MIT",
  "dependencies": { "fgl": {} }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")
	write("CHANGELOG.md", "## [1.0.0]\n\n- Only the old one.\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out, runErr := captureDryRun(t, func() error {
		return publishPackage(m, "http://127.0.0.1:1", "6", true, "", "", mustBuildPackage(t, m))
	})
	if runErr != nil {
		t.Fatalf("dry-run publishPackage returned error: %v", runErr)
	}
	if !strings.Contains(out, "no entry for 2.0.0") {
		t.Errorf("expected missing-section warning\n---output---\n%s", out)
	}
}

// TestPublishPackageDryRunSyncsMetadata verifies the dry-run preview shows the
// GIS-268 F/G metadata-sync PATCH with the manifest's description and keywords.
func TestPublishPackageDryRunSyncsMetadata(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "meta-sync",
  "version": "1.0.0",
  "description": "a searchable blurb",
  "author": "me",
  "license": "MIT",
  "keywords": ["alpha", "beta"],
  "dependencies": { "fgl": {} }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out, runErr := captureDryRun(t, func() error {
		return publishPackage(m, "http://127.0.0.1:1", "6", true, "", "", mustBuildPackage(t, m))
	})
	if runErr != nil {
		t.Fatalf("dry-run publishPackage returned error: %v", runErr)
	}
	for _, want := range []string{
		"would PATCH",
		"/registry/packages/meta-sync",
		"a searchable blurb",
		"alpha",
		"beta",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n---output---\n%s", want, out)
		}
	}
}

// captureDryRun redirects os.Stdout for the duration of fn and returns what
// it printed.
func captureDryRun(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String(), err
}
