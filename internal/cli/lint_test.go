package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// writeLintProject materializes files under a fresh temp dir and Chdirs into
// it, so the cwd-relative staging walks (buildPackageZip, loadIgnore, …) run
// against the fixture. Returns the project dir.
func writeLintProject(t *testing.T, files map[string]string) string {
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
	return dir
}

// loadLintReport loads the fixture manifest and runs the full lint pass.
func loadLintReport(t *testing.T) *manifest.Report {
	t.Helper()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return lintManifest(m, ".")
}

// warningFields collects the Field of every warning diagnostic.
func warningFields(r *manifest.Report) []string {
	var out []string
	for _, d := range r.Warnings() {
		out = append(out, d.Field)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestLintCleanManifest(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "clean",
  "version": "1.0.0",
  "description": "A clean package",
  "license": "MIT",
  "repository": "https://github.com/acme/clean",
  "author": "Acme",
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Errorf("clean manifest should have no errors, got %+v", r.Errors())
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("clean manifest should have no warnings, got %+v", r.Warnings())
	}
}

func TestLintZeroMatchFilesWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "zm",
  "version": "1.0.0",
  "files": ["*.42m", "*.42x"],
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Fatalf("expected no errors (a .42m is present), got %+v", r.Errors())
	}
	if !contains(warningFields(r), "files") {
		t.Errorf("expected a zero-match files warning for *.42x, got %+v", r.Warnings())
	}
}

func TestLintNoModulesError(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "empty",
  "version": "1.0.0",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"README.md": "# empty\n",
	})
	r := loadLintReport(t)
	if !r.HasErrors() {
		t.Fatalf("a package that stages no modules must be an error, got %+v", r.Diagnostics)
	}
	var msg string
	for _, d := range r.Errors() {
		msg += d.Message
	}
	if !strings.Contains(msg, "no BDL modules") {
		t.Errorf("error should mention no BDL modules, got: %s", msg)
	}
}

func TestLintUnresolvedProgramWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "prog",
  "version": "1.0.0",
  "programs": ["Main", "Missing"],
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Fatalf("expected no errors, got %+v", r.Errors())
	}
	if !contains(warningFields(r), "programs") {
		t.Errorf("expected an unresolved-program warning for Missing, got %+v", r.Warnings())
	}
}

func TestLintNonexistentRootWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "badroot",
  "version": "1.0.0",
  "root": "nope",
  "dependencies": { "fgl": {} }
}`,
	})
	r := loadLintReport(t)
	if !contains(warningFields(r), "root") {
		t.Errorf("expected a nonexistent-root warning, got %+v", r.Warnings())
	}
	// A missing root that breaks the BDL staging walk must ALSO surface as a
	// blocking error, so `lint` fails and pack/publish refuse — rather than the
	// old behaviour where lint warned, exited 0, and pack then died on its own
	// build. See lintProject: the root check no longer short-circuits the build.
	if !r.HasErrors() {
		t.Errorf("a missing root must be a blocking error, not just a warning; got %+v", r.Diagnostics)
	}
}

// TestLintFriendlyTypeErrorSurfaced verifies the GIS-269 friendly error (a
// scalar where an array is expected) reaches the user through `fglpkg lint`
// with a non-zero exit, not a raw json message.
func TestLintFriendlyTypeErrorSurfaced(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "typeerr",
  "version": "1.0.0",
  "docs": "README.md"
}`,
	})
	out, err := captureStdout(t, func() error { return cmdLint(nil) })
	if err == nil {
		t.Fatal("cmdLint should return a non-nil error for an invalid manifest")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Errorf("expected ExitError{Code:1}, got %v", err)
	}
	if !strings.Contains(out, "docs") || strings.Contains(out, "cannot unmarshal") {
		t.Errorf("expected a friendly field-named docs error, got:\n%s", out)
	}
}

// TestPackRefusesEmptyPackage confirms the lint gate wired into pack blocks a
// manifest that would stage no modules, rather than silently writing an empty
// zip.
func TestPackRefusesEmptyPackage(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "empty",
  "version": "1.0.0",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"README.md": "# empty\n",
	})
	_, err := captureStdout(t, func() error { return cmdPack([]string{"--list"}) })
	if err == nil {
		t.Fatal("cmdPack should refuse a package that would contain no modules")
	}
	if !strings.Contains(err.Error(), "no BDL modules") {
		t.Errorf("pack error should explain the no-modules problem, got: %v", err)
	}
}

func TestLintRejectsArguments(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{"name":"x","version":"1.0.0"}`,
	})
	if err := cmdLint([]string{"extra"}); err == nil {
		t.Error("cmdLint should reject positional arguments")
	}
}
