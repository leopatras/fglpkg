package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// stagePackTestDir writes the given files into a fresh temp dir and Chdirs
// into it, since buildPackageZip reads from the current working directory.
func stagePackTestDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// zipEntries reads a built archive into a name->contents map.
func zipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]string{}
	for _, f := range r.File {
		body, err := readZipEntry(f)
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		got[f.Name] = body
	}
	return got
}

// TestBuildPackageZip_ImportRootRebases is the reported case: compiled modules
// live under lib/com/fourjs/... but must ship as com/fourjs/... so imports
// resolve after install. The shipped manifest's root is rewritten to the
// post-strip layout and importRoot is dropped.
func TestBuildPackageZip_ImportRootRebases(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "lib/com/fourjs/fglpkgtest",
  "importRoot": "lib",
  "files": ["*.42m"],
  "programs": ["ModuleA"],
  "dependencies": { "fgl": {} }
}`,
		"lib/com/fourjs/fglpkgtest/ModuleA.42m": "MAIN END MAIN\n",
		"lib/com/fourjs/fglpkgtest/ModuleB.42m": "FUNCTION f() END FUNCTION\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	got := zipEntries(t, data)

	for _, want := range []string{
		"com/fourjs/fglpkgtest/ModuleA.42m",
		"com/fourjs/fglpkgtest/ModuleB.42m",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q in archive; got %v", want, keys(boolKeys(got)))
		}
	}
	for name := range got {
		if strings.HasPrefix(name, "lib/") {
			t.Errorf("archive entry still carries lib/ prefix: %s", name)
		}
	}

	mfRaw := got["fglpkg.json"]
	if !strings.Contains(mfRaw, `"root": "com/fourjs/fglpkgtest"`) {
		t.Errorf("shipped manifest root not rewritten to post-strip layout:\n%s", mfRaw)
	}
	if strings.Contains(mfRaw, "importRoot") {
		t.Errorf("shipped manifest should not carry importRoot:\n%s", mfRaw)
	}
}

// TestBuildPackageZip_IncludeFoldsToArchiveRoot verifies that an `include`
// entry is copied to the top of importRoot (the archive root) under its
// basename, without any change to the source tree.
func TestBuildPackageZip_IncludeFoldsToArchiveRoot(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "lib/com/fourjs/x",
  "importRoot": "lib",
  "files": ["*.42m"],
  "include": ["dist/app.4st"],
  "dependencies": { "fgl": {} }
}`,
		"lib/com/fourjs/x/ModuleA.42m": "MAIN END MAIN\n",
		"dist/app.4st":                 "<style/>\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	got := zipEntries(t, data)

	if _, ok := got["app.4st"]; !ok {
		t.Errorf("expected include file at archive root as app.4st; got %v", keys(boolKeys(got)))
	}
	if _, ok := got["com/fourjs/x/ModuleA.42m"]; !ok {
		t.Errorf("expected rebased module com/fourjs/x/ModuleA.42m; got %v", keys(boolKeys(got)))
	}
	if _, ok := got["dist/app.4st"]; ok {
		t.Error("include file should be stored by basename, not at its source path dist/app.4st")
	}
}

// TestBuildPackageZip_OutsideImportRootErrors: a matched file outside
// importRoot with no include mapping must fail loudly rather than emit a
// "../" archive entry.
func TestBuildPackageZip_OutsideImportRootErrors(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "importRoot": "lib",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"lib/com/fourjs/x/ModuleA.42m": "MAIN END MAIN\n",
		"Stray.42m":                    "MAIN END MAIN\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := buildPackageZip(m); err == nil {
		t.Fatal("expected an error for a matched file outside importRoot")
	}
}

// TestBuildPackageZip_IncludeCollisionErrors: two include entries that flatten
// to the same basename collide (the sharp edge of the top-of-importRoot model).
func TestBuildPackageZip_IncludeCollisionErrors(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "include": ["a/util.txt", "b/util.txt"],
  "dependencies": { "fgl": {} }
}`,
		"a/util.txt": "a\n",
		"b/util.txt": "b\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := buildPackageZip(m); err == nil {
		t.Fatal("expected a collision error for two includes sharing a basename")
	}
}

// TestBuildPackageZip_Deterministic: packing the same tree twice yields
// byte-identical archives (constant entry metadata + sorted order).
func TestBuildPackageZip_Deterministic(t *testing.T) {
	files := map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "lib",
  "importRoot": "lib",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"lib/com/fourjs/x/ModuleA.42m": "MAIN END MAIN\n",
		"lib/com/fourjs/x/ModuleB.42m": "FUNCTION f() END FUNCTION\n",
	}
	stagePackTestDir(t, files)
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, sum1, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip (1): %v", err)
	}
	_, sum2, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip (2): %v", err)
	}
	if sum1 != sum2 {
		t.Errorf("archive not reproducible: %s != %s", sum1, sum2)
	}
}
