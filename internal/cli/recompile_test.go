package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestResolveSourceMatching drives the tiered source resolver directly with a
// synthetic index (no filesystem), covering the layouts the recompile check
// must handle.
func TestResolveSourceMatching(t *testing.T) {
	cases := []struct {
		name    string
		binPath string
		index   map[string][]string
		want    []string
		wantOK  bool
	}{
		{
			name:    "compile in place (exact sibling)",
			binPath: "com/fourjs/poiapi/Module.42m",
			index: map[string][]string{
				"Module.4gl": {"com/fourjs/poiapi/Module.4gl"},
			},
			want:   []string{"com/fourjs/poiapi/Module.4gl"},
			wantOK: true,
		},
		{
			name:    "sibling preferred over a same-basename source elsewhere",
			binPath: "lib/Module.42m",
			index: map[string][]string{
				"Module.4gl": {"lib/Module.4gl", "src/Module.4gl"},
			},
			want:   []string{"lib/Module.4gl"},
			wantOK: true,
		},
		{
			name:    "split src/ to lib/ via path-suffix match",
			binPath: "lib/com/fourjs/poiapi/Module.42m",
			index: map[string][]string{
				"Module.4gl": {"src/com/fourjs/poiapi/Module.4gl", "src/other/Module.4gl"},
			},
			want:   []string{"src/com/fourjs/poiapi/Module.4gl"},
			wantOK: true,
		},
		{
			name:    "flattened build dir, lone basename match",
			binPath: "Module.42m",
			index: map[string][]string{
				"Module.4gl": {"src/deep/Module.4gl"},
			},
			want:   []string{"src/deep/Module.4gl"},
			wantOK: true,
		},
		{
			name:    "ambiguous: equal suffix score returns all tied candidates",
			binPath: "bin/utils.42m",
			index: map[string][]string{
				"utils.4gl": {"a/utils.4gl", "b/utils.4gl"},
			},
			want:   []string{"a/utils.4gl", "b/utils.4gl"},
			wantOK: true,
		},
		{
			name:    "no source with matching basename",
			binPath: "lib/Module.42m",
			index:   map[string][]string{"Other.4gl": {"src/Other.4gl"}},
			want:    nil,
			wantOK:  false,
		},
		{
			name:    "unknown binary extension",
			binPath: "docs/README.md",
			index:   map[string][]string{"README.4gl": {"README.4gl"}},
			want:    nil,
			wantOK:  false,
		},
		{
			name:    "per to 42f form mapping",
			binPath: "forms/cust.42f",
			index:   map[string][]string{"cust.per": {"forms/cust.per"}},
			want:    []string{"forms/cust.per"},
			wantOK:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveSource(tc.binPath, tc.index)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			// Order within a tie is not meaningful; compare as sets.
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("sources = %v, want %v", got, want)
			}
		})
	}
}

func TestCommonSuffixSegments(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"src/com/foo", "lib/com/foo", 2},
		{"src", ".", 0},
		{".", ".", 0},
		{"a/b/c", "a/b/c", 3},
		{"x/com/foo", "y/bar/foo", 1},
		{"", "lib", 0},
	}
	for _, tc := range cases {
		if got := commonSuffixSegments(tc.a, tc.b); got != tc.want {
			t.Errorf("commonSuffixSegments(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestBuildSourceIndexDeepAndIgnored confirms the index finds sources at any
// depth, skips the .fglpkg artifact dir, and honors .fglpkgignore.
func TestBuildSourceIndexDeepAndIgnored(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "src", "com", "acme", "Mod.4gl"), "x")
	mustWriteFile(t, filepath.Join(dir, "forms", "cust.per"), "x")
	mustWriteFile(t, filepath.Join(dir, "strings", "msgs.str"), "x")
	mustWriteFile(t, filepath.Join(dir, ".fglpkg", "packages", "dep", "Vendored.4gl"), "x")
	mustWriteFile(t, filepath.Join(dir, "ignored", "Skip.4gl"), "x")
	mustWriteFile(t, filepath.Join(dir, ".fglpkgignore"), "ignored/\n")

	withWorkdir(t, dir)

	ignore, err := loadIgnore(".")
	if err != nil {
		t.Fatalf("loadIgnore: %v", err)
	}
	index := buildSourceIndex(ignore)

	if got := index["Mod.4gl"]; !reflect.DeepEqual(got, []string{"src/com/acme/Mod.4gl"}) {
		t.Errorf("Mod.4gl = %v, want [src/com/acme/Mod.4gl]", got)
	}
	if _, ok := index["cust.per"]; !ok {
		t.Errorf("cust.per not indexed")
	}
	if _, ok := index["msgs.str"]; !ok {
		t.Errorf("msgs.str not indexed")
	}
	if _, ok := index["Vendored.4gl"]; ok {
		t.Errorf("source under .fglpkg should be skipped")
	}
	if _, ok := index["Skip.4gl"]; ok {
		t.Errorf("source under an ignored dir should be skipped")
	}
}

// TestCheckForRecompileDetectsStaleAcrossDirs is the regression for the bug:
// a source in a *separate* directory from its binary, made newer than the
// binary, must be resolved and reported — the old sibling-only lookup missed it.
func TestCheckForRecompileDetectsStaleAcrossDirs(t *testing.T) {
	dir := t.TempDir()
	binRel := filepath.Join("lib", "com", "acme", "Mod.42m")
	srcRel := filepath.Join("src", "com", "acme", "Mod.4gl")
	mustWriteFile(t, filepath.Join(dir, binRel), "pcode")
	mustWriteFile(t, filepath.Join(dir, srcRel), "source")

	// Binary older than source → stale.
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, binRel), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, srcRel), newer, newer); err != nil {
		t.Fatal(err)
	}

	withWorkdir(t, dir)

	ignore, _ := loadIgnore(".")
	index := buildSourceIndex(ignore)

	got, ok := resolveSource(filepath.ToSlash(binRel), index)
	if !ok {
		t.Fatal("resolveSource did not find the cross-directory source")
	}
	wantSrc := filepath.ToSlash(srcRel)
	if len(got) != 1 || got[0] != wantSrc {
		t.Fatalf("resolved %v, want [%s]", got, wantSrc)
	}

	binInfo, _ := os.Stat(filepath.FromSlash(binRel))
	srcInfo, _ := os.Stat(filepath.FromSlash(got[0]))
	if !srcInfo.ModTime().After(binInfo.ModTime()) {
		t.Errorf("expected source newer than binary (stale), src=%v bin=%v",
			srcInfo.ModTime(), binInfo.ModTime())
	}
}

// TestCheckForRecompileWarnsOnStale is the regression for issue #24 C5: the
// recompile staleness guard must actually fire when a packaged binary is older
// than its source. cmdPublish now runs this guard on BOTH the GI and the
// Artifactory publish paths (previously the Artifactory branch returned before
// reaching it). Driven directly here because cmdPublish additionally calls
// genero.Detect(), which requires a real Genero install.
func TestCheckForRecompileWarnsOnStale(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "fglpkg.json"), `{
  "name": "stale-test",
  "version": "1.0.0",
  "description": "test",
  "author": "me",
  "license": "UNLICENSED",
  "dependencies": { "fgl": {} }
}`)
	mustWriteFile(t, filepath.Join(dir, "Main.42m"), "pcode")
	mustWriteFile(t, filepath.Join(dir, "Main.4gl"), "source")

	// Binary older than source → stale.
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Main.42m"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "Main.4gl"), newer, newer); err != nil {
		t.Fatal(err)
	}

	withWorkdir(t, dir)

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}

	// Feed "y" so the guard's "Continue?" prompt does not os.Exit(1).
	origReader := reader
	reader = bufio.NewReader(strings.NewReader("y\n"))
	t.Cleanup(func() { reader = origReader })

	out, _ := captureDryRun(t, func() error {
		checkForRecompile(m)
		return nil
	})
	if !strings.Contains(out, "may not have been recompiled") {
		t.Errorf("expected stale-recompile warning, got:\n%s", out)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withWorkdir chdirs into dir for the duration of the test, restoring the
// previous working directory on cleanup.
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
