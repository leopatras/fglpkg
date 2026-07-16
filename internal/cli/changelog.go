package cli

import (
	"regexp"
	"strings"
)

// changelogCandidates mirror readmeCandidates for the package's changelog.
// Same ordering preference (markdown first), same case-insensitive
// root-level basename match. First match wins.
var changelogCandidates = []string{
	"CHANGELOG.md",
	"CHANGELOG.markdown",
	"CHANGELOG.rst",
	"CHANGELOG.txt",
	"CHANGELOG",
}

const changelogTruncationMarker = "\n\n*(CHANGELOG truncated at 256 KB)*\n"

// collectChangelog scans dir for a top-level CHANGELOG (markdown first,
// then rst/txt/plain) and returns the section for the given version.
// It reuses the shared collectDoc scan + 256 KB cap, then extracts the
// per-version section via extractChangelogSection.
//
// The registry stores a changelog per version, so we send only the entry
// for the version being published rather than the whole history.
//
// Returns:
//   - (section, true, nil)  when a CHANGELOG exists and has a matching section
//   - ("", true, nil)       when a CHANGELOG exists but has no matching section
//     (the caller warns the publisher and sends an empty changelog)
//   - ("", false, nil)      when no CHANGELOG file is present (not an error)
func collectChangelog(dir, version string) (section string, found bool, err error) {
	raw, err := collectDoc(dir, "CHANGELOG", changelogCandidates, changelogTruncationMarker)
	if err != nil {
		return "", false, err
	}
	if raw == "" {
		return "", false, nil
	}
	return extractChangelogSection(raw, version), true, nil
}

// changelogHeadingRE matches a "Keep a Changelog" version heading:
//
//	## [1.2.0] - 2026-07-13
//	## 1.2.0
//	##[1.2.0]
//
// Capture group 1 is the bare version string (brackets and any trailing
// " - date" / description are stripped). The leading "## " is the
// conventional level, but we accept any run of '#' so a doc using "###"
// per release still matches.
var changelogHeadingRE = regexp.MustCompile(`(?m)^#{1,6}\s*\[?\s*v?([0-9][0-9A-Za-z.\-+]*)\s*\]?`)

// extractChangelogSection returns the body of the changelog entry whose
// heading names version, excluding the heading line itself and trimmed of
// surrounding blank lines. The section runs from just after its heading to
// the next version heading (or end of file). Returns "" when no heading
// matches the version.
func extractChangelogSection(content, version string) string {
	locs := changelogHeadingRE.FindAllStringSubmatchIndex(content, -1)
	for i, loc := range locs {
		// loc[2]:loc[3] is capture group 1 (the bare version).
		if content[loc[2]:loc[3]] != version {
			continue
		}
		// Body starts at the end of the heading line.
		bodyStart := loc[1]
		if nl := strings.IndexByte(content[loc[0]:], '\n'); nl >= 0 {
			bodyStart = loc[0] + nl + 1
		}
		bodyEnd := len(content)
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		}
		return strings.TrimSpace(content[bodyStart:bodyEnd])
	}
	return ""
}
