package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

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
