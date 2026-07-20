package cli

import (
	"os"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// chdirTemp points FGLPKG_HOME and the working directory at fresh temp dirs for
// the duration of the test, so `registry add/remove` writes are isolated.
func chdirTemp(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	work := t.TempDir()
	t.Setenv("FGLPKG_HOME", home)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return home
}

func TestParseRegistryAddFlags(t *testing.T) {
	f, err := parseRegistryAddFlags([]string{
		"acme", "https://a.example",
		"--type", "artifactory", "--repo-key=GeneroBDL",
		"--auth", "bearer", "--priority", "5", "--packages", "acme-*, foo-*",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.name != "acme" || f.url != "https://a.example" {
		t.Fatalf("name/url = %q %q", f.name, f.url)
	}
	if f.typ != "artifactory" || f.repoKey != "GeneroBDL" || f.auth != "bearer" {
		t.Fatalf("flags = %+v", f)
	}
	if f.priority == nil || *f.priority != 5 {
		t.Fatalf("priority = %v, want 5", f.priority)
	}

	// An omitted --priority stays nil (unset) so the add path can auto-assign;
	// an explicit --priority 0 is captured as 0 (not nil) so validation rejects
	// it rather than silently rewriting it. (GIS-249)
	if f, err := parseRegistryAddFlags([]string{"n", "u"}); err != nil || f.priority != nil {
		t.Fatalf("omitted priority should be nil: %v (err %v)", f.priority, err)
	}
	if f, err := parseRegistryAddFlags([]string{"n", "u", "--priority", "0"}); err != nil || f.priority == nil || *f.priority != 0 {
		t.Fatalf("explicit --priority 0 should be captured as 0: %v (err %v)", f.priority, err)
	}
	if len(f.packages) != 2 || f.packages[0] != "acme-*" || f.packages[1] != "foo-*" {
		t.Fatalf("packages = %v", f.packages)
	}

	if _, err := parseRegistryAddFlags([]string{"only-name"}); err == nil {
		t.Error("expected error for missing url")
	}
	if _, err := parseRegistryAddFlags([]string{"n", "u", "--priority", "x"}); err == nil {
		t.Error("expected error for non-integer priority")
	}
}

func TestCmdRegistryAddRemove_Global(t *testing.T) {
	home := chdirTemp(t)

	// Add an artifactory registry with no explicit priority → auto-assigned
	// after the built-in gi (priority 1).
	if err := cmdRegistryAdd([]string{"acme", "https://a.example", "--repo-key", "GeneroBDL"}); err != nil {
		t.Fatalf("registry add: %v", err)
	}
	g, err := config.LoadGlobalFile(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r, ok := config.Find(g.Registries, "acme")
	if !ok {
		t.Fatalf("acme not persisted: %+v", g.Registries)
	}
	if r.Priority != 2 || r.Type != config.TypeArtifactory || r.RepoKey != "GeneroBDL" {
		t.Fatalf("persisted registry = %+v", r)
	}

	// Adding the same name again is refused.
	if err := cmdRegistryAdd([]string{"acme", "https://a.example", "--repo-key", "K"}); err == nil {
		t.Error("expected duplicate-name error")
	}

	// Redefining the built-in gi is refused.
	if err := cmdRegistryAdd([]string{"gi", "https://x"}); err == nil {
		t.Error("expected error redefining gi")
	}

	// Remove it.
	if err := cmdRegistryRemove([]string{"acme"}); err != nil {
		t.Fatalf("registry remove: %v", err)
	}
	g, _ = config.LoadGlobalFile(home)
	if _, ok := config.Find(g.Registries, "acme"); ok {
		t.Fatalf("acme still present after remove: %+v", g.Registries)
	}

	// Removing a non-existent / built-in registry errors.
	if err := cmdRegistryRemove([]string{"acme"}); err == nil {
		t.Error("expected error removing absent registry")
	}
	if err := cmdRegistryRemove([]string{"gi"}); err == nil {
		t.Error("expected error removing built-in gi")
	}
}

// TestCmdRegistryRemove_ProjectClearsDanglingDefault is the regression test for
// GIS-249 C3: removing the project registry that is fglpkg.json's
// defaultRegistry must also clear the default, so a later bare `publish` does
// not resolve a now-removed name. Mirrors the global branch's behaviour.
func TestCmdRegistryRemove_ProjectClearsDanglingDefault(t *testing.T) {
	chdirTemp(t)

	m := manifest.New("app", "1.0.0", "", "")
	m.Registries = []config.Registry{{
		Name: "acme", Type: config.TypeArtifactory, URL: "https://a.example",
		RepoKey: "GeneroBDL", Priority: 2,
	}}
	m.DefaultRegistry = "acme"
	if err := m.Save("."); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := cmdRegistryRemove([]string{"acme", "--project"}); err != nil {
		t.Fatalf("registry remove --project: %v", err)
	}

	got, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if _, ok := config.Find(got.Registries, "acme"); ok {
		t.Fatalf("acme still present after remove: %+v", got.Registries)
	}
	if got.DefaultRegistry != "" {
		t.Fatalf("defaultRegistry should be cleared, got %q", got.DefaultRegistry)
	}
}

// TestCmdRegistryRemove_ProjectKeepsUnrelatedDefault confirms removing a
// non-default registry leaves defaultRegistry untouched.
func TestCmdRegistryRemove_ProjectKeepsUnrelatedDefault(t *testing.T) {
	chdirTemp(t)

	m := manifest.New("app", "1.0.0", "", "")
	m.Registries = []config.Registry{
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a.example", RepoKey: "K", Priority: 2},
		{Name: "corp", Type: config.TypeArtifactory, URL: "https://c.example", RepoKey: "K", Priority: 3},
	}
	m.DefaultRegistry = "corp"
	if err := m.Save("."); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := cmdRegistryRemove([]string{"acme", "--project"}); err != nil {
		t.Fatalf("registry remove --project: %v", err)
	}

	got, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if got.DefaultRegistry != "corp" {
		t.Fatalf("unrelated defaultRegistry should be kept, got %q", got.DefaultRegistry)
	}
}

func TestCmdRegistryAdd_DuplicatePriorityRejected(t *testing.T) {
	chdirTemp(t)
	// Priority 1 collides with the built-in gi → validation error, nothing written.
	if err := cmdRegistryAdd([]string{"acme", "https://a", "--repo-key", "K", "--priority", "1"}); err == nil {
		t.Fatal("expected priority-collision error")
	}
}

// TestCmdRegistryAdd_ExplicitPriorityZeroRejected is the GIS-249 minor: an
// explicit --priority 0 must be validated (positive required) rather than
// silently auto-reassigned like an omitted flag.
func TestCmdRegistryAdd_ExplicitPriorityZeroRejected(t *testing.T) {
	chdirTemp(t)
	if err := cmdRegistryAdd([]string{"acme", "https://a", "--repo-key", "K", "--priority", "0"}); err == nil {
		t.Fatal("expected explicit --priority 0 to be rejected")
	}
}

func TestCmdRegistryAdd_ArtifactoryRequiresRepoKey(t *testing.T) {
	chdirTemp(t)
	if err := cmdRegistryAdd([]string{"acme", "https://a"}); err == nil {
		t.Fatal("expected error: artifactory type requires --repo-key")
	}
}
