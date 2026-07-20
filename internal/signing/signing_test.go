package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// keyFromSeed derives a deterministic Ed25519 keypair and returns the private
// key plus its standard-base64 raw public key.
func keyFromSeed(t *testing.T, b byte) (ed25519.PrivateKey, string) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
	pub := priv.Public().(ed25519.PublicKey)
	return priv, base64.StdEncoding.EncodeToString(pub)
}

func signB64(priv ed25519.PrivateKey, msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
}

// goldenFields / goldenCanonical are the frozen cross-language vector from the
// GI repo (tests/unit/artifact-signing.test.ts). The canonical bytes MUST match
// byte-for-byte or client verification silently diverges from the signer.
var goldenFields = ArtifactFields{
	Name:       "chart-3d",
	Version:    "1.0.0",
	Variant:    "genero6",
	SHA256:     "b6e1",
	Size:       87477,
	UploadedAt: "2026-07-05T14:22:00Z",
	Uploader:   "partner:pA",
}

const goldenCanonical = `{"artifact":{"name":"chart-3d","sha256":"b6e1","size":87477,"uploaded_at":"2026-07-05T14:22:00Z","uploader":"partner:pA","variant":"genero6","version":"1.0.0"}}`

func TestCanonicalArtifactPayloadGoldenVector(t *testing.T) {
	got, err := CanonicalArtifactPayload(goldenFields)
	if err != nil {
		t.Fatalf("CanonicalArtifactPayload: %v", err)
	}
	if string(got) != goldenCanonical {
		t.Errorf("canonical mismatch\n got: %s\nwant: %s", got, goldenCanonical)
	}
}

func TestCanonicalizeSortsKeys(t *testing.T) {
	got, err := canonicalize(map[string]interface{}{"b": json.Number("1"), "a": "x", "c": true})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if want := `{"a":"x","b":1,"c":true}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeNamedEscapes(t *testing.T) {
	// The control chars with short escapes, plus " and \. (No \u case here — see
	// TestCanonicalizeControlCharUnicodeEscape.)
	in := "a\"b\\c\nd\te\bf\fg\rh"
	got, err := canonicalize(map[string]interface{}{"k": in})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	// The canonical OUTPUT holds literal backslash escapes, so want is a raw
	// string (backslash-n is two characters, not a newline).
	want := `{"k":"a\"b\\c\nd\te\bf\fg\rh"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeControlCharUnicodeEscape(t *testing.T) {
	// C0 controls without a short escape use \u00xx with LOWERCASE hex. want is
	// built with Sprintf so no literal \u escape appears in this source file.
	got, err := canonicalize(map[string]interface{}{"k": "\x01\x1f"})
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	want := `{"k":"` + fmt.Sprintf(`\u%04x\u%04x`, 0x01, 0x1f) + `"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeRejectsNonInteger(t *testing.T) {
	if _, err := canonicalize(map[string]interface{}{"n": json.Number("1.5")}); err == nil {
		t.Error("expected error for json.Number 1.5")
	}
	if _, err := canonicalize(map[string]interface{}{"n": 1.5}); err == nil {
		t.Error("expected error for float64 1.5")
	}
	if _, err := canonicalize(map[string]interface{}{"n": 2.0}); err != nil {
		t.Errorf("integral float64 2.0 should be accepted: %v", err)
	}
}

func TestVerifyArtifactRoundTrip(t *testing.T) {
	priv, pubB64 := keyFromSeed(t, 0x11)
	payload, err := CanonicalArtifactPayload(goldenFields)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	sigB64 := signB64(priv, payload)

	if err := VerifyArtifact(goldenFields, sigB64, pubB64); err != nil {
		t.Fatalf("VerifyArtifact (valid): %v", err)
	}

	// Tamper a field: signature must no longer verify.
	tampered := goldenFields
	tampered.Size = 87478
	if err := VerifyArtifact(tampered, sigB64, pubB64); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("tampered payload: got %v, want ErrSignatureMismatch", err)
	}

	// Wrong key.
	_, otherPub := keyFromSeed(t, 0x22)
	if err := VerifyArtifact(goldenFields, sigB64, otherPub); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("wrong key: got %v, want ErrSignatureMismatch", err)
	}

	// Malformed signature encoding is still a mismatch, not a panic.
	if err := VerifyArtifact(goldenFields, "!!!not-base64!!!", pubB64); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("bad sig encoding: got %v, want ErrSignatureMismatch", err)
	}
}

// signedManifest builds a manifest with one working key, signed by rootPriv.
func signedManifest(t *testing.T, rootPriv ed25519.PrivateKey, workingPubB64 string) *Manifest {
	t.Helper()
	m := &Manifest{
		IssuedAt: "2026-07-05T00:00:00Z",
		Keys: []Key{{
			KeyID:     "gi-2026-1",
			Alg:       "ed25519",
			Pub:       workingPubB64,
			ValidFrom: "2026-01-01T00:00:00Z",
			ValidTo:   "2027-01-01T00:00:00Z",
		}},
	}
	input, err := m.SigningInput()
	if err != nil {
		t.Fatalf("SigningInput: %v", err)
	}
	m.Sig = ManifestSig{RootKeyID: "root-1", Alg: "ed25519", Sig: signB64(rootPriv, input)}
	return m
}

func TestManifestVerify(t *testing.T) {
	rootPriv, rootPub := keyFromSeed(t, 0x07)
	_, workingPub := keyFromSeed(t, 0x09)
	m := signedManifest(t, rootPriv, workingPub)
	roots := []Root{{KeyID: "root-1", PubB64: rootPub}}

	if err := m.Verify(roots); err != nil {
		t.Fatalf("Verify (valid): %v", err)
	}

	// Wrong pinned root.
	_, otherRootPub := keyFromSeed(t, 0x08)
	if err := m.Verify([]Root{{KeyID: "root-1", PubB64: otherRootPub}}); !errors.Is(err, ErrManifestUnverified) {
		t.Errorf("wrong root: got %v, want ErrManifestUnverified", err)
	}

	// Unknown rootKeyid (no pinned root at all).
	if err := m.Verify(nil); !errors.Is(err, ErrManifestUnverified) {
		t.Errorf("no pinned root: got %v, want ErrManifestUnverified", err)
	}

	// Tamper issuedAt: recomputed signing input no longer matches the signature.
	tampered := *m
	tampered.IssuedAt = "2020-01-01T00:00:00Z"
	if err := tampered.Verify(roots); !errors.Is(err, ErrManifestUnverified) {
		t.Errorf("tampered issuedAt: got %v, want ErrManifestUnverified", err)
	}
}

func TestManifestSelectKey(t *testing.T) {
	rootPriv, _ := keyFromSeed(t, 0x07)
	_, workingPub := keyFromSeed(t, 0x09)
	m := signedManifest(t, rootPriv, workingPub)

	within := time.Date(2026, 7, 5, 14, 22, 0, 0, time.UTC)
	if _, err := m.SelectKey("gi-2026-1", within); err != nil {
		t.Errorf("in-window: %v", err)
	}
	after := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := m.SelectKey("gi-2026-1", after); !errors.Is(err, ErrKeyExpired) {
		t.Errorf("out-of-window: got %v, want ErrKeyExpired", err)
	}
	if _, err := m.SelectKey("no-such-key", within); !errors.Is(err, ErrKeyUnknown) {
		t.Errorf("unknown keyid: got %v, want ErrKeyUnknown", err)
	}
}

func TestManifestValidKeys(t *testing.T) {
	rootPriv, _ := keyFromSeed(t, 0x07)
	_, workingPub := keyFromSeed(t, 0x09)
	m := signedManifest(t, rootPriv, workingPub)

	if got := m.ValidKeys(time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); len(got) != 1 {
		t.Errorf("in-window: got %d valid keys, want 1", len(got))
	}
	if got := m.ValidKeys(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); len(got) != 0 {
		t.Errorf("out-of-window: got %d valid keys, want 0", len(got))
	}
}

func TestManifestRoundTripJSON(t *testing.T) {
	rootPriv, rootPub := keyFromSeed(t, 0x07)
	_, workingPub := keyFromSeed(t, 0x09)
	m := signedManifest(t, rootPriv, workingPub)

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := parsed.Verify([]Root{{KeyID: "root-1", PubB64: rootPub}}); err != nil {
		t.Errorf("verify after JSON round-trip: %v", err)
	}
}

func TestVerifyDetachedRoundTrip(t *testing.T) {
	priv, pubB64 := keyFromSeed(t, 0x33)
	data := []byte("b6e1  fglpkg-darwin-arm64\n")
	sigB64 := signB64(priv, data)

	if err := VerifyDetached(data, sigB64, pubB64); err != nil {
		t.Fatalf("VerifyDetached (valid): %v", err)
	}
	// A trailing newline on the .sig contents is tolerated.
	if err := VerifyDetached(data, sigB64+"\n", pubB64); err != nil {
		t.Errorf("trailing newline: %v", err)
	}
	// Tampered data.
	if err := VerifyDetached([]byte("deadbeef  fglpkg-darwin-arm64\n"), sigB64, pubB64); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("tampered data: got %v, want ErrSignatureMismatch", err)
	}
}

// TestPinnedRootsAreValid guards against a typo'd or truncated pinned root: each
// must have a keyid and decode to a 32-byte Ed25519 public key.
func TestPinnedRootsAreValid(t *testing.T) {
	roots := PinnedRoots()
	if len(roots) == 0 {
		t.Skip("no roots pinned yet")
	}
	seen := map[string]bool{}
	for _, r := range roots {
		if r.KeyID == "" {
			t.Error("pinned root has an empty KeyID")
		}
		if seen[r.KeyID] {
			t.Errorf("duplicate pinned root keyid %q", r.KeyID)
		}
		seen[r.KeyID] = true
		raw, err := base64.StdEncoding.DecodeString(r.PubB64)
		if err != nil {
			t.Errorf("root %q: pub is not valid standard base64: %v", r.KeyID, err)
			continue
		}
		if len(raw) != ed25519.PublicKeySize {
			t.Errorf("root %q: pub is %d bytes, want %d", r.KeyID, len(raw), ed25519.PublicKeySize)
		}
	}
}
