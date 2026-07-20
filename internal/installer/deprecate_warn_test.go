package installer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func TestWarnDeprecationsPrintsMessageAndSuccessor(t *testing.T) {
	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "ok-pkg", Version: semver.MustParse("1.0.0")},
			{
				Name:               "chart-3d",
				Version:            semver.MustParse("1.2.3"),
				Deprecated:         true,
				DeprecationMessage: "security fix in 1.2.4",
				MovedTo:            "chart-3d-ng",
			},
		},
	}

	var buf bytes.Buffer
	warnDeprecations(plan, &buf)
	out := buf.String()

	if !strings.Contains(out, "chart-3d@1.2.3 is deprecated: security fix in 1.2.4") {
		t.Errorf("missing deprecation warning, got:\n%s", out)
	}
	if !strings.Contains(out, "chart-3d has moved to chart-3d-ng") {
		t.Errorf("missing moved-to line, got:\n%s", out)
	}
	if !strings.Contains(out, "fglpkg install chart-3d-ng") {
		t.Errorf("missing successor hint, got:\n%s", out)
	}
	if strings.Contains(out, "ok-pkg") {
		t.Errorf("non-deprecated package should not warn, got:\n%s", out)
	}
}

// A package with no successor warns without a moved-to line.
func TestWarnDeprecationsNoSuccessor(t *testing.T) {
	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "old", Version: semver.MustParse("1.0.0"), Deprecated: true, DeprecationMessage: "unmaintained"},
		},
	}
	var buf bytes.Buffer
	warnDeprecations(plan, &buf)
	out := buf.String()
	if !strings.Contains(out, "old@1.0.0 is deprecated: unmaintained") {
		t.Errorf("missing warning, got:\n%s", out)
	}
	if strings.Contains(out, "has moved to") {
		t.Errorf("should not print a moved-to line, got:\n%s", out)
	}
}

// De-duplicate: the same (name, version) appearing twice warns once. (In a real
// plan each name is unique, but the guard protects against duplicates.)
func TestWarnDeprecationsDeduplicates(t *testing.T) {
	dep := resolver.ResolvedPackage{
		Name: "chart-3d", Version: semver.MustParse("1.2.3"),
		Deprecated: true, DeprecationMessage: "msg",
	}
	plan := &resolver.Plan{Packages: []resolver.ResolvedPackage{dep, dep}}

	var buf bytes.Buffer
	warnDeprecations(plan, &buf)
	if n := strings.Count(buf.String(), "is deprecated"); n != 1 {
		t.Errorf("warned %d times, want 1 (deduplicated)", n)
	}
}
