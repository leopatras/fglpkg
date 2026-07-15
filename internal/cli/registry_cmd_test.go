package cli

import (
	"os"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
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
	if f.typ != "artifactory" || f.repoKey != "GeneroBDL" || f.auth != "bearer" || f.priority != 5 {
		t.Fatalf("flags = %+v", f)
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

func TestCmdRegistryAdd_DuplicatePriorityRejected(t *testing.T) {
	chdirTemp(t)
	// Priority 1 collides with the built-in gi → validation error, nothing written.
	if err := cmdRegistryAdd([]string{"acme", "https://a", "--repo-key", "K", "--priority", "1"}); err == nil {
		t.Fatal("expected priority-collision error")
	}
}

func TestCmdRegistryAdd_ArtifactoryRequiresRepoKey(t *testing.T) {
	chdirTemp(t)
	if err := cmdRegistryAdd([]string{"acme", "https://a"}); err == nil {
		t.Fatal("expected error: artifactory type requires --repo-key")
	}
}
