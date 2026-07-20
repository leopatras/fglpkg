package registry_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

func TestFetchLatestFGLPkg(t *testing.T) {
	const body = `{
	  "version": "3.9.0",
	  "notes": "https://example/releases/tag/v3.9.0",
	  "checksumsUrl": "https://example/releases/download/v3.9.0/checksums.txt",
	  "checksumsSigUrl": "https://example/releases/download/v3.9.0/checksums.txt.sig",
	  "manualUrl": "https://example/releases/tag/v3.9.0",
	  "instructions": "Download and replace your fglpkg binary.",
	  "assets": [
	    {"os": "darwin", "arch": "arm64", "url": "https://example/releases/download/v3.9.0/fglpkg-darwin-arm64"},
	    {"os": "linux",  "arch": "amd64", "url": "https://example/releases/download/v3.9.0/fglpkg-linux-amd64"}
	  ]
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/fglpkg/latest" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	lr, err := registry.FetchLatestFGLPkg()
	if err != nil {
		t.Fatalf("FetchLatestFGLPkg: %v", err)
	}
	if lr.Version != "3.9.0" {
		t.Errorf("version = %q, want 3.9.0", lr.Version)
	}
	if a := lr.AssetFor("darwin", "arm64"); a == nil || a.URL == "" {
		t.Errorf("AssetFor(darwin/arm64) = %v, want a URL", a)
	}
	if a := lr.AssetFor("windows", "arm64"); a != nil {
		t.Errorf("AssetFor(windows/arm64) = %v, want nil", a)
	}
	// keys.json is derived from checksumsUrl's directory when no keysUrl is set.
	if got, want := lr.KeysManifestURL(), "https://example/releases/download/v3.9.0/keys.json"; got != want {
		t.Errorf("KeysManifestURL() = %q, want %q", got, want)
	}
}

func TestKeysManifestURLPrefersExplicit(t *testing.T) {
	lr := &registry.LatestRelease{
		ChecksumsURL: "https://example/v1/checksums.txt",
		KeysURL:      "https://cdn.example/keys.json",
	}
	if got := lr.KeysManifestURL(); got != "https://cdn.example/keys.json" {
		t.Errorf("KeysManifestURL() = %q, want the explicit keysUrl", got)
	}
}

func TestFetchLatestFGLPkgNotFound(t *testing.T) {
	// A registry that predates the endpoint returns 404 -> ErrNotFound, which
	// callers treat as "no update info".
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if _, err := registry.FetchLatestFGLPkg(); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}
