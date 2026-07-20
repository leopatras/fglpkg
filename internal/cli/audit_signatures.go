package cli

import (
	"fmt"
	"os"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// cmdAuditSignatures implements `fglpkg audit signatures`: it walks the lock
// file and re-verifies the Layer 1 registry signature of every entry against
// the current keys manifest, printing one line per package and exiting
// non-zero if anything is missing or fails to verify (for CI use).
//
// Exit codes mirror `fglpkg audit`:
//
//	0  every locked package has a valid signature
//	1  at least one package is unsigned or fails verification
//	2  the audit itself failed (missing lockfile, unverifiable manifest, …)
func cmdAuditSignatures(args []string) error {
	if len(args) > 0 {
		return &ExitError{Code: 2, Err: fmt.Errorf("unknown argument %q", args[0])}
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("cannot determine working directory: %w", err)}
	}
	if !lockfile.Exists(projectDir) {
		return &ExitError{Code: 2, Err: fmt.Errorf("no %s in current directory; run `fglpkg install` first", lockfile.Filename)}
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)}
	}

	globalHome, err := fglpkgHome()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	m, err := signing.LoadManifest(globalHome, defaultRegistry())
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("cannot load keys manifest: %w", err)}
	}

	failures := 0
	total := 0

	for _, p := range lf.Packages {
		total++
		variant := p.GeneroMajor
		if variant != "" {
			variant = "genero" + variant
		}
		if !auditOne(m, p.Name, p.Version, variant, p.Checksum, p.Size,
			p.UploadedAt, p.Uploader, p.SignatureKeyID, p.Signature) {
			failures++
		}
	}
	for _, w := range lf.Webcomponents {
		total++
		if !auditOne(m, w.Name, w.Version, "webcomponent", w.Checksum, w.Size,
			w.UploadedAt, w.Uploader, w.SignatureKeyID, w.Signature) {
			failures++
		}
	}

	if total == 0 {
		fmt.Println("No packages in the lock file to audit.")
		return nil
	}
	if failures > 0 {
		return &ExitError{Code: 1, Err: fmt.Errorf(
			"%d of %d package%s failed signature verification", failures, total, pluralS(total))}
	}
	fmt.Printf("\nAll %d package signature%s verified.\n", total, pluralS(total))
	return nil
}

// auditOne verifies a single locked entry and prints its result line. Returns
// true when the signature verifies.
func auditOne(m *signing.Manifest, name, version, variant, sha256 string, size int64,
	uploadedAt, uploader, keyid, sig string) bool {

	label := fmt.Sprintf("%s@%s (%s)", name, version, variant)
	if keyid == "" && sig == "" {
		fmt.Printf("✗ %-40s ERROR: signature missing\n", label)
		return false
	}
	p := signing.ArtifactFields{
		Name: name, Version: version, Variant: variant,
		SHA256: sha256, Size: size, UploadedAt: uploadedAt, Uploader: uploader,
	}
	err := m.VerifyArtifact(p, signing.ArtifactSignature{KeyID: keyid, Alg: "ed25519", Sig: sig})
	if err != nil {
		fmt.Printf("✗ %-40s ERROR: %v\n", label, err)
		return false
	}
	fmt.Printf("✓ %-40s keyid=%s\n", label, keyid)
	return true
}
