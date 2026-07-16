package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractChangelogSection(t *testing.T) {
	doc := `# Changelog

All notable changes.

## [1.2.0] - 2026-07-13

### Added
- Publisher-set changelog.

### Fixed
- A bug.

## [1.1.0] - 2026-06-01

- Older stuff.

## 1.0.0

Initial release.
`
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{
			name:    "bracketed with date",
			version: "1.2.0",
			want:    "### Added\n- Publisher-set changelog.\n\n### Fixed\n- A bug.",
		},
		{
			name:    "middle section stops at next heading",
			version: "1.1.0",
			want:    "- Older stuff.",
		},
		{
			name:    "bare version, runs to EOF",
			version: "1.0.0",
			want:    "Initial release.",
		},
		{
			name:    "no matching section",
			version: "9.9.9",
			want:    "",
		},
		{
			name:    "no false prefix match",
			version: "1.2", // must not match "1.2.0"
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractChangelogSection(doc, tc.version)
			if got != tc.want {
				t.Errorf("version %q:\n got: %q\nwant: %q", tc.version, got, tc.want)
			}
		})
	}
}

func TestCollectChangelog(t *testing.T) {
	t.Run("absent file is not found and not an error", func(t *testing.T) {
		dir := t.TempDir()
		section, found, err := collectChangelog(dir, "1.0.0")
		if err != nil {
			t.Fatalf("collectChangelog: %v", err)
		}
		if found || section != "" {
			t.Errorf("got found=%v section=%q, want found=false section=\"\"", found, section)
		}
	})

	t.Run("present with matching section", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "CHANGELOG.md"), "## [2.0.0]\n\n- New major.\n")
		section, found, err := collectChangelog(dir, "2.0.0")
		if err != nil {
			t.Fatalf("collectChangelog: %v", err)
		}
		if !found {
			t.Fatal("expected found=true")
		}
		if section != "- New major." {
			t.Errorf("got %q", section)
		}
	})

	t.Run("present but no matching section", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "CHANGELOG.md"), "## [1.0.0]\n\n- Old.\n")
		section, found, err := collectChangelog(dir, "2.0.0")
		if err != nil {
			t.Fatalf("collectChangelog: %v", err)
		}
		if !found {
			t.Fatal("expected found=true (file exists)")
		}
		if section != "" {
			t.Errorf("expected empty section for missing version, got %q", section)
		}
	})

	t.Run("oversized content truncated", func(t *testing.T) {
		dir := t.TempDir()
		body := strings.Repeat("x", maxReadmeBytes+100)
		writeFile(t, filepath.Join(dir, "CHANGELOG.md"), "## [1.0.0]\n\n"+body)
		section, found, err := collectChangelog(dir, "1.0.0")
		if err != nil {
			t.Fatalf("collectChangelog: %v", err)
		}
		if !found {
			t.Fatal("expected found=true")
		}
		if !strings.HasSuffix(section, strings.TrimSpace(changelogTruncationMarker)) {
			t.Errorf("expected truncation marker suffix, got tail %q", section[len(section)-40:])
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
