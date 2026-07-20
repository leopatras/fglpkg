package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindInstalledPackageAcceptsNonCanonicalName: a package is installed under
// its canonical slug (under-score-test), so `fglpkg bdl`/`fglpkg docs` must find
// it when the user types any spelling that canonicalizes to that slug —
// under_score_test, Under_Score_Test, under.score.test (GIS-271). Before the
// fix, findInstalledPackage joined the raw name and reported "not installed".
func TestFindInstalledPackageAcceptsNonCanonicalName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FGLPKG_HOME", home)

	slugDir := filepath.Join(home, "packages", "under-score-test")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The installed manifest keeps the original name verbatim as the display name.
	manifestJSON := `{"name":"under_score_test","version":"1.0.0","programs":["Test"]}`
	if err := os.WriteFile(filepath.Join(slugDir, "fglpkg.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"under_score_test", "under-score-test", "Under_Score_Test", "under.score.test"} {
		t.Run(name, func(t *testing.T) {
			dir, m, err := findInstalledPackage(name)
			if err != nil {
				t.Fatalf("findInstalledPackage(%q) failed: %v", name, err)
			}
			if dir != slugDir {
				t.Errorf("dir = %q, want %q", dir, slugDir)
			}
			if m == nil || m.Name != "under_score_test" {
				t.Errorf("manifest = %+v, want display name %q", m, "under_score_test")
			}
		})
	}
}

// TestFindInstalledPackageStillReportsMissing: a name that is genuinely not
// installed must still error (canonicalization must not mask a real miss).
func TestFindInstalledPackageStillReportsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FGLPKG_HOME", home)

	if _, _, err := findInstalledPackage("no_such_pkg"); err == nil {
		t.Fatal("expected an error for a package that is not installed")
	}
}
