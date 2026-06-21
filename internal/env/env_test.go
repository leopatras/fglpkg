package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateGWAEmitsFlags verifies --gwa output: one --webcomponent flag
// per COMPONENTTYPE directory under .fglpkg/webcomponents/.
func TestGenerateGWAEmitsFlags(t *testing.T) {
	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "3DChart"))
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "Heatmap"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir()) // separate global home, empty
	flags, err := g.GenerateGWA()
	if err != nil {
		t.Fatalf("GenerateGWA: %v", err)
	}
	if len(flags) != 2 {
		t.Fatalf("expected 2 --webcomponent flags, got %d: %v", len(flags), flags)
	}
	for _, f := range flags {
		if !strings.HasPrefix(f, "--webcomponent ") {
			t.Errorf("expected --webcomponent prefix, got %q", f)
		}
	}
	joined := strings.Join(flags, "\n")
	if !strings.Contains(joined, "3DChart") || !strings.Contains(joined, "Heatmap") {
		t.Errorf("expected both component names in output: %s", joined)
	}
}

// TestGenerateLocalIncludesFGLIMAGEPATH verifies that the local-scope env
// output prepends the project's .fglpkg/ directory onto FGLIMAGEPATH when
// at least one webcomponent is installed.
func TestGenerateLocalIncludesFGLIMAGEPATH(t *testing.T) {
	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "MyWidget"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "FGLIMAGEPATH=") {
		t.Errorf("expected FGLIMAGEPATH export in:\n%s", joined)
	}
	if !strings.Contains(joined, "WEB_COMPONENT_DIRECTORY") {
		t.Errorf("expected GAS hint comment in:\n%s", joined)
	}
}

// TestGenerateLocalSkipsFGLIMAGEPATHWhenEmpty verifies that no FGLIMAGEPATH
// line is emitted when there are no webcomponents installed.
func TestGenerateLocalSkipsFGLIMAGEPATHWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	// No webcomponents dir at all.

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	for _, line := range exports {
		if strings.Contains(line, "FGLIMAGEPATH") {
			t.Errorf("unexpected FGLIMAGEPATH line when no webcomponents installed: %q", line)
		}
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}
