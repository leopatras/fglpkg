package registry

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LatestRelease is the client's view of GET /registry/fglpkg/latest — the
// self-update contract (see specs/self-update.md and GIS-256). The registry
// stores only the version and derives the URLs from it; binaries live on GitHub
// Releases, which GI neither hosts nor proxies.
type LatestRelease struct {
	Version         string         `json:"version"`         // latest STABLE release (no pre-release)
	Notes           string         `json:"notes"`           // human-facing release notes URL
	ChecksumsURL    string         `json:"checksumsUrl"`    // URL of the release's checksums.txt
	ChecksumsSigURL string         `json:"checksumsSigUrl"` // detached Ed25519 signature over checksums.txt
	KeysURL         string         `json:"keysUrl"`         // optional: working-key manifest (keys.json)
	ManualURL       string         `json:"manualUrl"`       // operator-configurable manual-download URL
	Instructions    string         `json:"instructions"`    // operator-configurable recovery instructions
	Assets          []ReleaseAsset `json:"assets"`
}

// ReleaseAsset is one platform binary. os/arch use Go's runtime.GOOS/GOARCH
// spellings, so the client matches directly.
type ReleaseAsset struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	URL  string `json:"url"`
}

// FetchLatestFGLPkg queries the registry's latest-release endpoint. The endpoint
// is public (auth, if present, only lifts rate limits), so this uses the normal
// authenticated GET which also works unauthenticated. A registry that predates
// the endpoint returns 404, surfaced as ErrNotFound so callers can treat it as
// "no update info" (a silent no-op for the passive check).
func FetchLatestFGLPkg() (*LatestRelease, error) {
	u := registryBase() + "/registry/fglpkg/latest"
	data, err := httpGetAuthed(u)
	if err != nil {
		return nil, err // ErrNotFound on 404
	}
	var lr LatestRelease
	if err := json.Unmarshal(data, &lr); err != nil {
		return nil, fmt.Errorf("invalid latest-release response: %w", err)
	}
	if lr.Version == "" {
		return nil, fmt.Errorf("latest-release response missing version")
	}
	return &lr, nil
}

// AssetFor returns the asset matching goos/goarch, or nil if this platform has
// no published binary.
func (lr *LatestRelease) AssetFor(goos, goarch string) *ReleaseAsset {
	for i := range lr.Assets {
		if lr.Assets[i].OS == goos && lr.Assets[i].Arch == goarch {
			return &lr.Assets[i]
		}
	}
	return nil
}

// KeysManifestURL returns the URL of the working-key manifest (keys.json). It
// prefers an explicit keysUrl from the endpoint and otherwise derives it from
// checksumsUrl, since keys.json is published as a sibling release asset. Empty
// if neither is available.
func (lr *LatestRelease) KeysManifestURL() string {
	if lr.KeysURL != "" {
		return lr.KeysURL
	}
	if i := strings.LastIndex(lr.ChecksumsURL, "/"); i >= 0 {
		return lr.ChecksumsURL[:i+1] + "keys.json"
	}
	return ""
}
