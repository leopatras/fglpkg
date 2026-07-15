package lockfile_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func TestFromPlan_RecordsSourceAsRegistry(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("6.00.01"),
		Packages: []resolver.ResolvedPackage{
			{Name: "acme-utils", Version: semver.MustParse("1.0.0"), Source: "acme-internal", RequiredBy: []string{"<root>"}},
			{Name: "logft", Version: semver.MustParse("2.0.0"), RequiredBy: []string{"<root>"}}, // no source (gi default)
		},
	}
	lf := lockfile.FromPlan(plan, manifest.New("app", "1.0.0", "", ""))

	var acme, logft *lockfile.LockedPackage
	for i := range lf.Packages {
		switch lf.Packages[i].Name {
		case "acme-utils":
			acme = &lf.Packages[i]
		case "logft":
			logft = &lf.Packages[i]
		}
	}
	if acme == nil || acme.Registry != "acme-internal" {
		t.Fatalf("acme-utils registry = %+v", acme)
	}
	if logft == nil || logft.Registry != "" {
		t.Fatalf("logft should have empty registry (gi default): %+v", logft)
	}
}

func TestCheckRegistries(t *testing.T) {
	lf := &lockfile.LockFile{
		Packages: []lockfile.LockedPackage{
			{Name: "logft"},                              // empty registry = gi default
			{Name: "acme-utils", Registry: "acme-internal"},
		},
		Webcomponents: []lockfile.LockedWebcomponent{
			{Name: "chart-3d", Registry: "acme-internal"},
		},
	}

	// All recorded registries configured → no error.
	if err := lf.CheckRegistries([]string{"gi", "acme-internal"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// acme-internal removed from config → clear error naming the package and repo.
	err := lf.CheckRegistries([]string{"gi"})
	if err == nil {
		t.Fatal("expected error when a locked registry is not configured")
	}
	if !strings.Contains(err.Error(), "acme-utils") || !strings.Contains(err.Error(), "acme-internal") {
		t.Errorf("error should name the package and repo: %v", err)
	}

	// Empty configured set (caller couldn't determine it) → check skipped.
	if err := lf.CheckRegistries(nil); err != nil {
		t.Errorf("nil configured should skip the check: %v", err)
	}
}

func TestCheckRegistries_WebcomponentSource(t *testing.T) {
	lf := &lockfile.LockFile{
		Webcomponents: []lockfile.LockedWebcomponent{
			{Name: "chart-3d", Registry: "gone"},
		},
	}
	if err := lf.CheckRegistries([]string{"gi"}); err == nil {
		t.Fatal("expected error for webcomponent referencing an unconfigured repo")
	}
}

func TestFromPlan_EmptySourceOmittedInJSON(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("6.00.01"),
		Packages: []resolver.ResolvedPackage{
			{Name: "logft", Version: semver.MustParse("2.0.0"), RequiredBy: []string{"<root>"}},
		},
	}
	lf := lockfile.FromPlan(plan, manifest.New("app", "1.0.0", "", ""))
	data, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"registry"`) {
		t.Fatalf("empty registry must be omitted for byte-identical legacy locks: %s", data)
	}
}
