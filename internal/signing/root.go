package signing

// Root is a pinned trust anchor: an offline root public key the client trusts
// to certify keys manifests. KeyID is matched against a manifest's
// sig.rootKeyid; PubB64 is standard base64 of the raw 32-byte Ed25519 key.
type Root struct {
	KeyID  string
	PubB64 string
}

// pinnedRoots holds the root public keys baked into the binary. A root is added
// here (which requires a new CLI release) when a trust anchor is minted:
//
//   - the fglpkg RELEASE-signing root, generated offline by
//     scripts/gen-signing-key (GIS-255 self-update R1) — PINNED below;
//   - the Genero Intelligence PACKAGE-signing root, for verify-on-install
//     (GIS-244) — a DIFFERENT key, kept independent so a compromise of one
//     trust domain cannot forge the other — PINNED below.
//
// With no matching pinned root, Manifest.Verify fails closed
// (ErrManifestUnverified), so nothing is trusted by default.
var pinnedRoots = []Root{
	// fglpkg release-signing root. The private half is held offline by the
	// release owner and never touches CI or this repo; only this public key
	// lives in the binary. Rotating it requires a new CLI release.
	{KeyID: "fglpkg-release-root-1", PubB64: "ZXf5zT+FnkZyYsyEL4mO51cS0zi2q1FNoR4E7ZKN4TE="},

	// Genero Intelligence package-signing root (GIS-244, verify-on-install).
	// root-test-1 is the GI *test* registry
	// (genero-intelligence-test.michael-folcher.workers.dev) root; the
	// production registry root is added alongside it at customer-release time,
	// so old+new roots can overlap across a rotation.
	{KeyID: "root-test-1", PubB64: "IT1y7PBb9/ZXkbIuWcAPRSANiez/A3yLe9z5ps+DoXk="},
}

// PinnedRoots returns the root trust anchors baked into this binary.
func PinnedRoots() []Root { return pinnedRoots }

func findRoot(roots []Root, keyid string) (Root, bool) {
	for _, r := range roots {
		if r.KeyID == keyid {
			return r, true
		}
	}
	return Root{}, false
}
