package signing

import (
	"errors"
	"testing"
)

// consumerManifest builds an (unsigned) manifest with a single working key for
// exercising the consumer VerifyArtifact path, which consults only m.Keys.
func consumerManifest(keyid, pub, from, to string) *Manifest {
	return &Manifest{
		IssuedAt: "2026-07-06T00:00:00Z",
		Keys:     []Key{{KeyID: keyid, Alg: "ed25519", Pub: pub, ValidFrom: from, ValidTo: to}},
	}
}

// withPinnedRoot temporarily prepends rk to the pinned roots for one test, so a
// manifest signed by a test root verifies. Restored on cleanup.
func withPinnedRoot(t *testing.T, rk Root) {
	t.Helper()
	orig := pinnedRoots
	pinnedRoots = append([]Root{rk}, orig...)
	t.Cleanup(func() { pinnedRoots = orig })
}

// The consumer VerifyArtifact accepts the SQLite "YYYY-MM-DD HH:MM:SS" upload
// timestamp the registry stores, not just RFC 3339 — this is the whole point of
// the tolerant parseTimestamp.
func TestConsumerVerifyArtifactHappyPathSQLiteTimestamp(t *testing.T) {
	priv, pub := keyFromSeed(t, 0x11)
	m := consumerManifest("gi-2026-1", pub, "2026-07-01T00:00:00Z", "2027-07-01T00:00:00Z")

	f := ArtifactFields{
		Name: "qrcode", Version: "1.0.0", Variant: "genero6",
		SHA256: "deadbeef", Size: 512,
		UploadedAt: "2026-07-02 14:22:00", // SQLite datetime form, not RFC3339
		Uploader:   "partner:x",
	}
	payload, err := CanonicalArtifactPayload(f)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	sig := ArtifactSignature{KeyID: "gi-2026-1", Alg: "ed25519", Sig: signB64(priv, payload)}

	if err := m.VerifyArtifact(f, sig); err != nil {
		t.Fatalf("expected verify to pass, got %v", err)
	}
}

func TestConsumerVerifyArtifactTampered(t *testing.T) {
	priv, pub := keyFromSeed(t, 0x22)
	m := consumerManifest("k", pub, "2026-01-01T00:00:00Z", "2030-01-01T00:00:00Z")
	f := ArtifactFields{Name: "p", Version: "1.0.0", Variant: "genero6",
		SHA256: "aaaa", Size: 10, UploadedAt: "2026-07-02 14:22:00", Uploader: "partner:x"}
	payload, _ := CanonicalArtifactPayload(f)
	sig := ArtifactSignature{KeyID: "k", Sig: signB64(priv, payload)}

	f.Size = 11 // tamper after signing
	if err := m.VerifyArtifact(f, sig); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestConsumerVerifyArtifactUnknownKey(t *testing.T) {
	priv, pub := keyFromSeed(t, 0x33)
	m := consumerManifest("k1", pub, "2026-01-01T00:00:00Z", "2030-01-01T00:00:00Z")
	f := ArtifactFields{Name: "p", Version: "1", Variant: "v", SHA256: "x", Size: 1,
		UploadedAt: "2026-07-02 14:22:00"}
	payload, _ := CanonicalArtifactPayload(f)
	sig := ArtifactSignature{KeyID: "k2", Sig: signB64(priv, payload)}
	if err := m.VerifyArtifact(f, sig); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("expected ErrKeyUnknown, got %v", err)
	}
}

func TestConsumerVerifyArtifactExpiredWindow(t *testing.T) {
	priv, pub := keyFromSeed(t, 0x44)
	m := consumerManifest("k", pub, "2026-07-10T00:00:00Z", "2027-07-10T00:00:00Z")
	f := ArtifactFields{Name: "p", Version: "1", Variant: "v", SHA256: "x", Size: 1,
		UploadedAt: "2026-07-02 14:22:00"} // before validFrom
	payload, _ := CanonicalArtifactPayload(f)
	sig := ArtifactSignature{KeyID: "k", Sig: signB64(priv, payload)}
	if err := m.VerifyArtifact(f, sig); !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("expected ErrKeyExpired, got %v", err)
	}
}

func TestKeyByID(t *testing.T) {
	m := consumerManifest("k", "pub", "2026-01-01T00:00:00Z", "2030-01-01T00:00:00Z")
	if _, ok := m.KeyByID("k"); !ok {
		t.Error("expected to find key k")
	}
	if _, ok := m.KeyByID("nope"); ok {
		t.Error("did not expect to find key nope")
	}
}
