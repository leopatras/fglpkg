package selfupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

func b64(b []byte) string       { return base64.StdEncoding.EncodeToString(b) }
func sha256hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func keyFromSeed(seed byte) (ed25519.PrivateKey, string) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return priv, b64(priv.Public().(ed25519.PublicKey))
}

// signedManifest builds a keys.json signed by rootPriv, listing one working key.
func signedManifest(t *testing.T, rootPriv ed25519.PrivateKey, rootID, workID, workPub string) []byte {
	t.Helper()
	m := &signing.Manifest{
		IssuedAt: "2026-07-01T00:00:00Z",
		Keys:     []signing.Key{{KeyID: workID, Alg: "ed25519", Pub: workPub, ValidFrom: "2026-01-01T00:00:00Z", ValidTo: "2028-01-01T00:00:00Z"}},
	}
	input, err := m.SigningInput()
	if err != nil {
		t.Fatal(err)
	}
	m.Sig = signing.ManifestSig{RootKeyID: rootID, Alg: "ed25519", Sig: b64(ed25519.Sign(rootPriv, input))}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseChecksums(t *testing.T) {
	vc := parseChecksums([]byte("AABB  fglpkg-linux-amd64\nccdd  *fglpkg-darwin-arm64\ngarbageline\n"))
	if got, _ := vc.sha("fglpkg-linux-amd64"); got != "aabb" {
		t.Errorf("linux sha = %q, want aabb (lowercased)", got)
	}
	if got, _ := vc.sha("fglpkg-darwin-arm64"); got != "ccdd" {
		t.Errorf("darwin sha = %q, want ccdd (binary marker stripped)", got)
	}
	if _, ok := vc.sha("missing"); ok {
		t.Error("missing file should not be present")
	}
}

func TestAuthenticateChecksums(t *testing.T) {
	rootPriv, rootPub := keyFromSeed(0x01)
	workPriv, workPub := keyFromSeed(0x02)
	keysJSON := signedManifest(t, rootPriv, "root-1", "work-1", workPub)
	checksums := []byte("deadbeef  fglpkg-linux-amd64\n")
	sig := []byte(b64(ed25519.Sign(workPriv, checksums)))
	roots := []signing.Root{{KeyID: "root-1", PubB64: rootPub}}
	at := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)

	vc, err := authenticateChecksums(checksums, sig, keysJSON, roots, at)
	if err != nil {
		t.Fatalf("valid chain: %v", err)
	}
	if s, _ := vc.sha("fglpkg-linux-amd64"); s != "deadbeef" {
		t.Errorf("sha = %q, want deadbeef", s)
	}

	// Wrong pinned root -> manifest unverified.
	_, otherPub := keyFromSeed(0x09)
	if _, err := authenticateChecksums(checksums, sig, keysJSON, []signing.Root{{KeyID: "root-1", PubB64: otherPub}}, at); err == nil {
		t.Error("wrong root should fail")
	}
	// Tampered checksums -> detached signature no longer matches.
	if _, err := authenticateChecksums([]byte("00000000  fglpkg-linux-amd64\n"), sig, keysJSON, roots, at); err == nil {
		t.Error("tampered checksums should fail")
	}
	// Key outside its validity window.
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := authenticateChecksums(checksums, sig, keysJSON, roots, future); err == nil {
		t.Error("expired key window should fail")
	}
}

func TestAtomicReplaceAndMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix rename semantics")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "fglpkg")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".staged")
	if err := os.WriteFile(staged, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyMode(staged, target); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(staged); fi.Mode().Perm()&0o111 == 0 {
		t.Error("applyMode should set the execute bit")
	}
	if err := atomicReplace(target, staged); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW" {
		t.Errorf("target content = %q, want NEW", got)
	}
}

func TestManagedBy(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Cellar/fglpkg/3.8.0/bin/fglpkg": "Homebrew",
		"/home/linuxbrew/.linuxbrew/bin/fglpkg":        "Homebrew",
		"/usr/local/bin/fglpkg":                        "",
		"/home/me/bin/fglpkg":                          "",
	}
	for path, want := range cases {
		if got := managedBy(path); got != want {
			t.Errorf("managedBy(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestVersionNewer(t *testing.T) {
	if !versionNewer("3.8.0", "3.9.0") {
		t.Error("3.9.0 should be newer than 3.8.0")
	}
	if versionNewer("3.9.0", "3.9.0") {
		t.Error("equal versions are not newer")
	}
	if versionNewer("3.9.0", "3.8.0") {
		t.Error("older is not newer")
	}
	if versionNewer("dev", "3.9.0") {
		t.Error("unparseable current should be treated as not-newer")
	}
}

func TestRecoveryErr(t *testing.T) {
	lr := &registry.LatestRelease{ManualURL: "https://m.example/dl", Instructions: "do it by hand"}
	msg := recoveryErr(lr, "something broke").Error()
	for _, want := range []string{"something broke", "https://m.example/dl", "do it by hand"} {
		if !strings.Contains(msg, want) {
			t.Errorf("recovery error missing %q in: %q", want, msg)
		}
	}
}

// releaseServer serves a mock latest-release endpoint plus its assets.
func releaseServer(t *testing.T, version string, binary, checksums, sig, keysJSON []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	assetName := "fglpkg-" + runtime.GOOS + "-" + runtime.GOARCH
	srv := httptest.NewServer(mux)
	base = srv.URL
	mux.HandleFunc("/registry/fglpkg/latest", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"version":         version,
			"checksumsUrl":    base + "/checksums.txt",
			"checksumsSigUrl": base + "/checksums.txt.sig",
			"keysUrl":         base + "/keys.json",
			"manualUrl":       "https://manual.example/download",
			"instructions":    "Grab the binary and replace it by hand.",
			"assets":          []map[string]string{{"os": runtime.GOOS, "arch": runtime.GOARCH, "url": base + "/download/" + assetName}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(checksums) })
	mux.HandleFunc("/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/keys.json", func(w http.ResponseWriter, r *http.Request) { w.Write(keysJSON) })
	mux.HandleFunc("/download/"+assetName, func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	return srv
}

func TestRunUpdatesBinary(t *testing.T) {
	rootPriv, rootPub := keyFromSeed(0x01)
	workPriv, workPub := keyFromSeed(0x02)
	keysJSON := signedManifest(t, rootPriv, "root-1", "work-1", workPub)

	binary := []byte("FAKE-FGLPKG-BINARY-v3.9.0")
	assetName := "fglpkg-" + runtime.GOOS + "-" + runtime.GOARCH
	checksums := []byte(fmt.Sprintf("%s  %s\n", sha256hex(binary), assetName))
	sig := []byte(b64(ed25519.Sign(workPriv, checksums)))

	srv := releaseServer(t, "3.9.0", binary, checksums, sig, keysJSON)
	defer srv.Close()
	t.Setenv("FGLPKG_REGISTRY", srv.URL)

	dir := t.TempDir()
	exe := filepath.Join(dir, "fglpkg")
	if err := os.WriteFile(exe, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()

	var out bytes.Buffer
	err := Run(Options{
		Current: "3.8.0", Yes: true, Stdout: &out, HomeForCache: home,
		exePath: exe,
		roots:   []signing.Root{{KeyID: "root-1", PubB64: rootPub}},
		now:     time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if !bytes.Equal(got, binary) {
		t.Errorf("binary not swapped: got %q", got)
	}
	if !strings.Contains(out.String(), "v3.8.0 → v3.9.0") {
		t.Errorf("missing success line: %q", out.String())
	}
}

func TestRunAbortsOnBadRoot(t *testing.T) {
	rootPriv, _ := keyFromSeed(0x01)
	workPriv, workPub := keyFromSeed(0x02)
	keysJSON := signedManifest(t, rootPriv, "root-1", "work-1", workPub)
	binary := []byte("FAKE")
	assetName := "fglpkg-" + runtime.GOOS + "-" + runtime.GOARCH
	checksums := []byte(fmt.Sprintf("%s  %s\n", sha256hex(binary), assetName))
	sig := []byte(b64(ed25519.Sign(workPriv, checksums)))
	srv := releaseServer(t, "3.9.0", binary, checksums, sig, keysJSON)
	defer srv.Close()
	t.Setenv("FGLPKG_REGISTRY", srv.URL)

	dir := t.TempDir()
	exe := filepath.Join(dir, "fglpkg")
	os.WriteFile(exe, []byte("OLD"), 0o755)

	_, wrongRoot := keyFromSeed(0x099)
	err := Run(Options{
		Current: "3.8.0", Yes: true, Stdout: &bytes.Buffer{},
		exePath: exe,
		roots:   []signing.Root{{KeyID: "root-1", PubB64: wrongRoot}},
		now:     time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected an error when the manifest is not signed by a pinned root")
	}
	if !strings.Contains(err.Error(), "manual.example") {
		t.Errorf("abort error should carry the recovery URL, got: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "OLD" {
		t.Errorf("binary must be untouched on abort, got %q", got)
	}
}

func TestRunUpToDateAndCheck(t *testing.T) {
	rootPriv, rootPub := keyFromSeed(0x01)
	_, workPub := keyFromSeed(0x02)
	keysJSON := signedManifest(t, rootPriv, "root-1", "work-1", workPub)
	srv := releaseServer(t, "3.8.0", nil, nil, nil, keysJSON)
	defer srv.Close()
	t.Setenv("FGLPKG_REGISTRY", srv.URL)
	dir := t.TempDir()
	exe := filepath.Join(dir, "fglpkg")
	os.WriteFile(exe, []byte("SAME"), 0o755)
	roots := []signing.Root{{KeyID: "root-1", PubB64: rootPub}}

	var out bytes.Buffer
	if err := Run(Options{Current: "3.8.0", Yes: true, Stdout: &out, exePath: exe, roots: roots, now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("up-to-date Run: %v", err)
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Errorf("expected up-to-date message, got %q", out.String())
	}

	out.Reset()
	if err := Run(Options{Current: "3.7.0", Check: true, Stdout: &out, exePath: exe, roots: roots, now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("--check Run: %v", err)
	}
	if !strings.Contains(out.String(), "3.7.0 → 3.8.0") {
		t.Errorf("--check should report availability, got %q", out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "SAME" {
		t.Errorf("--check must not modify the binary, got %q", got)
	}
}

func TestRunRefusesDevBuild(t *testing.T) {
	if err := Run(Options{Current: "dev", Stdout: &bytes.Buffer{}}); err == nil {
		t.Error("dev build must refuse self-update")
	}
}
