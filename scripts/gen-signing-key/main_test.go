package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// TestKeygenRoundTrip drives the tool end to end — generate a root and a working
// key, root-sign a manifest, detached-sign a file — then verifies both with
// internal/signing, proving the signer (this tool) and the client verifier agree.
func TestKeygenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "root.key.json")
	workPath := filepath.Join(dir, "work.key.json")
	manifestPath := filepath.Join(dir, "keys.json")
	checksums := filepath.Join(dir, "checksums.txt")
	sigPath := checksums + ".sig"

	if err := cmdGenKey([]string{"--id", "test-root-1", "--out", rootPath}, "", "", true); err != nil {
		t.Fatalf("gen-root: %v", err)
	}
	if err := cmdGenKey([]string{"--id", "test-work-1", "--out", workPath}, "", "", false); err != nil {
		t.Fatalf("gen-working: %v", err)
	}

	_, rootKF, _, err := loadKeyFile(rootPath)
	if err != nil {
		t.Fatalf("load root: %v", err)
	}
	_, workKF, _, err := loadKeyFile(workPath)
	if err != nil {
		t.Fatalf("load working: %v", err)
	}

	const from, to = "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z"
	keyArg := strings.Join([]string{workKF.ID, workKF.Pub, from, to}, ",")
	if err := cmdSignManifest([]string{
		"--root", rootPath, "--key", keyArg,
		"--issued-at", "2026-07-05T00:00:00Z", "--out", manifestPath,
	}); err != nil {
		t.Fatalf("sign-manifest: %v", err)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := signing.ParseManifest(data)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	roots := []signing.Root{{KeyID: rootKF.ID, PubB64: rootKF.Pub}}
	if err := m.Verify(roots); err != nil {
		t.Fatalf("manifest verify against generated root: %v", err)
	}
	if _, err := m.SelectKey(workKF.ID, time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("select working key: %v", err)
	}

	// A manifest signed by this root must NOT verify against a different root.
	otherRootPath := filepath.Join(dir, "other-root.key.json")
	if err := cmdGenKey([]string{"--id", "test-root-2", "--out", otherRootPath}, "", "", true); err != nil {
		t.Fatal(err)
	}
	_, otherRoot, _, _ := loadKeyFile(otherRootPath)
	if err := m.Verify([]signing.Root{{KeyID: rootKF.ID, PubB64: otherRoot.Pub}}); err == nil {
		t.Error("manifest verified against the wrong root public key")
	}

	// Detached sign a file, then verify it with the working public key.
	if err := os.WriteFile(checksums, []byte("b6e1  fglpkg-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdSignFile([]string{"--key", workPath, "--in", checksums, "--out", sigPath}); err != nil {
		t.Fatalf("sign-file: %v", err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(checksums)
	if err != nil {
		t.Fatal(err)
	}
	if err := signing.VerifyDetached(body, string(sig), workKF.Pub); err != nil {
		t.Fatalf("detached verify: %v", err)
	}

	// The release gate must REJECT this manifest: it was signed by an ad-hoc test
	// root, not the client's pinned release root, so a client would reject it too.
	if err := cmdVerifyRelease([]string{
		"--manifest", manifestPath, "--checksums", checksums, "--sig", sigPath,
		"--at", "2026-07-05T00:00:00Z",
	}); err == nil {
		t.Error("verify-release accepted a manifest not signed by a pinned root")
	}
}
