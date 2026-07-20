package cli

import (
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestFilesPatternMatch covers the GIS-275 `files` matching rules: bare
// patterns match the basename at any depth (unchanged); patterns containing
// "/" are path-scoped relative to root with "**"/single-segment "*".
func TestFilesPatternMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		rel     string // file path relative to root
		want    bool
	}{
		// Bare patterns — basename at any depth (historical behaviour).
		{"bare top-level", "*.42m", "ModuleA.42m", true},
		{"bare nested", "*.42m", "tests/ModuleA.42m", true},
		{"bare wrong ext", "*.42m", "ModuleA.4gl", false},
		// Path-scoped — anchored, relative to root, "*" is single-segment.
		{"scoped in dir", "tests/*.4gl", "tests/foo.4gl", true},
		{"scoped not at root", "tests/*.4gl", "foo.4gl", false},
		{"scoped other dir", "tests/*.4gl", "lib/foo.4gl", false},
		{"scoped star no cross dir", "tests/*.4gl", "tests/sub/foo.4gl", false},
		{"leading slash == anchored", "/tests/*.4gl", "tests/foo.4gl", true},
		// Doublestar spans directory levels.
		{"doublestar deep", "com/**/*.42m", "com/fourjs/ai/foo.42m", true},
		{"doublestar direct child", "com/**/*.42m", "com/foo.42m", true},
		{"doublestar wrong prefix", "com/**/*.42m", "org/foo.42m", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relToRoot := filepath.FromSlash(tt.rel)
			base := filepath.Base(relToRoot)
			if got := filesPatternMatch(tt.pattern, base, relToRoot, nil); got != tt.want {
				t.Errorf("filesPatternMatch(%q, base=%q, rel=%q) = %v, want %v",
					tt.pattern, base, relToRoot, got, tt.want)
			}
		})
	}
}

// TestBuildPackageZip_FilesPathScopedPatterns is Sebastien's reported case
// (GIS-275): with root set, "tests/*.4gl" ships only the test .4gl — not the
// library .4gl at root — alongside all compiled modules matched by a bare
// "*.42m" at any depth. No .fglpkgignore needed.
func TestBuildPackageZip_FilesPathScopedPatterns(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "com/fourjs/ai/fgl_ai_sdk",
  "files": ["*.42m", "tests/*.4gl"],
  "dependencies": { "fgl": {} }
}`,
		"com/fourjs/ai/fgl_ai_sdk/ModuleA.42m":       "MAIN END MAIN\n",
		"com/fourjs/ai/fgl_ai_sdk/lib.4gl":           "FUNCTION lib() END FUNCTION\n",
		"com/fourjs/ai/fgl_ai_sdk/tests/spec.4gl":    "FUNCTION t() END FUNCTION\n",
		"com/fourjs/ai/fgl_ai_sdk/tests/ModuleT.42m": "MAIN END MAIN\n",
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

	// Bare "*.42m" ships compiled modules at any depth.
	for _, want := range []string{
		"com/fourjs/ai/fgl_ai_sdk/ModuleA.42m",
		"com/fourjs/ai/fgl_ai_sdk/tests/ModuleT.42m",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q in archive; got %v", want, keys(boolKeys(got)))
		}
	}
	// Path-scoped "tests/*.4gl" ships the test source...
	if _, ok := got["com/fourjs/ai/fgl_ai_sdk/tests/spec.4gl"]; !ok {
		t.Errorf("expected tests/spec.4gl in archive; got %v", keys(boolKeys(got)))
	}
	// ...but NOT the library .4gl at root (it is not under tests/).
	if _, ok := got["com/fourjs/ai/fgl_ai_sdk/lib.4gl"]; ok {
		t.Error("library lib.4gl must not ship — only tests/*.4gl was requested")
	}
}

// TestBuildPackageZip_FilesSlashPatternWasDeadBefore documents the
// backward-compat guarantee: a "/"-pattern matched nothing under the old
// basename-only rule, so this only revives dead patterns.
func TestBuildPackageZip_FilesSlashPatternWasDeadBefore(t *testing.T) {
	// A manifest whose ONLY selector is a bare pattern must be unaffected: the
	// library .4gl is excluded because only *.42m is requested.
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"ModuleA.42m": "MAIN END MAIN\n",
		"helper.4gl":  "FUNCTION f() END FUNCTION\n",
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
	if _, ok := got["ModuleA.42m"]; !ok {
		t.Errorf("expected ModuleA.42m; got %v", keys(boolKeys(got)))
	}
	if _, ok := got["helper.4gl"]; ok {
		t.Error("helper.4gl should not ship under a bare *.42m selector")
	}
}
