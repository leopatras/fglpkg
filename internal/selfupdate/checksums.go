package selfupdate

import (
	"fmt"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// verifiedChecksums holds SHA-256 digests that have been authenticated via the
// Ed25519 release-signing chain — never trust a digest that did not come from
// here.
type verifiedChecksums struct{ byFile map[string]string }

// sha returns the authenticated SHA-256 for filename, if present.
func (v *verifiedChecksums) sha(filename string) (string, bool) {
	s, ok := v.byFile[filename]
	return s, ok
}

// authenticateChecksums establishes authenticity BEFORE any digest is trusted:
//
//  1. verify the keys.json manifest against the pinned root(s);
//  2. verify the detached checksums signature against a working key the manifest
//     lists as valid at `at`;
//  3. only then parse checksums.txt into per-file SHA-256 digests.
//
// Any failure returns an error and no digests — the caller must abort rather
// than install an unverified binary.
func authenticateChecksums(checksums, sig, keysJSON []byte, roots []signing.Root, at time.Time) (*verifiedChecksums, error) {
	m, err := signing.ParseManifest(keysJSON)
	if err != nil {
		return nil, fmt.Errorf("keys manifest: %w", err)
	}
	if err := m.Verify(roots); err != nil {
		return nil, err // ErrManifestUnverified
	}
	valid := m.ValidKeys(at)
	if len(valid) == 0 {
		return nil, fmt.Errorf("no signing key is valid at %s", at.UTC().Format(time.RFC3339))
	}
	verified := false
	for _, k := range valid {
		if signing.VerifyDetached(checksums, string(sig), k.Pub) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, signing.ErrSignatureMismatch
	}
	return parseChecksums(checksums), nil
}

// parseChecksums parses `sha256sum`-format lines ("<hex>  <filename>"). A binary
// marker ("<hex> *<filename>") is tolerated. Malformed lines are skipped.
func parseChecksums(data []byte) *verifiedChecksums {
	byFile := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		byFile[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return &verifiedChecksums{byFile: byFile}
}
