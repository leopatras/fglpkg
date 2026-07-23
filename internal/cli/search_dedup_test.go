package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// searchStub is a minimal provider.Provider returning canned search results.
type searchStub struct {
	name    string
	results []registry.SearchResult
}

func (s *searchStub) Name() string { return s.name }
func (s *searchStub) FetchVersions(string) ([]resolver.CandidateVersion, error) {
	return nil, provider.ErrNotFound
}
func (s *searchStub) FetchInfo(string, string, string) (*registry.PackageInfo, error) {
	return nil, provider.ErrNotFound
}
func (s *searchStub) Search(string) ([]registry.SearchResult, error) { return s.results, nil }

func TestSearchAcrossProviders_DedupAndCollision(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "logft", LatestVersion: "2.0.0", Description: "logging"},
		{Name: "utils", LatestVersion: "1.3.0", Description: "gi utils"},
	}}
	acme := &searchStub{name: "acme", results: []registry.SearchResult{
		{Name: "utils", LatestVersion: "0.9.0", Description: "acme utils"},
		{Name: "acme-only", LatestVersion: "3.0.0", Description: "internal"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi, acme}, descs, nil)

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "u", false, nil) })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	// "utils" appears once (deduped) with both sources shown, higher-priority
	// (gi) version/description kept.
	utilsRows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "utils ") {
			utilsRows++
			if !strings.Contains(line, "gi, acme") {
				t.Errorf("utils row should list both sources: %q", line)
			}
			if !strings.Contains(line, "1.3.0") || !strings.Contains(line, "gi utils") {
				t.Errorf("utils row should keep gi's version/description: %q", line)
			}
		}
	}
	if utilsRows != 1 {
		t.Errorf("want exactly one deduped utils row, got %d\n%s", utilsRows, out)
	}
	if !strings.Contains(out, "more than one repository") {
		t.Errorf("expected a collision note:\n%s", out)
	}
}

// TestSearchAcrossProviders_GradesAgainstTarget covers the multi-provider
// grading path: results carrying a per-package GeneroConstraint (as the Genero
// provider supplies) must be shown in the GENERO column and graded ✓/✗/? against
// a non-nil target, rather than blanked to "-"/"?" for every row.
func TestSearchAcrossProviders_GradesAgainstTarget(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "modern", LatestVersion: "2.0.0", Description: "for genero 4", GeneroConstraint: "^4.0.0"},
		{Name: "legacy", LatestVersion: "1.0.0", Description: "for genero 3", GeneroConstraint: "^3.0.0"},
		{Name: "anyver", LatestVersion: "1.0.0", Description: "no constraint"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)
	target := genero.MustParse("4.01.12")

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &target) })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	if want := "(Genero 4.01.12)"; !strings.Contains(out, want) {
		t.Errorf("header missing target %q\n%s", want, out)
	}
	cases := []struct{ pkg, constraint, marker string }{
		{"modern", "^4.0.0", "✓"},
		{"legacy", "^3.0.0", "✗"},
		{"anyver", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n%s", c.pkg, out)
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

// TestSearchAcrossProviders_GeneroColumnWidensToFit guards the dynamic GENERO
// column width: a constraint longer than the 12-char floor must not push its
// row's later columns out of alignment with a short-constraint row (the "spill
// into the ? column" regression). Both rows grade ✓, so their verdict glyph is
// the same width; with a correctly widened GENERO column their descriptions
// start at the same byte offset. A fixed %-12s would misalign them.
func TestSearchAcrossProviders_GeneroColumnWidensToFit(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "shortc", LatestVersion: "1.0.0", Description: "SHORTDESC", GeneroConstraint: "^4.0.0"},
		{Name: "longc", LatestVersion: "1.0.0", Description: "LONGDESC", GeneroConstraint: ">=3.20.00 <5.00.00"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)
	target := genero.MustParse("4.01.12")

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &target) })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	// The long constraint must render in full (not truncated).
	if !strings.Contains(out, ">=3.20.00 <5.00.00") {
		t.Errorf("long constraint missing or truncated:\n%s", out)
	}
	shortLine := lineContaining(out, "SHORTDESC")
	longLine := lineContaining(out, "LONGDESC")
	if shortLine == "" || longLine == "" {
		t.Fatalf("expected both rows in output:\n%s", out)
	}
	// Both rows grade ✓; with the GENERO column widened to fit the long
	// constraint, the description starts at the same offset in both.
	if got, want := strings.Index(longLine, "LONGDESC"), strings.Index(shortLine, "SHORTDESC"); got != want {
		t.Errorf("columns misaligned: LONGDESC at %d, SHORTDESC at %d (GENERO column did not widen)\n%s",
			got, want, out)
	}
}

// TestSearchAcrossProviders_StatusColumn covers the STATUS column in the
// multi-provider path: it appears only when a result is deprecated, and renders
// the plain and relocation ("deprecated -> successor") forms.
func TestSearchAcrossProviders_StatusColumn(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "gone", LatestVersion: "1.0.0", Description: "retired", Deprecated: true},
		{Name: "moved", LatestVersion: "1.0.0", Description: "relocated", Deprecated: true, MovedTo: "newpkg"},
		{Name: "live", LatestVersion: "1.0.0", Description: "still here"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, nil) })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	if !strings.Contains(lineContaining(out, "NAME"), "STATUS") {
		t.Errorf("STATUS column header missing when a result is deprecated:\n%s", out)
	}
	if line := lineContaining(out, "gone"); !strings.Contains(line, "deprecated") {
		t.Errorf("gone row = %q, want STATUS deprecated", line)
	}
	if line := lineContaining(out, "moved"); !strings.Contains(line, "deprecated -> newpkg") {
		t.Errorf("moved row = %q, want STATUS relocation", line)
	}
}
