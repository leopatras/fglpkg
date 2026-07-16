// Package provider abstracts a package repository behind a single interface so
// fglpkg can consult more than one source (the built-in Genero Intelligence
// registry plus one or more JFrog Artifactory repositories).
//
// The interface covers the CONSUME path only — the operations the resolver and
// routing layer need. Publishing is intentionally not part of the interface:
// the GI publish flow is a multi-step protocol already wired into the CLI and
// kept byte-identical (the "thin-wrapper" approach), while Artifactory publish
// is a direct-PUT deploy exposed by *ArtifactoryProvider directly. See
// specs/artifactory-secondary-repository.md §5.
package provider

import (
	"fmt"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// ErrNotFound indicates a package (or a specific version) is absent in a
// provider's repository. It wraps registry.ErrNotFound so existing
// errors.Is(err, registry.ErrNotFound) checks continue to work, and the
// routing layer relies on it to count how many repositories hold a name.
var ErrNotFound = fmt.Errorf("package not found in repository: %w", registry.ErrNotFound)

// Provider is one package repository fglpkg can resolve packages from.
//
// FetchVersions/FetchInfo MUST return an error satisfying
// errors.Is(err, registry.ErrNotFound) when the name (or version) is absent,
// and MUST return a distinct, non-not-found error for auth failures — the
// routing layer treats "absent" and "unauthorised" very differently.
type Provider interface {
	// Name is the logical repository id (e.g. "gi", "acme-internal").
	Name() string

	// FetchVersions lists available versions and their Genero constraints.
	FetchVersions(name string) ([]resolver.CandidateVersion, error)

	// FetchInfo returns full metadata plus an absolute download URL for
	// name@version, variant-selected by generoMajor ("" = default). The
	// returned PackageInfo.Source is stamped with this provider's Name().
	FetchInfo(name, version, generoMajor string) (*registry.PackageInfo, error)

	// Search returns packages matching term within this repository.
	Search(term string) ([]registry.SearchResult, error)
}
