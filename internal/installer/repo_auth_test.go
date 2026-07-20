package installer

import "testing"

// TestMatchRepoAuth_AttackerSuffixHostDoesNotMatch is the regression test for
// GIS-249 S1: a repo configured with URL "https://acme.jfrog.io" must not
// match a download URL whose host merely has that string as a prefix, e.g.
// "https://acme.jfrog.io.attacker.com" — which would otherwise receive the
// repo's auth headers (or, worse, cause the real repo lookup to miss and fall
// through to the GI bearer).
func TestMatchRepoAuth_AttackerSuffixHostDoesNotMatch(t *testing.T) {
	inst := (&Installer{}).WithRepoAuth([]RepoAuth{
		{URLPrefix: "https://acme.jfrog.io", Headers: map[string]string{"Authorization": "Bearer repo-secret"}},
	})

	_, matched := inst.matchRepoAuth("https://acme.jfrog.io.attacker.com/evil.zip")
	if matched {
		t.Fatalf("attacker-suffix host must not match the configured repo")
	}
}

// TestMatchRepoAuth_PathBoundary confirms path-prefix matching respects a "/"
// boundary: a repo configured at ".../repo" must not match ".../repo-other/x",
// but must match ".../repo/x".
func TestMatchRepoAuth_PathBoundary(t *testing.T) {
	inst := (&Installer{}).WithRepoAuth([]RepoAuth{
		{URLPrefix: "https://acme.jfrog.io/repo", Headers: map[string]string{"Authorization": "Bearer repo-secret"}},
	})

	if _, matched := inst.matchRepoAuth("https://acme.jfrog.io/repo-other/x.zip"); matched {
		t.Fatalf("sibling path 'repo-other' must not match repo prefix 'repo'")
	}
	if _, matched := inst.matchRepoAuth("https://acme.jfrog.io/repo/x.zip"); !matched {
		t.Fatalf("path under the configured repo prefix should match")
	}
	if _, matched := inst.matchRepoAuth("https://acme.jfrog.io/repo"); !matched {
		t.Fatalf("exact repo prefix (no trailing path) should match")
	}
}

// TestMatchRepoAuth_DifferentSchemeDoesNotMatch confirms the scheme is part
// of the origin comparison.
func TestMatchRepoAuth_DifferentSchemeDoesNotMatch(t *testing.T) {
	inst := (&Installer{}).WithRepoAuth([]RepoAuth{
		{URLPrefix: "https://acme.jfrog.io", Headers: map[string]string{"Authorization": "Bearer repo-secret"}},
	})

	if _, matched := inst.matchRepoAuth("http://acme.jfrog.io/x.zip"); matched {
		t.Fatalf("http must not match a repo configured as https")
	}
}
