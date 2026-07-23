package genero_test

import (
	"os"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
)

func TestParse(t *testing.T) {
	cases := []struct {
		input        string
		wantOriginal string
		wantMajor    uint64
		wantMinor    uint64
		wantPatch    uint64
	}{
		{"4.01.12", "4.01.12", 4, 1, 12},
		{"3.20.05", "3.20.05", 3, 20, 5},
		{"4.00.00", "4.00.00", 4, 0, 0},
		{"10.2.3", "10.2.3", 10, 2, 3},
	}

	for _, tc := range cases {
		v, err := genero.Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.input, err)
			continue
		}
		if v.String() != tc.wantOriginal {
			t.Errorf("Parse(%q).String() = %q, want %q", tc.input, v.String(), tc.wantOriginal)
		}
		sv := v.Semver()
		if sv.Major != tc.wantMajor || sv.Minor != tc.wantMinor || sv.Patch != tc.wantPatch {
			t.Errorf("Parse(%q) semver = %d.%d.%d, want %d.%d.%d",
				tc.input, sv.Major, sv.Minor, sv.Patch,
				tc.wantMajor, tc.wantMinor, tc.wantPatch)
		}
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{"4.01", "abc", "", "4.01.x"}
	for _, s := range bad {
		if _, err := genero.Parse(s); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", s)
		}
	}
}

func TestParseLoose(t *testing.T) {
	cases := []struct {
		input        string
		wantOriginal string
		wantMajor    uint64
		wantMinor    uint64
		wantPatch    uint64
	}{
		{"4.01.12", "4.01.12", 4, 1, 12},
		{"4.01", "4.01", 4, 1, 0},   // patch padded
		{"3.20", "3.20", 3, 20, 0},  // patch padded
		{"4", "4", 4, 0, 0},         // minor + patch padded
		{" 4.01 ", "4.01", 4, 1, 0}, // trimmed
	}
	for _, tc := range cases {
		v, err := genero.ParseLoose(tc.input)
		if err != nil {
			t.Errorf("ParseLoose(%q) error: %v", tc.input, err)
			continue
		}
		if v.String() != tc.wantOriginal {
			t.Errorf("ParseLoose(%q).String() = %q, want %q", tc.input, v.String(), tc.wantOriginal)
		}
		sv := v.Semver()
		if sv.Major != tc.wantMajor || sv.Minor != tc.wantMinor || sv.Patch != tc.wantPatch {
			t.Errorf("ParseLoose(%q) semver = %d.%d.%d, want %d.%d.%d",
				tc.input, sv.Major, sv.Minor, sv.Patch,
				tc.wantMajor, tc.wantMinor, tc.wantPatch)
		}
	}
}

func TestParseLooseErrors(t *testing.T) {
	bad := []string{"abc", "", "4.01.x", "4.1.2.3"}
	for _, s := range bad {
		if _, err := genero.ParseLoose(s); err == nil {
			t.Errorf("ParseLoose(%q) expected error, got nil", s)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		version    string
		constraint string
		want       bool
	}{
		// Exact
		{"4.01.12", "=4.1.12", true},
		{"4.01.12", "=4.1.0", false},

		// Ranges
		{"4.01.12", ">=4.0.0", true},
		{"4.01.12", ">=4.2.0", false},
		{"4.01.12", "<5.0.0", true},
		{"4.01.12", "<4.1.0", false},

		// Caret — compatible within major
		{"4.01.12", "^4.0.0", true},
		{"4.01.12", "^4.2.0", false}, // 4.01 < 4.2
		{"3.20.05", "^4.0.0", false},

		// Tilde — patch-level
		{"4.01.12", "~4.1.0", true},
		{"4.01.12", "~4.2.0", false},

		// AND
		{"4.01.12", ">=4.0.0 <5.0.0", true},
		{"4.01.12", ">=4.0.0 <4.1.0", false},

		// OR
		{"4.01.12", "^3.20.0 || ^4.0.0", true},
		{"3.20.05", "^3.20.0 || ^4.0.0", true},
		{"5.00.00", "^3.20.0 || ^4.0.0", false},

		// Wildcard
		{"4.01.12", "*", true},
		{"4.01.12", "", true},
	}

	for _, tc := range cases {
		v := genero.MustParse(tc.version)
		got, err := v.Satisfies(tc.constraint)
		if err != nil {
			t.Errorf("(%q).Satisfies(%q) error: %v", tc.version, tc.constraint, err)
			continue
		}
		if got != tc.want {
			t.Errorf("(%q).Satisfies(%q) = %v, want %v",
				tc.version, tc.constraint, got, tc.want)
		}
	}
}

func TestDetectFromEnvOverride(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "4.01.12")
	v, err := genero.Detect()
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}
	if v.String() != "4.01.12" {
		t.Errorf("Detect() = %q, want %q", v.String(), "4.01.12")
	}
}

// TestDetectFromEnvLoose proves FGLPKG_GENERO_VERSION is parsed leniently
// (matching the --genero flag): a bare MAJOR.MINOR with no patch is accepted,
// shown verbatim, and pads to a semver that satisfies a real constraint.
func TestDetectFromEnvLoose(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "4.01")
	v, err := genero.Detect()
	if err != nil {
		t.Fatalf("Detect(%q) error: %v", "4.01", err)
	}
	if v.String() != "4.01" {
		t.Errorf("Detect() = %q, want %q", v.String(), "4.01")
	}
	if ok, err := v.Satisfies("^4.0.0"); err != nil || !ok {
		t.Errorf("Satisfies(^4.0.0) = %v, %v; want true, nil", ok, err)
	}
}

// TestDetectFromEnvMalformed guards against silent fallthrough: a bad
// FGLPKG_GENERO_VERSION must surface an error rather than being ignored.
func TestDetectFromEnvMalformed(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "not-a-version")
	if _, err := genero.Detect(); err == nil {
		t.Error("Detect() with malformed FGLPKG_GENERO_VERSION: want error, got nil")
	}
}

func TestDetectFailsGracefully(t *testing.T) {
	// Clear all detection sources so we get a clean error.
	os.Unsetenv("FGLPKG_GENERO_VERSION")
	os.Unsetenv("FGLDIR")
	// fglcomp won't be on PATH in CI — if it is, this test is a no-op.
	_, err := genero.Detect()
	// Either succeeds (fglcomp is on PATH) or returns a descriptive error.
	if err != nil {
		if err.Error() == "" {
			t.Error("expected non-empty error message")
		}
	}
}
