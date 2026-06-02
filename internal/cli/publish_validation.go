package cli

import (
	"errors"
	"fmt"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// checkVariantNotPublished returns nil if (m.Name, m.Version, generoMajor)
// is safe to publish against the new registry. It returns:
//
//   - nil when the package is unknown (first publish), when the version
//     exists but the specific variant does not, or when the version is
//     entirely new for an existing package.
//   - a guidance error pointing at `fglpkg version` if the same version
//     AND the same variant are already published.
//   - a wrapped network/server error if the check itself failed —
//     callers must treat this as "we cannot tell whether re-publish
//     would clobber" and abort, not silently allow.
//
// Talks to the consumer endpoint /registry/packages/<slug>; the variant
// list comes from that response. New registry only — the legacy fly.dev
// publish path was removed in the Genero Intelligence cutover.
func checkVariantNotPublished(m *manifest.Manifest, generoMajor string) error {
	variants, err := registry.VariantsFor(m.Name, m.Version)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// Package or version not found — nothing to clobber.
			return nil
		}
		return fmt.Errorf("cannot check whether version %s is already published: %w",
			m.Version, err)
	}
	want := "genero" + generoMajor
	for _, v := range variants {
		if v == want {
			return fmt.Errorf(
				"version %s of %s is already published for Genero %s\n"+
					"bump the version before publishing again:\n"+
					"    fglpkg version patch     # %s -> next patch\n"+
					"    fglpkg version minor     # next minor\n"+
					"    fglpkg version major     # next major",
				m.Version, m.Name, generoMajor, m.Version)
		}
	}
	return nil
}
