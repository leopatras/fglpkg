// Command gen-signing-key is an OFFLINE operator tool for fglpkg release signing
// (GIS-255 R1). It is NOT part of the shipped fglpkg binary.
//
// It mirrors the Genero Intelligence repo's scripts/gen-signing-key.mjs but is
// written in Go and lives in this repo, so fglpkg's release-signing keys can be
// generated and rotated without depending on the genero-intelligence repo. It
// reuses internal/signing, so the bytes it signs are identical to what the
// fglpkg client verifies (the manifest golden vector guards this).
//
// Trust model: an offline ROOT key certifies WORKING keys by signing a keys.json
// manifest over the RFC 8785 canonicalization of {issuedAt, keys}; the release
// pipeline uses a working key to produce a detached signature over checksums.txt.
// The fglpkg client pins the root PUBLIC key (internal/signing/root.go).
//
// SECURITY: private keys are written as JSON under a git-ignored directory
// (default .private/) with mode 0600. Never commit them; the root private key
// must stay offline. Only the root PUBLIC key is pinned in the client, and only
// a working key (not the root) is ever exposed to CI.
//
// Usage:
//
//	go run ./scripts/gen-signing-key gen-root    [--id ID] [--out FILE]
//	go run ./scripts/gen-signing-key gen-working [--id ID] [--out FILE]
//	go run ./scripts/gen-signing-key sign-manifest --root FILE \
//	    --key "KEYID,PUBB64,VALIDFROM,VALIDTO" [--key ...] [--issued-at TS] [--out keys.json]
//	go run ./scripts/gen-signing-key sign-file --in FILE [--out FILE.sig] \
//	    (--key WORKINGFILE | --seed-env ENVVAR)
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// keyFile is the on-disk representation of a generated keypair. Seed is the
// PRIVATE 32-byte Ed25519 seed (base64); guard it like any private key.
type keyFile struct {
	ID   string `json:"id"`
	Alg  string `json:"alg"`
	Seed string `json:"seed"`
	Pub  string `json:"pub"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen-root":
		err = cmdGenKey(os.Args[2:], "fglpkg-release-root-1", ".private/root.key.json", true)
	case "gen-working":
		err = cmdGenKey(os.Args[2:], "fglpkg-release-"+today(), "", false)
	case "sign-manifest":
		err = cmdSignManifest(os.Args[2:])
	case "sign-file":
		err = cmdSignFile(os.Args[2:])
	case "verify-release":
		err = cmdVerifyRelease(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gen-signing-key — offline key tool for fglpkg release signing (GIS-255)

Commands:
  gen-root       Generate the offline ROOT keypair (one-time). Prints the base64
                 public key to pin in internal/signing/root.go.
  gen-working    Generate a WORKING signing keypair (its seed becomes the CI
                 signing secret; its public key goes in the manifest).
  sign-manifest  Assemble and root-sign a keys.json manifest from working keys.
  sign-file      Detached-sign a file (e.g. checksums.txt) with a working key.
  verify-release Verify a signed release (keys.json + checksums.txt + .sig)
                 against the client's pinned root — a release-pipeline gate.

Run 'go run ./scripts/gen-signing-key <command> --help' for flags.
Private keys are written under .private/ (git-ignored); never commit them.
`)
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// newFlagSet builds a FlagSet that prints usage to stderr on error.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// cmdGenKey generates a keypair and writes it to a git-ignored file. When isRoot
// is true it prints the snippet to pin the public key in the client.
func cmdGenKey(args []string, defaultID, defaultOut string, isRoot bool) error {
	fs := newFlagSet("gen-key")
	id := fs.String("id", defaultID, "key id")
	out := fs.String("out", defaultOut, "path to write the private key JSON (default .private/<id>.key.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(".private", *id+".key.json")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	kf := keyFile{
		ID:   *id,
		Alg:  "ed25519",
		Seed: base64.StdEncoding.EncodeToString(priv.Seed()),
		Pub:  base64.StdEncoding.EncodeToString(pub),
	}
	if err := writeKeyFile(*out, kf); err != nil {
		return err
	}

	fmt.Printf("Wrote private key to %s (mode 0600 — DO NOT COMMIT)\n", *out)
	fmt.Printf("  id:  %s\n", kf.ID)
	fmt.Printf("  pub: %s\n", kf.Pub)
	if isRoot {
		fmt.Print("\nPin this root in internal/signing/root.go:\n\n")
		fmt.Printf("    var pinnedRoots = []Root{\n        {KeyID: %q, PubB64: %q},\n    }\n", kf.ID, kf.Pub)
	} else {
		fmt.Print("\nNext:\n")
		fmt.Println("  - add the pub to a manifest entry via 'sign-manifest --key'")
		fmt.Println("  - set the CI signing secret to this key's seed (keep it out of git)")
	}
	return nil
}

func cmdSignManifest(args []string) error {
	fs := newFlagSet("sign-manifest")
	rootPath := fs.String("root", "", "path to the root private key JSON")
	issuedAt := fs.String("issued-at", time.Now().UTC().Format(time.RFC3339), "manifest issuedAt (RFC 3339)")
	out := fs.String("out", "keys.json", "output manifest path")
	var keyEntries multiFlag
	fs.Var(&keyEntries, "key", "working key entry \"keyid,pubB64,validFrom,validTo\" (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rootPath == "" {
		return fmt.Errorf("--root is required")
	}
	if len(keyEntries) == 0 {
		return fmt.Errorf("at least one --key is required")
	}

	_, rootKF, rootPriv, err := loadKeyFile(*rootPath)
	if err != nil {
		return fmt.Errorf("load root key: %w", err)
	}

	keys := make([]signing.Key, 0, len(keyEntries))
	for _, e := range keyEntries {
		parts := strings.Split(e, ",")
		if len(parts) != 4 {
			return fmt.Errorf("--key %q: want \"keyid,pubB64,validFrom,validTo\"", e)
		}
		keys = append(keys, signing.Key{
			KeyID:     strings.TrimSpace(parts[0]),
			Alg:       "ed25519",
			Pub:       strings.TrimSpace(parts[1]),
			ValidFrom: strings.TrimSpace(parts[2]),
			ValidTo:   strings.TrimSpace(parts[3]),
		})
	}

	m := &signing.Manifest{IssuedAt: *issuedAt, Keys: keys}
	input, err := m.SigningInput()
	if err != nil {
		return fmt.Errorf("compute signing input: %w", err)
	}
	m.Sig = signing.ManifestSig{
		RootKeyID: rootKF.ID,
		Alg:       "ed25519",
		Sig:       base64.StdEncoding.EncodeToString(ed25519.Sign(rootPriv, input)),
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("Wrote root-signed manifest to %s (%d key(s), rootKeyid %q)\n", *out, len(keys), rootKF.ID)
	return nil
}

func cmdSignFile(args []string) error {
	fs := newFlagSet("sign-file")
	keyPath := fs.String("key", "", "path to the working private key JSON")
	seedEnv := fs.String("seed-env", "", "env var holding the base64 working-key seed (for CI)")
	in := fs.String("in", "", "file to sign (e.g. checksums.txt)")
	out := fs.String("out", "", "signature output path (default <in>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("--in is required")
	}
	if *out == "" {
		*out = *in + ".sig"
	}

	var priv ed25519.PrivateKey
	switch {
	case *keyPath != "":
		var err error
		if _, _, priv, err = loadKeyFile(*keyPath); err != nil {
			return fmt.Errorf("load working key: %w", err)
		}
	case *seedEnv != "":
		seedB64 := os.Getenv(*seedEnv)
		if seedB64 == "" {
			return fmt.Errorf("env %s is empty", *seedEnv)
		}
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
		if err != nil {
			return fmt.Errorf("decode seed from %s: %w", *seedEnv, err)
		}
		if len(seed) != ed25519.SeedSize {
			return fmt.Errorf("seed from %s is %d bytes, want %d", *seedEnv, len(seed), ed25519.SeedSize)
		}
		priv = ed25519.NewKeyFromSeed(seed)
	default:
		return fmt.Errorf("one of --key or --seed-env is required")
	}

	data, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read %s: %w", *in, err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	if err := os.WriteFile(*out, []byte(sigB64+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("Wrote detached signature to %s\n", *out)
	return nil
}

// cmdVerifyRelease verifies a signed release exactly as the fglpkg client will:
// the manifest must verify against a root in signing.PinnedRoots(), and the
// detached checksums signature must verify against a working key that the
// manifest lists as valid. Intended as a release-pipeline gate so a
// misconfigured signing key cannot ship a release the client would reject.
func cmdVerifyRelease(args []string) error {
	fs := newFlagSet("verify-release")
	manifestPath := fs.String("manifest", "keys.json", "path to the root-signed keys.json")
	checksumsPath := fs.String("checksums", "", "path to checksums.txt")
	sigPath := fs.String("sig", "", "path to checksums.txt.sig")
	atStr := fs.String("at", "", "check key validity at this RFC 3339 time (default now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *checksumsPath == "" || *sigPath == "" {
		return fmt.Errorf("--checksums and --sig are required")
	}
	at := time.Now().UTC()
	if *atStr != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, *atStr); err != nil {
			return fmt.Errorf("parse --at: %w", err)
		}
	}

	mData, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	m, err := signing.ParseManifest(mData)
	if err != nil {
		return err
	}
	if err := m.Verify(signing.PinnedRoots()); err != nil {
		return fmt.Errorf("manifest does not verify against the client's pinned root: %w", err)
	}
	body, err := os.ReadFile(*checksumsPath)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return fmt.Errorf("read sig: %w", err)
	}
	valid := m.ValidKeys(at)
	if len(valid) == 0 {
		return fmt.Errorf("no working key in the manifest is valid at %s", at.Format(time.RFC3339))
	}
	for _, k := range valid {
		if signing.VerifyDetached(body, string(sig), k.Pub) == nil {
			fmt.Printf("OK: checksums signature verifies via manifest key %q, chained to pinned root %q\n", k.KeyID, m.Sig.RootKeyID)
			return nil
		}
	}
	return fmt.Errorf("checksums signature did not verify against any working key valid at %s", at.Format(time.RFC3339))
}

// writeKeyFile writes a private key JSON with mode 0600 under a dir created 0700.
func writeKeyFile(path string, kf keyFile) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// loadKeyFile reads a private key JSON and reconstructs the Ed25519 private key
// from its seed.
func loadKeyFile(path string) ([]byte, *keyFile, ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kf.Seed))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, nil, nil, fmt.Errorf("seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return data, &kf, ed25519.NewKeyFromSeed(seed), nil
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ";") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
