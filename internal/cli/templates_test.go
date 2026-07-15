package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func TestParseInitFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{"none", nil, "", false},
		{"long", []string{"--template", "library"}, "library", false},
		{"short", []string{"-t", "app"}, "app", false},
		{"equals", []string{"--template=library"}, "library", false},
		{"missing value", []string{"--template"}, "", true},
		{"unexpected arg", []string{"bogus"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseInitFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %v", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseInitFlags(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFindTemplate(t *testing.T) {
	if findTemplate("library") == nil {
		t.Error("library template not found")
	}
	if findTemplate("app") == nil {
		t.Error("app template not found")
	}
	if findTemplate("nope") != nil {
		t.Error("unknown template should return nil")
	}
}

// TestLibraryTemplateApply checks the library template sets the publish-only
// manifest fields and adds no dependencies.
func TestLibraryTemplateApply(t *testing.T) {
	m := manifest.New("mylib", "0.1.0", "", "")
	findTemplate("library").apply(m)

	if m.Root != "." {
		t.Errorf("Root = %q, want .", m.Root)
	}
	if m.GeneroConstraint != "*" {
		t.Errorf("GeneroConstraint = %q, want *", m.GeneroConstraint)
	}
	if len(m.Docs) != 1 || m.Docs[0] != "README.md" {
		t.Errorf("Docs = %v, want [README.md]", m.Docs)
	}
	if len(m.Dependencies.FGL) != 0 || len(m.Dependencies.Java) != 0 {
		t.Errorf("template must not add dependencies, got %+v", m.Dependencies)
	}
}

// TestAppTemplateApply checks the app template leaves publish-only fields
// unset and adds no dependencies.
func TestAppTemplateApply(t *testing.T) {
	m := manifest.New("myapp", "0.1.0", "", "")
	findTemplate("app").apply(m)

	if m.Root != "" {
		t.Errorf("Root = %q, want empty (app is not published)", m.Root)
	}
	if m.GeneroConstraint != "" {
		t.Errorf("GeneroConstraint = %q, want empty", m.GeneroConstraint)
	}
	if len(m.Dependencies.FGL) != 0 || len(m.Dependencies.Java) != 0 {
		t.Errorf("template must not add dependencies, got %+v", m.Dependencies)
	}
}

// TestWriteFilesScaffolds verifies the files are created with {{NAME}}
// substituted, and that existing files are not overwritten.
func TestWriteFilesScaffolds(t *testing.T) {
	dir := t.TempDir()

	// Pre-create README.md so we can assert it is left untouched.
	existing := "DO NOT OVERWRITE"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(existing), 0644); err != nil {
		t.Fatalf("seed README: %v", err)
	}

	tmpl := findTemplate("library")
	if err := tmpl.writeFiles(dir, "qrcode"); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}

	// README.md must be preserved.
	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if string(got) != existing {
		t.Errorf("README.md was overwritten: %q", string(got))
	}

	// Lib.4gl should be created with the name substituted.
	src, err := os.ReadFile(filepath.Join(dir, "Lib.4gl"))
	if err != nil {
		t.Fatalf("read Lib.4gl: %v", err)
	}
	if !strings.Contains(string(src), "qrcode") {
		t.Errorf("Lib.4gl missing substituted name; got:\n%s", src)
	}
	if strings.Contains(string(src), "{{NAME}}") {
		t.Errorf("Lib.4gl still contains unsubstituted {{NAME}}")
	}

	// .gitignore should be created.
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Errorf(".gitignore not created: %v", err)
	}
}

func TestParsePublishFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantDry bool
		wantCI  bool
		wantErr bool
	}{
		{"none", nil, false, false, false},
		{"dry-run", []string{"--dry-run"}, true, false, false},
		{"dry-run short", []string{"-n"}, true, false, false},
		{"ci", []string{"--ci"}, false, true, false},
		{"both", []string{"--dry-run", "--ci"}, true, true, false},
		{"unknown", []string{"--nope"}, false, false, true},
		{"changelog value", []string{"--changelog", "notes"}, false, false, false},
		{"changelog eq", []string{"--changelog=notes"}, false, false, false},
		{"changelog missing value", []string{"--changelog"}, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, err := parsePublishFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %v", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePublishFlags(%v): %v", tc.args, err)
			}
			if pf.dryRun != tc.wantDry || pf.ci != tc.wantCI {
				t.Errorf("got dry=%v ci=%v, want dry=%v ci=%v", pf.dryRun, pf.ci, tc.wantDry, tc.wantCI)
			}
		})
	}
}

func TestParsePublishFlagsChangelogValues(t *testing.T) {
	pf, err := parsePublishFlags([]string{"--changelog", "hello world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf.changelog != "hello world" {
		t.Errorf("got text=%q, want %q", pf.changelog, "hello world")
	}

	pf, err = parsePublishFlags([]string{"--changelog=inline notes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf.changelog != "inline notes" {
		t.Errorf("got text=%q, want %q", pf.changelog, "inline notes")
	}
}

func TestParsePublishFlagsRegistryAndForce(t *testing.T) {
	pf, err := parsePublishFlags([]string{"--registry", "acme", "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf.registry != "acme" || !pf.force {
		t.Fatalf("parsed = %+v", pf)
	}
}
