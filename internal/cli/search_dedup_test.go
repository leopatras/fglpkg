package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
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
