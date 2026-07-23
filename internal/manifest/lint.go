package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// Severity classifies a lint Diagnostic. Errors block (fglpkg lint exits
// non-zero, and pack/publish refuse); warnings are advisory and printed but
// never block on their own.
type Severity int

const (
	// SeverityError marks a problem that must be fixed before the package is
	// fit to build or publish.
	SeverityError Severity = iota
	// SeverityWarning marks a likely-but-not-certain mistake (a footgun) that
	// is surfaced loudly but does not, by itself, stop pack/publish.
	SeverityWarning
)

// Diagnostic is a single lint finding. Field names the offending manifest key
// (e.g. "files", "programs") in user-facing terms, or is empty for a
// whole-manifest finding.
type Diagnostic struct {
	Severity Severity
	Field    string
	Message  string
}

// Report accumulates lint Diagnostics from the manifest layer and the CLI
// (filesystem) layer, so `lint`, `pack`, and `publish` share one structured
// result instead of each re-deriving errors as plain strings.
type Report struct {
	Diagnostics []Diagnostic
}

// Add appends a diagnostic with the given severity.
func (r *Report) Add(sev Severity, field, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Severity: sev, Field: field, Message: msg})
}

// Errorf appends a SeverityError diagnostic, formatting the message.
func (r *Report) Errorf(field, format string, a ...any) {
	r.Add(SeverityError, field, fmt.Sprintf(format, a...))
}

// Warnf appends a SeverityWarning diagnostic, formatting the message.
func (r *Report) Warnf(field, format string, a ...any) {
	r.Add(SeverityWarning, field, fmt.Sprintf(format, a...))
}

// HasErrors reports whether any diagnostic is a SeverityError.
func (r *Report) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Errors returns the SeverityError diagnostics, in insertion order.
func (r *Report) Errors() []Diagnostic { return r.bySeverity(SeverityError) }

// Warnings returns the SeverityWarning diagnostics, in insertion order.
func (r *Report) Warnings() []Diagnostic { return r.bySeverity(SeverityWarning) }

func (r *Report) bySeverity(sev Severity) []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == sev {
			out = append(out, d)
		}
	}
	return out
}

// LintInto appends the manifest-level diagnostics for m to r:
//
//   - Structural problems: surfaced from Validate() as a single error
//     diagnostic (Validate is fail-fast, so at most one is reported per run).
//   - Missing publish-readiness fields (description, license, repository,
//     author): warnings, so `lint` stays useful mid-development while
//     `publish` continues to treat them as hard errors via ValidateForPublish.
//   - Duplicate keywords: a warning.
//
// Filesystem-derived diagnostics (zero-match globs, unresolved programs,
// missing modules, nonexistent root/importRoot) are added separately by the
// CLI layer, which owns the staging/walk primitives.
func (m *Manifest) LintInto(r *Report) {
	if err := m.Validate(); err != nil {
		r.Errorf("", "%s", err.Error())
	}

	for _, f := range publishRequiredFields {
		if strings.TrimSpace(f.getter(m)) == "" {
			msg := f.name + " is not set"
			if f.hintMsg != "" {
				msg += " (" + f.hintMsg + ")"
			}
			msg += "; required before publishing"
			r.Warnf(f.name, "%s", msg)
		}
	}

	if dups := duplicateStrings(m.Keywords); len(dups) > 0 {
		r.Warnf("keywords", "duplicate keyword(s): %s", strings.Join(dups, ", "))
	}
}

// duplicateStrings returns the distinct values that appear more than once in
// values, sorted, for a stable message.
func duplicateStrings(values []string) []string {
	seen := make(map[string]int, len(values))
	for _, v := range values {
		seen[v]++
	}
	var dups []string
	for v, n := range seen {
		if n > 1 {
			dups = append(dups, v)
		}
	}
	sort.Strings(dups)
	return dups
}
