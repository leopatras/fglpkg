package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
)

func TestParseSearchArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantTerm   string
		wantAll    bool
		wantGenero string
		wantErr    string
	}{
		{"keyword", []string{"foo"}, "foo", false, "", ""},
		{"all", []string{"--all"}, "", true, "", ""},
		{"no args errors", nil, "", false, "", "usage:"},
		{"all + term conflict", []string{"--all", "foo"}, "", false, "", "mutually exclusive"},
		{"two terms errors", []string{"foo", "bar"}, "", false, "", "extra argument"},
		{"genero flag separate value", []string{"--genero", "4.01", "foo"}, "foo", false, "4.01", ""},
		{"genero flag equals form", []string{"--genero=4.01", "foo"}, "foo", false, "4.01", ""},
		{"genero with all", []string{"--all", "--genero", "3.20"}, "", true, "3.20", ""},
		{"genero missing value", []string{"--genero"}, "", false, "", "requires a version"},
		{"genero empty equals form", []string{"--genero="}, "", false, "", "requires a version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, all, genero, err := parseSearchArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if term != c.wantTerm || all != c.wantAll || genero != c.wantGenero {
				t.Errorf("term=%q all=%v genero=%q, want term=%q all=%v genero=%q",
					term, all, genero, c.wantTerm, c.wantAll, c.wantGenero)
			}
		})
	}
}

func TestSearchDeprecatedStatus(t *testing.T) {
	cases := []struct {
		name       string
		deprecated bool
		movedTo    string
		want       string
	}{
		{"live", false, "", ""},
		{"live ignores stray movedTo", false, "chart-3d-ng", ""},
		{"deprecated no successor", true, "", "deprecated"},
		{"deprecated with successor", true, "chart-3d-ng", "deprecated -> chart-3d-ng"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := searchDeprecatedStatus(c.deprecated, c.movedTo); got != c.want {
				t.Errorf("searchDeprecatedStatus(%v, %q) = %q, want %q",
					c.deprecated, c.movedTo, got, c.want)
			}
		})
	}
}

func TestGradeCompat(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	cases := []struct {
		name       string
		target     *genero.Version
		constraint string
		want       string
	}{
		{"no target version", nil, "^4.0.0", "?"},
		{"empty constraint", &v4, "", "?"},
		{"compatible", &v4, "^4.0.0", "✓"},
		{"incompatible", &v4, "^3.0.0", "✗"},
		{"star constraint is compatible", &v4, "*", "✓"},
		{"unparseable constraint", &v4, "not-a-constraint", "?"},
		{"no target and empty constraint", nil, "", "?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gradeCompat(c.target, c.constraint); got != c.want {
				t.Errorf("gradeCompat(%v, %q) = %q, want %q", c.target, c.constraint, got, c.want)
			}
		})
	}
}

// TestCmdSearchStatusColumnConditional confirms the STATUS column is omitted
// entirely when no match is deprecated (byte-for-byte the original layout) and
// appears only once at least one match carries a deprecation.
func TestCmdSearchStatusColumnConditional(t *testing.T) {
	cases := []struct {
		name       string
		packages   []map[string]any
		wantStatus bool   // STATUS header expected in output
		wantSubstr string // value that must appear when deprecated
	}{
		{
			name: "all live omits STATUS column",
			packages: []map[string]any{
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
				{"slug": "chart-pro", "latest_version": "2.0.0", "description": "pro charts"},
			},
			wantStatus: false,
		},
		{
			name: "deprecated match shows STATUS column",
			packages: []map[string]any{
				{"slug": "chart-3d", "latest_version": "1.2.3", "description": "3D charts",
					"deprecated": true, "moved_to": "chart-3d-ng"},
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
			},
			wantStatus: true,
			wantSubstr: "deprecated -> chart-3d-ng",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"packages": c.packages})
			}))
			defer ts.Close()
			t.Setenv("FGLPKG_REGISTRY", ts.URL)
			// Isolate the fglpkg home so a developer's real config doesn't add a
			// second provider and divert us onto the multi-provider path.
			t.Setenv("FGLPKG_HOME", t.TempDir())

			out, err := captureStdout(t, func() error { return cmdSearch([]string{"--all"}) })
			if err != nil {
				t.Fatalf("cmdSearch: %v", err)
			}
			if got := strings.Contains(out, "STATUS"); got != c.wantStatus {
				t.Errorf("STATUS column present = %v, want %v\noutput:\n%s", got, c.wantStatus, out)
			}
			if c.wantStatus {
				if !strings.Contains(out, c.wantSubstr) {
					t.Errorf("output missing %q\noutput:\n%s", c.wantSubstr, out)
				}
			} else if strings.Contains(out, "deprecated") {
				t.Errorf("live-only output unexpectedly mentions deprecation\noutput:\n%s", out)
			}
		})
	}
}

// browsePackagesServer returns an httptest server that answers the browse
// endpoint (`GET /registry/packages?q=…`) with the given listed packages,
// wrapped in the {"packages":[…],"total":N} envelope registry.Search expects.
func browsePackagesServer(t *testing.T, packages []map[string]any) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"packages": packages,
			"total":    len(packages),
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// lineContaining returns the first line of out that contains sub, or "".
func lineContaining(out, sub string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

// TestCmdSearchRendersAnnotatedTable is the golden rendering check: a mixed
// result set (compatible / incompatible / no-constraint) graded against an
// explicit --genero version must show the version in the header, a GENERO
// constraint column, and the ✓/✗/? verdict column.
func TestCmdSearchRendersAnnotatedTable(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "jsonutils", "name": "jsonutils", "description": "JSON helpers",
			"latest_version": "2.1.0", "owner": map[string]any{"name": "ACME"}, "genero": "^4.0.0"},
		{"slug": "legacyjson", "name": "legacyjson", "description": "JSON for Genero 3",
			"latest_version": "1.4.0", "owner": map[string]any{"name": "ACME"}, "genero": "^3.0.0"},
		{"slug": "mystery", "name": "mystery", "description": "no constraint reported",
			"latest_version": "0.9.0", "owner": map[string]any{"name": "ACME"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate fglpkg home so a real config's Artifactory repo doesn't divert
	// the search to the multi-provider (ungraded) path.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	// A bare major.minor override (no patch) must be accepted and shown
	// verbatim in the header — grading does not require a patch level.
	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--genero", "4.01", "json"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	if want := `Results for "json" (Genero 4.01):`; !strings.Contains(out, want) {
		t.Errorf("header missing %q\n--- output ---\n%s", want, out)
	}
	header := lineContaining(out, "NAME")
	for _, col := range []string{"VERSION", "GENERO", "?", "DESCRIPTION"} {
		if !strings.Contains(header, col) {
			t.Errorf("column header %q missing from %q", col, header)
		}
	}

	cases := []struct {
		pkg, constraint, marker string
	}{
		{"jsonutils", "^4.0.0", "✓"},
		{"legacyjson", "^3.0.0", "✗"},
		{"mystery", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n--- output ---\n%s", c.pkg, out)
			continue
		}
		if !strings.Contains(line, c.constraint) {
			t.Errorf("%s row = %q, want GENERO %q", c.pkg, line, c.constraint)
		}
		if !strings.Contains(line, c.marker) {
			t.Errorf("%s row = %q, want marker %q", c.pkg, line, c.marker)
		}
	}
}

// TestCmdSearchUnknownVersionFallback covers the no-target path: when no Genero
// version can be resolved, search still runs, the header explains how to set
// one, and every result is graded "?" even when the registry reports a real
// constraint.
func TestCmdSearchUnknownVersionFallback(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "jsonutils", "name": "jsonutils", "description": "JSON helpers",
			"latest_version": "2.1.0", "owner": map[string]any{"name": "ACME"}, "genero": "^4.0.0"},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())
	// Force genero.Detect() to fail deterministically: no override, no $FGLDIR,
	// and an empty PATH so fglcomp cannot be discovered on a Genero dev machine.
	t.Setenv("FGLPKG_GENERO_VERSION", "")
	t.Setenv("FGLDIR", "")
	t.Setenv("PATH", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"json"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	if want := "Genero version unknown"; !strings.Contains(out, want) {
		t.Errorf("fallback header missing %q\n--- output ---\n%s", want, out)
	}
	// A real constraint is reported, but with no target it must still grade "?".
	line := lineContaining(out, "jsonutils")
	if !strings.Contains(line, "?") {
		t.Errorf("jsonutils row = %q, want marker %q", line, "?")
	}
	if strings.Contains(line, "✓") || strings.Contains(line, "✗") {
		t.Errorf("jsonutils row = %q, want no ✓/✗ verdict with unknown version", line)
	}
}

// TestCmdSearchAllSurfacesCleanErrorOn400 simulates an old registry that
// still rejects empty `q` and confirms the client surfaces the upgrade
// hint rather than leaking the raw HTTP 400.
func TestCmdSearchAllSurfacesCleanErrorOn400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			http.Error(w, "missing query parameter q", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate the fglpkg home so a developer's real ~/.fglpkg/config.json
	// (e.g. a globally-configured Artifactory repo) doesn't add a second
	// provider that satisfies the search and masks the GI 400 under test.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	err := cmdSearch([]string{"--all"})
	if err == nil {
		t.Fatal("expected error from old server's 400, got nil")
	}
	if !strings.Contains(err.Error(), "doesn't support --all") {
		t.Errorf("err = %v, want one explaining --all unsupported", err)
	}
}
