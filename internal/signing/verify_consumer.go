package signing

import (
	"fmt"
	"strings"
	"time"
)

// This file carries the consumer (verify-on-install, GIS-244) surface layered
// on the shared primitives in payload.go/manifest.go/verify.go. The registry
// signs each artifact with a working key named in the keys manifest; the
// installer and `fglpkg audit signatures` reconstruct the canonical payload and
// check it here.

// ArtifactSignature is the signature envelope attached to an artifact record:
// the working keyid that signed it, the algorithm, and the base64 signature.
type ArtifactSignature struct {
	KeyID string
	Alg   string
	Sig   string
}

// KeyByID returns the working key with the given keyid, if present. It does not
// check the validity window (SelectKey does) — verify the manifest against a
// pinned root before trusting any key it names.
func (m *Manifest) KeyByID(keyid string) (Key, bool) {
	for i := range m.Keys {
		if m.Keys[i].KeyID == keyid {
			return m.Keys[i], true
		}
	}
	return Key{}, false
}

// VerifyArtifact verifies an artifact's registry signature against the
// manifest's working keys, performing the same three checks as the reference
// verifier:
//
//  1. the signature's keyid is present in the manifest (else ErrKeyUnknown);
//  2. the artifact's upload time falls within that key's validity window
//     (else ErrKeyExpired);
//  3. the Ed25519 signature verifies against the reconstructed canonical
//     payload (else ErrSignatureMismatch).
//
// Backfill note: the registry signs backfilled historical artifacts with the
// current working key but keeps uploaded_at at the artifact's original
// created_at, which can predate the key's validFrom. Such artifacts fail the
// window check and surface as ErrKeyExpired; under the default "warn"
// enforcement that is a warning, not a hard failure.
func (m *Manifest) VerifyArtifact(f ArtifactFields, sig ArtifactSignature) error {
	at, err := parseTimestamp(f.UploadedAt)
	if err != nil {
		return fmt.Errorf("%w: cannot parse upload time %q: %v", ErrKeyExpired, f.UploadedAt, err)
	}
	key, err := m.SelectKey(sig.KeyID, at)
	if err != nil {
		return err // ErrKeyUnknown or ErrKeyExpired, already contextual
	}
	if err := VerifyArtifact(f, sig.Sig, key.Pub); err != nil {
		return fmt.Errorf("%s@%s (%s), keyid %s: %w", f.Name, f.Version, f.Variant, sig.KeyID, err)
	}
	return nil
}

// parseTimestamp accepts RFC 3339 timestamps and the SQLite
// "YYYY-MM-DD HH:MM:SS" form (assumed UTC) that the registry stores for
// uploaded_at, returning a UTC time. Mirrors the reference verifier.
func parseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp format")
}

// parseAndVerify parses raw keys.json bytes and verifies the manifest against
// the pinned roots baked into the binary. It returns ErrManifestUnverified if
// the root is untrusted or the signature does not verify — callers must never
// trust the keys inside an unverified manifest.
func parseAndVerify(raw []byte) (*Manifest, error) {
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	if err := m.Verify(pinnedRoots); err != nil {
		return nil, err
	}
	return m, nil
}
