package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ignoreFilename is the name of the per-project ignore file. Patterns
// inside it subtract from the inclusion set computed by `files`/`docs`/
// the bin script list when building the publishable zip.
const ignoreFilename = ".fglpkgignore"

// ignoreRule is a single line of a .fglpkgignore file after parsing.
// `negate` rules re-include a path that an earlier rule excluded.
type ignoreRule struct {
	pattern string
	negate  bool
	dirOnly bool
}

// ignoreSet is an ordered list of rules. Rules are evaluated in file
// order; the last rule that matches a given path decides whether it is
// excluded, mirroring gitignore semantics.
type ignoreSet struct {
	rules []ignoreRule
}

// loadIgnore reads .fglpkgignore from root. A missing file is not an
// error — it simply returns an empty set.
func loadIgnore(root string) (*ignoreSet, error) {
	path := filepath.Join(root, ignoreFilename)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ignoreSet{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var set ignoreSet
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := ignoreRule{pattern: line}
		if strings.HasPrefix(rule.pattern, "!") {
			rule.negate = true
			rule.pattern = strings.TrimPrefix(rule.pattern, "!")
		}
		if strings.HasSuffix(rule.pattern, "/") {
			rule.dirOnly = true
			rule.pattern = strings.TrimRight(rule.pattern, "/")
		}
		if rule.pattern == "" {
			continue
		}
		set.rules = append(set.rules, rule)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &set, nil
}

// dirShouldBeSkipped reports whether a directory encountered during a
// filepath.Walk should be pruned entirely (return filepath.SkipDir at the
// call site), by checking it against .fglpkgignore with isDir=true. This
// is the only way a dirOnly (trailing-slash) ignore rule ever applies —
// shouldExclude skips dirOnly rules whenever isDir is false, so a walk
// that only ever calls shouldExclude on individual files (isDir=false)
// can never honour a trailing-slash pattern like "test/", regardless of
// what it names. path is the directory path as seen by filepath.Walk
// (relative to the walk's root); root is "." for a walk rooted at the
// current directory, or a different WalkDir root for e.g. a
// webcomponent's own subtree — path is always converted to be relative
// to "." before matching, since .fglpkgignore patterns are project-root
// relative regardless of which subtree is being walked.
func dirShouldBeSkipped(ignore *ignoreSet, path string) bool {
	rel, err := filepath.Rel(".", path)
	if err != nil || rel == "." {
		return false
	}
	return ignore.shouldExclude(filepath.ToSlash(rel), true)
}

// shouldExclude reports whether a relative path should be omitted from
// the zip. relPath is normalised to forward slashes before matching so
// patterns work the same on Windows. Empty rule sets always return false.
func (s *ignoreSet) shouldExclude(relPath string, isDir bool) bool {
	if s == nil || len(s.rules) == 0 {
		return false
	}
	rel := filepath.ToSlash(relPath)
	excluded := false
	for _, r := range s.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if !ignoreMatch(r.pattern, rel) {
			continue
		}
		if r.negate {
			excluded = false
		} else {
			excluded = true
		}
	}
	return excluded
}

// ignoreMatch implements a small subset of gitignore matching: a leading
// "/" anchors the pattern to the project root; otherwise the pattern is
// tried against the full relative path and any path segment. "**" matches
// any number of directories. "*" matches a single path segment.
func ignoreMatch(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)

	if strings.HasPrefix(pattern, "/") {
		// Anchored to root — match the full rel path only, with no
		// basename fallback (so "/build" does not match "nested/build").
		anchored := strings.TrimPrefix(pattern, "/")
		matched, _ := filepath.Match(anchored, rel)
		return matched
	}

	if matchGlob(pattern, rel) {
		return true
	}
	if !strings.ContainsAny(pattern, "/") {
		// Unanchored simple pattern: try every path segment so that
		// "build" matches both "build" and "nested/build/x.txt".
		for _, seg := range strings.Split(rel, "/") {
			if matched, _ := filepath.Match(pattern, seg); matched {
				return true
			}
		}
	}
	return false
}
