package resolver_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestResolveCanonicalizesUnderscoreName: a dependency written with an
// underscore resolves under its canonical (hyphen) slug, and the plan/lock key
// is the canonical form (GIS-271).
func TestResolveCanonicalizesUnderscoreName(t *testing.T) {
	db := packageDB{
		"foo-bar": {"1.0.0": entry("", pkg("foo-bar", "1.0.0", nil))},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency("foo_bar", "^1.0.0") // underscore spelling

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := planByName(plan)
	if _, ok := byName["foo-bar"]; !ok {
		t.Errorf("expected canonical key %q in the plan", "foo-bar")
	}
	if _, ok := byName["foo_bar"]; ok {
		t.Error("plan should not contain the non-canonical key foo_bar")
	}
}

// TestResolveDedupesSpellingsToOnePackage: the root depends on "foo_bar" while a
// sibling depends on "foo-bar". Both canonicalize to the same slug, so the
// package must resolve exactly once — the correctness reason canonicalization
// belongs in the resolver, not just in URL construction (GIS-271).
func TestResolveDedupesSpellingsToOnePackage(t *testing.T) {
	db := packageDB{
		"foo-bar": {"1.0.0": entry("", pkg("foo-bar", "1.0.0", nil))},
		"helper":  {"1.0.0": entry("", pkg("helper", "1.0.0", map[string]string{"foo-bar": "^1.0.0"}))},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency("foo_bar", "^1.0.0") // underscore
	root.AddFGLDependency("helper", "^1.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count := 0
	for _, p := range plan.Packages {
		if p.Name == "foo-bar" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("foo-bar resolved %d time(s), want exactly 1 (dedup across spellings)", count)
	}
	if len(plan.Packages) != 2 {
		t.Errorf("expected 2 packages (foo-bar + helper), got %d", len(plan.Packages))
	}
}
