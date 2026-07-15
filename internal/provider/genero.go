package provider

import (
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// GeneroProvider is a thin wrapper over the existing package-level registry
// client functions. It carries no per-instance base URL or bearer: the GI
// client reads its base from registryBase() (FGLPKG_REGISTRY) and its bearer
// from the process-global registry.Bearer hook wired at CLI startup. This keeps
// single-registry behaviour byte-identical (the thin-wrapper approach); a full
// extraction to per-instance state can follow later.
type GeneroProvider struct {
	name string
}

// NewGeneroProvider returns a GeneroProvider with the given logical name
// (defaults to "gi").
func NewGeneroProvider(name string) *GeneroProvider {
	if name == "" {
		name = registryName
	}
	return &GeneroProvider{name: name}
}

// registryName is the default logical name of the GI registry.
const registryName = "gi"

func (g *GeneroProvider) Name() string { return g.name }

func (g *GeneroProvider) FetchVersions(name string) ([]resolver.CandidateVersion, error) {
	vl, err := registry.FetchVersionList(name)
	if err != nil {
		return nil, err
	}
	out := make([]resolver.CandidateVersion, 0, len(vl.VersionEntries))
	for _, ve := range vl.VersionEntries {
		v, err := semver.Parse(ve.Version)
		if err != nil {
			continue // skip non-semver version tags, mirroring registryVersions
		}
		out = append(out, resolver.CandidateVersion{
			Version:          v,
			GeneroConstraint: ve.GeneroConstraint,
		})
	}
	return out, nil
}

func (g *GeneroProvider) FetchInfo(name, version, generoMajor string) (*registry.PackageInfo, error) {
	info, err := registry.FetchInfoForGenero(name, version, generoMajor)
	if err != nil {
		return nil, err
	}
	info.Source = g.name
	return info, nil
}

func (g *GeneroProvider) Search(term string) ([]registry.SearchResult, error) {
	return registry.Search(term)
}
