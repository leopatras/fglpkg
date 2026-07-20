package signing

import (
	"encoding/json"
	"errors"
	"testing"
)

// rawSignedManifest builds keys.json bytes signed by a test root, plus the Root
// entry the caller must pin for verification to succeed.
func rawSignedManifest(t *testing.T, rootSeed, workingSeed byte) (raw []byte, root Root) {
	t.Helper()
	rootPriv, rootPub := keyFromSeed(t, rootSeed)
	_, workingPub := keyFromSeed(t, workingSeed)
	m := &Manifest{
		IssuedAt: "2026-07-05T00:00:00Z",
		Keys: []Key{{KeyID: "gi-2026-1", Alg: "ed25519", Pub: workingPub,
			ValidFrom: "2026-01-01T00:00:00Z", ValidTo: "2027-01-01T00:00:00Z"}},
	}
	input, err := m.SigningInput()
	if err != nil {
		t.Fatal(err)
	}
	m.Sig = ManifestSig{RootKeyID: "root-test", Alg: "ed25519", Sig: signB64(rootPriv, input)}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw, Root{KeyID: "root-test", PubB64: rootPub}
}

func TestLoadManifestFetchesThenServesFromCache(t *testing.T) {
	raw, root := rawSignedManifest(t, 0x07, 0x09)
	withPinnedRoot(t, root)

	calls := 0
	orig := httpGet
	httpGet = func(url string) ([]byte, int, error) {
		calls++
		return raw, 3600, nil
	}
	t.Cleanup(func() { httpGet = orig })

	home := t.TempDir()
	m, err := LoadManifest(home, "https://registry.example")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if _, ok := m.KeyByID("gi-2026-1"); !ok {
		t.Fatal("working key missing")
	}
	// A second load within the cache lifetime must not hit the network again.
	if _, err := LoadManifest(home, "https://registry.example"); err != nil {
		t.Fatalf("second LoadManifest: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 network fetch then cache hit, got %d", calls)
	}
}

func TestLoadManifestOfflineFallbackToStaleCache(t *testing.T) {
	raw, root := rawSignedManifest(t, 0x0a, 0x0b)
	withPinnedRoot(t, root)

	home := t.TempDir()
	writeCache(home, raw, 0) // maxAge 0 => immediately stale

	orig := httpGet
	httpGet = func(url string) ([]byte, int, error) {
		return nil, 0, errors.New("network down")
	}
	t.Cleanup(func() { httpGet = orig })

	m, err := LoadManifest(home, "https://registry.example")
	if err != nil {
		t.Fatalf("expected offline fallback to stale cache, got %v", err)
	}
	if _, ok := m.KeyByID("gi-2026-1"); !ok {
		t.Fatal("working key missing from offline fallback")
	}
}

func TestLoadManifestRejectsUntrustedRoot(t *testing.T) {
	raw, _ := rawSignedManifest(t, 0x0c, 0x0d) // deliberately do NOT pin the root

	orig := httpGet
	httpGet = func(url string) ([]byte, int, error) { return raw, 3600, nil }
	t.Cleanup(func() { httpGet = orig })

	home := t.TempDir()
	if _, err := LoadManifest(home, "https://registry.example"); !errors.Is(err, ErrManifestUnverified) {
		t.Fatalf("expected ErrManifestUnverified for untrusted root, got %v", err)
	}
}

func TestParseMaxAge(t *testing.T) {
	cases := map[string]int{
		"max-age=600":             600,
		"public, max-age=3600":    3600,
		"no-cache":                0,
		"":                        0,
		"max-age=notanumber":      0,
		"s-maxage=10, max-age=42": 42,
	}
	for header, want := range cases {
		if got := parseMaxAge(header); got != want {
			t.Errorf("parseMaxAge(%q) = %d, want %d", header, got, want)
		}
	}
}
