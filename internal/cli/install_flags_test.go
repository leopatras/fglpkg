package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestInstallImpliesNewProject covers the SUPNA-10506 Bug 1 decision:
// `fglpkg install <pkg>` in an empty (non-project) directory should be
// treated as local, because the add-package branch is about to write
// fglpkg.json there.
func TestInstallImpliesNewProject(t *testing.T) {
	cases := []struct {
		name string
		f    installFlags
		isPj bool
		want bool
	}{
		{"empty dir, add package", installFlags{pkgs: []string{"foo"}}, false, true},
		{"in project, add package", installFlags{pkgs: []string{"foo"}}, true, false},
		{"empty dir, no packages", installFlags{}, false, false},
		{"--local forced", installFlags{pkgs: []string{"foo"}, local: true}, false, false},
		{"--global forced", installFlags{pkgs: []string{"foo"}, global: true}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := installImpliesNewProject(c.f, c.isPj); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseInstallFlagsDefaults(t *testing.T) {
	f, err := parseInstallFlags([]string{"pkg1", "pkg2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.scope != manifest.ScopeProd {
		t.Errorf("default scope: got %q, want %q", f.scope, manifest.ScopeProd)
	}
	if f.production {
		t.Error("default should not be production")
	}
	if len(f.pkgs) != 2 || f.pkgs[0] != "pkg1" || f.pkgs[1] != "pkg2" {
		t.Errorf("pkgs: %v", f.pkgs)
	}
}

func TestParseInstallFlagsSaveDev(t *testing.T) {
	for _, a := range []string{"--save-dev", "-D"} {
		f, err := parseInstallFlags([]string{a, "tester"})
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if f.scope != manifest.ScopeDev {
			t.Errorf("%s: scope got %q", a, f.scope)
		}
	}
}

func TestParseInstallFlagsSaveOptional(t *testing.T) {
	for _, a := range []string{"--save-optional", "-O"} {
		f, err := parseInstallFlags([]string{a, "telemetry"})
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if f.scope != manifest.ScopeOptional {
			t.Errorf("%s: scope got %q", a, f.scope)
		}
	}
}

func TestParseInstallFlagsProduction(t *testing.T) {
	for _, a := range []string{"--production", "--prod"} {
		f, err := parseInstallFlags([]string{a})
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if !f.production {
			t.Errorf("%s: production not set", a)
		}
	}
}

func TestParseInstallFlagsNoManifestFallback(t *testing.T) {
	f, err := parseInstallFlags([]string{"--no-manifest-fallback"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.noManifestFallback {
		t.Error("--no-manifest-fallback not set")
	}
	// Default is off (fallback enabled).
	def, _ := parseInstallFlags([]string{"pkg"})
	if def.noManifestFallback {
		t.Error("noManifestFallback should default to false")
	}
}

func TestParseInstallFlagsConflicting(t *testing.T) {
	cases := [][]string{
		{"--save-dev", "--save-optional", "x"},
		{"--production", "--save-dev", "x"},
		{"--production", "--save-optional", "x"},
	}
	for _, args := range cases {
		if _, err := parseInstallFlags(args); err == nil {
			t.Errorf("args %v: expected error, got nil", args)
		} else if !strings.Contains(err.Error(), "mutually exclusive") && !strings.Contains(err.Error(), "cannot be combined") {
			t.Errorf("args %v: error message unexpected: %v", args, err)
		}
	}
}

// Local/global/force flags continue to parse the same way through the install
// parser, so existing callers keep working.
func TestParseInstallFlagsKeepsLocalGlobalForce(t *testing.T) {
	f, err := parseInstallFlags([]string{"--local", "--force", "pkg"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.local || !f.force {
		t.Errorf("local=%v force=%v", f.local, f.force)
	}
	if len(f.pkgs) != 1 || f.pkgs[0] != "pkg" {
		t.Errorf("pkgs: %v", f.pkgs)
	}
}
