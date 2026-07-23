package semver_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ─── Version parsing ─────────────────────────────────────────────────────────

func TestParse(t *testing.T) {
	cases := []struct {
		input string
		major, minor, patch uint64
		pre   string
	}{
		{"1.2.3", 1, 2, 3, ""},
		{"0.0.1", 0, 0, 1, ""},
		{"v2.10.0", 2, 10, 0, ""},
		{"1.2.3-alpha.1", 1, 2, 3, "alpha.1"},
		{"1.2.3-beta+build.42", 1, 2, 3, "beta"},
		{"10.20.30", 10, 20, 30, ""},
	}

	for _, tc := range cases {
		v, err := semver.Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.input, err)
			continue
		}
		if v.Major != tc.major || v.Minor != tc.minor || v.Patch != tc.patch {
			t.Errorf("Parse(%q) = %d.%d.%d, want %d.%d.%d",
				tc.input, v.Major, v.Minor, v.Patch, tc.major, tc.minor, tc.patch)
		}
		if v.PreRelease != tc.pre {
			t.Errorf("Parse(%q) pre = %q, want %q", tc.input, v.PreRelease, tc.pre)
		}
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{"1.2", "1", "abc", "1.2.x", "", "1.2.3.4"}
	for _, s := range bad {
		if _, err := semver.Parse(s); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", s)
		}
	}
}

// ─── Version comparison ───────────────────────────────────────────────────────

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"1.0.0-alpha", "1.0.0", -1},        // pre-release < release
		{"1.0.0-alpha", "1.0.0-beta", -1},   // alpha < beta
		{"1.0.0-1", "1.0.0-2", -1},           // numeric pre-release
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric < alpha
		{"1.0.0-beta.11", "1.0.0-beta.2", 1},      // 11 > 2 numerically
	}

	for _, tc := range cases {
		a := semver.MustParse(tc.a)
		b := semver.MustParse(tc.b)
		got := a.Compare(b)
		// Normalise to -1/0/1
		if got < 0 {
			got = -1
		} else if got > 0 {
			got = 1
		}
		if got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ─── Constraint matching ──────────────────────────────────────────────────────

func TestConstraintMatches(t *testing.T) {
	cases := []struct {
		constraint string
		version    string
		want       bool
	}{
		// Exact
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"=1.2.3", "1.2.3", true},
		{"=1.2.3", "1.2.4", false},

		// Comparison operators
		{">1.0.0", "1.0.1", true},
		{">1.0.0", "1.0.0", false},
		{">=1.0.0", "1.0.0", true},
		{">=1.0.0", "0.9.9", false},
		{"<2.0.0", "1.9.9", true},
		{"<2.0.0", "2.0.0", false},
		{"<=2.0.0", "2.0.0", true},
		{"<=2.0.0", "2.0.1", false},

		// Tilde
		{"~1.2.3", "1.2.3", true},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"~1.2.3", "1.2.2", false},
		{"~1.2", "1.2.0", true},
		{"~1.2", "1.2.99", true},
		{"~1.2", "1.3.0", false},
		{"~1", "1.0.0", true},
		{"~1", "1.99.99", true},
		{"~1", "2.0.0", false},

		// Caret
		{"^1.2.3", "1.2.3", true},
		{"^1.2.3", "1.99.99", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.2", false},
		{"^0.2.3", "0.2.3", true},
		{"^0.2.3", "0.2.99", true},
		{"^0.2.3", "0.3.0", false},
		{"^0.0.3", "0.0.3", true},
		{"^0.0.3", "0.0.4", false},

		// Wildcards
		{"*", "1.2.3", true},
		{"*", "99.0.0", true},
		{"latest", "1.2.3", true},
		{"1.2.x", "1.2.0", true},
		{"1.2.x", "1.2.99", true},
		{"1.2.x", "1.3.0", false},
		{"1.x", "1.0.0", true},
		{"1.x", "1.99.0", true},
		{"1.x", "2.0.0", false},

		// Bare partial (issue #24 M6a): "1.2" behaves like "1.2.x", "1" like
		// "1.x"; a full "1.2.3" stays an exact pin, and "=1.2" is the escape
		// hatch for an exact partial pin.
		{"1.2", "1.2.0", true},
		{"1.2", "1.2.99", true},
		{"1.2", "1.3.0", false},
		{"1.2", "1.1.9", false},
		{"1", "1.0.0", true},
		{"1", "1.99.0", true},
		{"1", "2.0.0", false},
		{"1", "0.9.9", false},
		{"=1.2", "1.2.0", true},
		{"=1.2", "1.2.1", false},

		// AND (space-separated)
		{">=1.0.0 <2.0.0", "1.5.0", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{">=1.0.0 <2.0.0", "0.9.0", false},

		// OR (pipe-separated)
		{"^1.2.0 || ^2.0.0", "1.5.0", true},
		{"^1.2.0 || ^2.0.0", "2.0.0", true},
		{"^1.2.0 || ^2.0.0", "3.0.0", false},

		// Pre-release filtering (pre-release excluded unless constraint has one)
		{"^1.0.0", "1.0.0-alpha", false},
		{"^1.0.0-alpha", "1.0.0-alpha", true},
		{"^1.0.0-alpha", "1.0.0-beta", true}, // still in range
		{">=1.0.0-alpha", "1.0.0-alpha", true},
	}

	for _, tc := range cases {
		c, err := semver.ParseConstraint(tc.constraint)
		if err != nil {
			t.Errorf("ParseConstraint(%q) error: %v", tc.constraint, err)
			continue
		}
		v := semver.MustParse(tc.version)
		got := c.Matches(v)
		if got != tc.want {
			t.Errorf("Constraint(%q).Matches(%q) = %v, want %v",
				tc.constraint, tc.version, got, tc.want)
		}
	}
}

// ─── Latest selection ─────────────────────────────────────────────────────────

func TestLatest(t *testing.T) {
	candidates := semver.MustParseAll(
		"1.0.0", "1.1.0", "1.2.0", "1.2.3", "2.0.0", "2.1.0", "3.0.0-alpha",
	)

	cases := []struct {
		constraint string
		want       string
	}{
		{"^1.0.0", "1.2.3"},
		{"^2.0.0", "2.1.0"},
		{"~1.2.0", "1.2.3"},
		{">=1.0.0 <2.0.0", "1.2.3"},
		{"*", "2.1.0"}, // pre-release excluded
		{"^3.0.0-alpha", "3.0.0-alpha"},
	}

	for _, tc := range cases {
		c := semver.MustParseConstraint(tc.constraint)
		got, err := c.Latest(candidates)
		if err != nil {
			t.Errorf("Latest(%q) error: %v", tc.constraint, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Latest(%q) = %q, want %q", tc.constraint, got.String(), tc.want)
		}
	}
}

func TestLatestNoMatch(t *testing.T) {
	candidates := semver.MustParseAll("1.0.0", "1.1.0")
	c := semver.MustParseConstraint("^2.0.0")
	if _, err := c.Latest(candidates); err == nil {
		t.Error("Expected error for unsatisfiable constraint, got nil")
	}
}

// TestBarePartialStripsBuildMetadata is the regression for issue #24: the
// partial-version parser (used for bare constraint tokens) split off the
// prerelease before stripping "+build" metadata, leaking the build string into
// the prerelease field. Build metadata is not significant in comparisons, so a
// bare constraint carrying it must still match the same version without it.
func TestBarePartialStripsBuildMetadata(t *testing.T) {
	c, err := semver.ParseConstraint("1.2.3-beta+build")
	if err != nil {
		t.Fatalf("ParseConstraint: %v", err)
	}
	if !c.Matches(semver.MustParse("1.2.3-beta")) {
		t.Errorf("constraint %q should match 1.2.3-beta (build metadata must be ignored)", c)
	}
}
