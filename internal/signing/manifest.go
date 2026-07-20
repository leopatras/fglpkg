package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Key is one working (signing) key entry in a keys manifest.
type Key struct {
	KeyID     string `json:"keyid"`
	Alg       string `json:"alg"`
	Pub       string `json:"pub"`       // standard base64 of the raw 32-byte Ed25519 public key
	ValidFrom string `json:"validFrom"` // RFC 3339
	ValidTo   string `json:"validTo"`   // RFC 3339
}

// ManifestSig is the detached root signature block attached to a manifest.
type ManifestSig struct {
	RootKeyID string `json:"rootKeyid"`
	Alg       string `json:"alg"`
	Sig       string `json:"sig"` // standard base64 of the raw 64-byte Ed25519 signature
}

// Manifest is the keys.json document: a set of working keys plus a detached
// signature over {issuedAt, keys} made by the offline root key.
type Manifest struct {
	IssuedAt string      `json:"issuedAt"`
	Keys     []Key       `json:"keys"`
	Sig      ManifestSig `json:"sig"`
}

// ParseManifest decodes keys.json bytes.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("signing: parse keys manifest: %w", err)
	}
	return &m, nil
}

// SigningInput returns the RFC 8785 canonical bytes the root signs: the manifest
// minus its own sig block, i.e. canonicalize({issuedAt, keys}). Both this
// verifier and scripts/gen-signing-key compute the signature over these bytes,
// so they cannot drift.
func (m *Manifest) SigningInput() ([]byte, error) {
	keys := make([]interface{}, 0, len(m.Keys))
	for _, k := range m.Keys {
		keys = append(keys, map[string]interface{}{
			"keyid":     k.KeyID,
			"alg":       k.Alg,
			"pub":       k.Pub,
			"validFrom": k.ValidFrom,
			"validTo":   k.ValidTo,
		})
	}
	return canonicalize(map[string]interface{}{
		"issuedAt": m.IssuedAt,
		"keys":     keys,
	})
}

// Verify checks the manifest's root signature against the pinned roots. It
// returns nil only if sig.rootKeyid matches a pinned root and the Ed25519
// signature verifies over SigningInput(); otherwise ErrManifestUnverified.
func (m *Manifest) Verify(roots []Root) error {
	root, ok := findRoot(roots, m.Sig.RootKeyID)
	if !ok {
		return fmt.Errorf("%w: unknown rootKeyid %q", ErrManifestUnverified, m.Sig.RootKeyID)
	}
	pub, err := decodeEd25519Pub(root.PubB64)
	if err != nil {
		return fmt.Errorf("%w: bad pinned root key: %v", ErrManifestUnverified, err)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Sig.Sig)
	if err != nil {
		return fmt.Errorf("%w: bad signature encoding: %v", ErrManifestUnverified, err)
	}
	input, err := m.SigningInput()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrManifestUnverified, err)
	}
	if !ed25519.Verify(pub, input, sig) {
		return ErrManifestUnverified
	}
	return nil
}

// SelectKey returns the working key with the given keyid whose validity window
// covers at (inclusive). Callers pass the artifact's uploaded_at (Layer 1) or
// the release time. Returns ErrKeyUnknown or ErrKeyExpired.
//
// Verify the manifest against the pinned root before trusting any key it names.
func (m *Manifest) SelectKey(keyid string, at time.Time) (*Key, error) {
	for i := range m.Keys {
		if m.Keys[i].KeyID != keyid {
			continue
		}
		k := &m.Keys[i]
		from, err := time.Parse(time.RFC3339, k.ValidFrom)
		if err != nil {
			return nil, fmt.Errorf("signing: key %q has invalid validFrom: %w", keyid, err)
		}
		to, err := time.Parse(time.RFC3339, k.ValidTo)
		if err != nil {
			return nil, fmt.Errorf("signing: key %q has invalid validTo: %w", keyid, err)
		}
		if at.Before(from) || at.After(to) {
			return nil, fmt.Errorf("%w: key %q valid %s..%s, artifact at %s",
				ErrKeyExpired, keyid, k.ValidFrom, k.ValidTo, at.UTC().Format(time.RFC3339))
		}
		return k, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrKeyUnknown, keyid)
}

// ValidKeys returns the working keys whose validity window covers at. Used where
// no keyid accompanies the signature (the detached checksums.txt.sig release
// path): the caller tries each returned key. Verify the manifest against the
// pinned root before trusting these keys. Entries with unparseable windows are
// skipped.
func (m *Manifest) ValidKeys(at time.Time) []Key {
	var out []Key
	for _, k := range m.Keys {
		from, err1 := time.Parse(time.RFC3339, k.ValidFrom)
		to, err2 := time.Parse(time.RFC3339, k.ValidTo)
		if err1 != nil || err2 != nil {
			continue
		}
		if !at.Before(from) && !at.After(to) {
			out = append(out, k)
		}
	}
	return out
}
