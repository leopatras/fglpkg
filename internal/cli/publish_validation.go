package cli

import (
	"errors"
	"fmt"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// checkVersionNotPublished asks the registry whether m.Version of
// m.Name is already published. Returns:
//
//   - nil if the version is free (including the first-publish case
//     where the package itself is unknown to the registry).
//   - a guidance error pointing at `fglpkg version` if the version
//     is already taken.
//   - a wrapped network/server error if the check itself failed —
//     callers must treat this as "we cannot tell whether re-publish
//     would clobber" and abort, not silently allow.
func checkVersionNotPublished(m *manifest.Manifest) error {
	vl, err := registry.FetchVersionList(m.Name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// First publish for this package name. Nothing to clobber.
			return nil
		}
		return fmt.Errorf("cannot check whether version %s is already published: %w",
			m.Version, err)
	}
	for _, v := range vl.Versions {
		if v == m.Version {
			return fmt.Errorf(
				"version %s of %s is already published\n"+
					"bump the version before publishing again:\n"+
					"    fglpkg version patch     # %s -> next patch\n"+
					"    fglpkg version minor     # next minor\n"+
					"    fglpkg version major     # next major",
				m.Version, m.Name, m.Version)
		}
	}
	return nil
}
