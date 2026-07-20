// Package slug computes and validates canonical fglpkg package slugs.
//
// A package's canonical slug is its identity in URLs, registry storage,
// lookups, the lockfile, and search. It follows the PyPI / PEP 503 rule:
// lowercase, with every maximal run of "-", "_", or "." collapsed to a single
// "-". So "fgl_ai_sdk", "Fgl.AI.SDK", and "fgl-ai-sdk" all canonicalize to
// "fgl-ai-sdk" and refer to the same package (GIS-271).
package slug

import (
	"regexp"
	"strings"
)

var (
	// sepRun matches a maximal run of slug separator characters.
	sepRun = regexp.MustCompile(`[-_.]+`)

	// validRe is the shape a canonical slug must satisfy: 2–64 chars,
	// lowercase alphanumerics and hyphens, starting and ending alphanumeric.
	validRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)
)

// Canonical returns the canonical slug for a package name: lowercased, with
// each run of separator characters ('-', '_', '.') collapsed to a single '-'
// (PEP 503). It is idempotent — Canonical(Canonical(x)) == Canonical(x). It
// does not by itself guarantee a well-formed slug (e.g. a name that is all
// separators, or starts/ends with one, still needs IsValid); callers validate
// the result.
func Canonical(name string) string {
	return strings.ToLower(sepRun.ReplaceAllString(name, "-"))
}

// IsValid reports whether s is a well-formed canonical slug: 2–64 characters,
// lowercase alphanumerics and hyphens, starting and ending with an
// alphanumeric. A valid slug is always already canonical.
func IsValid(s string) bool {
	return validRe.MatchString(s)
}
