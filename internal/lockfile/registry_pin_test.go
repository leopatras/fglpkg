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
