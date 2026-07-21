package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// ─── ignoreSet pattern tests ──────────────────────────────────────────────────

func TestIgnoreMatchesBasename(t *testing.T) {
	s := &ignoreSet{rules: []ignoreRule{{pattern: "*.bak"}}}
	if !s.shouldExclude("Main.bak", false) {
		t.Error("expected Main.bak excluded by *.bak")
	}
	if !s.shouldExclude("nested/path/Old.bak", false) {
		t.Error("expected nested .bak excluded — pattern is unanchored")
	}
	if s.shouldExclude("Main.42m", false) {
		t.Error("Main.42m should not match *.bak")
	}
}

func TestIgnoreAnchoredPattern(t *testing.T) {
	s := &ignoreSet{rules: []ignoreRule{{pattern: "build"}}}
	// Without leading slash, "build" matches any path segment named build.
	if !s.shouldExclude("build/output.txt", false) {
		t.Error("build segment should match unanchored pattern")
	}
	if !s.shouldExclude("nested/build/x.txt", false) {
		t.Error("nested build segment should match unanchored pattern")
	}

	rooted := &ignoreSet{rules: []ignoreRule{{pattern: "/build"}}}
	// With leading slash, only the root-level path matches.
	if !rooted.shouldExclude("build", false) {
		t.Error("root build should match anchored pattern")
	}
	if rooted.shouldExclude("nested/build", false) {
		t.Error("nested build should NOT match anchored pattern /build")
	}
}

func TestIgnoreNegationReinstates(t *testing.T) {
	s := &ignoreSet{rules: []ignoreRule{
		{pattern: "*.log"},
		{pattern: "important.log", negate: true},
	}}
	if !s.shouldExclude("foo.log", false) {
		t.Error("foo.log should be excluded by *.log")
	}
	if s.shouldExclude("important.log", false) {
		t.Error("important.log should be re-included by negation rule")
	}
}

func TestIgnoreDirOnlyRule(t *testing.T) {
	s := &ignoreSet{rules: []ignoreRule{{pattern: "cache", dirOnly: true}}}
	if !s.shouldExclude("cache", true) {
		t.Error("cache dir should be excluded by dir-only rule")
	}
	if s.shouldExclude("cache", false) {
		t.Error("a file named cache should NOT match a dir-only rule")
	}
}

// TestDirShouldBeSkipped covers dirShouldBeSkipped in isolation (GIS-297):
// this is the helper that lets a dirOnly (trailing-slash) .fglpkgignore
// rule actually take effect during a filepath.Walk, since shouldExclude
// only honours dirOnly rules when isDir is true.
func TestDirShouldBeSkipped(t *testing.T) {
	s := &ignoreSet{rules: []ignoreRule{{pattern: "sub", dirOnly: true}}}
	if !dirShouldBeSkipped(s, "sub") {
		t.Error("expected sub/ directory to be skipped by the dir-only rule")
	}
	if dirShouldBeSkipped(s, "other") {
		t.Error("unrelated directory should not be skipped")
	}
	if dirShouldBeSkipped(s, ".") {
		t.Error("the walk root itself must never be skipped")
	}
}

func TestIgnoreEmptySetIsNoop(t *testing.T) {
	var s *ignoreSet
	if s.shouldExclude("anything", false) {
		t.Error("nil set must always allow")
	}
	empty := &ignoreSet{}
	if empty.shouldExclude("anything", false) {
		t.Error("empty rule list must always allow")
	}
}

// ─── loadIgnore round-trip ────────────────────────────────────────────────────

func TestLoadIgnoreParsesCommentsBlankAndNegation(t *testing.T) {
	dir := t.TempDir()
	body := `# leading comment
*.bak

# blank above
build/
!build/keep.txt
`
	if err := os.WriteFile(filepath.Join(dir, ".fglpkgignore"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := loadIgnore(dir)
	if err != nil {
		t.Fatalf("loadIgnore: %v", err)
	}
	if len(s.rules) != 3 {
		t.Fatalf("expected 3 rules, got %d: %+v", len(s.rules), s.rules)
	}
	if s.rules[0].pattern != "*.bak" || s.rules[0].negate {
		t.Errorf("rule[0] wrong: %+v", s.rules[0])
	}
	if !s.rules[1].dirOnly {
		t.Errorf("rule[1] should be dir-only: %+v", s.rules[1])
	}
	if !s.rules[2].negate {
		t.Errorf("rule[2] should be negation: %+v", s.rules[2])
	}
}

func TestLoadIgnoreMissingFileIsEmpty(t *testing.T) {
	s, err := loadIgnore(t.TempDir())
	if err != nil {
		t.Fatalf("loadIgnore: %v", err)
	}
	if len(s.rules) != 0 {
		t.Errorf("expected empty set, got %+v", s.rules)
	}
}

// ─── buildPackageZip integration ──────────────────────────────────────────────

func TestBuildPackageZipRespectsFglpkgIgnore(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "ignoretest",
  "version": "1.0.0",
  "dependencies": { "fgl": {} },
  "files": ["*.42m", "*.42f", "*.sch"],
  "docs": ["docs/**/*.md"]
}`)
	write("Keep.42m", "MAIN END MAIN\n")
	write("Drop.42m", "MAIN END MAIN\n")
	write("scratch.42m.bak", "old\n")
	write("docs/keep.md", "# keep\n")
	write("docs/internal.md", "# internal — do not ship\n")

	// Ignore: a specific .42m by name, all .bak files, and any docs file
	// whose name starts with "internal".
	write(".fglpkgignore", "Drop.42m\n*.bak\ndocs/internal.md\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}

	if !got["Keep.42m"] {
		t.Errorf("Keep.42m should be in zip; got %v", got)
	}
	if !got["docs/keep.md"] {
		t.Errorf("docs/keep.md should be in zip; got %v", got)
	}
	if got["Drop.42m"] {
		t.Error("Drop.42m should be excluded by .fglpkgignore")
	}
	if got["docs/internal.md"] {
		t.Error("docs/internal.md should be excluded by .fglpkgignore")
	}
	if got[".fglpkgignore"] {
		t.Error(".fglpkgignore itself should not be shipped (no pattern matches it)")
	}
	if !got["fglpkg.json"] {
		t.Error("fglpkg.json must always be included")
	}
}

// TestBuildPackageZipRespectsDirOnlyIgnorePattern reproduces GIS-297: a
// dirOnly ("trailing slash") .fglpkgignore pattern like "sub/" previously
// had no effect at all in buildPackageZip, because none of the staging
// walks ever called shouldExclude with isDir=true (the only way a
// dirOnly rule can match) — they only ever checked individual files.
func TestBuildPackageZipRespectsDirOnlyIgnorePattern(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "dirignoretest",
  "version": "1.0.0",
  "dependencies": { "fgl": {} },
  "files": ["*.4gl"]
}`)
	write("hello.4gl", "FUNCTION main()\nEND FUNCTION\n")
	write("sub/secret.4gl", "FUNCTION shouldNotBePublished()\nEND FUNCTION\n")
	write(".fglpkgignore", "sub/\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}

	if !got["hello.4gl"] {
		t.Errorf("hello.4gl should be in zip; got %v", got)
	}
	if got["sub/secret.4gl"] {
		t.Error("sub/secret.4gl should be excluded by the dir-only 'sub/' rule in .fglpkgignore")
	}
}

func TestBuildPackageZipBinScriptOverridesIgnore(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "binignoretest",
  "version": "1.0.0",
  "dependencies": { "fgl": {} },
  "bin": { "migrate": "scripts/migrate.sh" }
}`)
	write("scripts/migrate.sh", "#!/bin/sh\n")
	// User accidentally ignored the scripts dir; the bin entry must still ship.
	write(".fglpkgignore", "scripts/\n*.sh\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	found := false
	for _, f := range r.File {
		if f.Name == "scripts/migrate.sh" {
			found = true
		}
	}
	if !found {
		t.Error("declared bin script must always be included even if .fglpkgignore matches its path")
	}
}
