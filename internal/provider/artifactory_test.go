package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// mockArtifactory serves the storage-API shapes fglpkg relies on, for repo
// key "GeneroBDL" holding jfrog-test@{0.0.1,1.0.0} with genero6/genero7 zips.
func mockArtifactory(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Repo root (search): package folders.
	mux.HandleFunc("/api/storage/GeneroBDL", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/jfrog-test","folder":true},{"uri":"/other-lib","folder":true},{"uri":"/loose.txt","folder":false}]}`)
	})
	// Version folders for jfrog-test (includes a non-semver folder to skip).
	mux.HandleFunc("/api/storage/GeneroBDL/jfrog-test", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/0.0.1","folder":true},{"uri":"/1.0.0","folder":true},{"uri":"/latest","folder":true}]}`)
	})
	// Variant listing for 1.0.0: genero6 + genero7.
	mux.HandleFunc("/api/storage/GeneroBDL/jfrog-test/1.0.0", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[
			{"uri":"/jfrog-test-1.0.0-genero6.zip","folder":false},
			{"uri":"/jfrog-test-1.0.0-genero7.zip","folder":false},
			{"uri":"/fglpkg.json","folder":false}]}`)
	})
	// File info for the two variants.
	mux.HandleFunc("/api/storage/GeneroBDL/jfrog-test/1.0.0/jfrog-test-1.0.0-genero6.zip", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"downloadUri":"`+serverURL(r)+`/GeneroBDL/jfrog-test/1.0.0/jfrog-test-1.0.0-genero6.zip","checksums":{"sha256":"aaa6"}}`)
	})
	mux.HandleFunc("/api/storage/GeneroBDL/jfrog-test/1.0.0/jfrog-test-1.0.0-genero7.zip", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"downloadUri":"`+serverURL(r)+`/GeneroBDL/jfrog-test/1.0.0/jfrog-test-1.0.0-genero7.zip","checksums":{"sha256":"bbb7"}}`)
	})
	// Sidecar manifest.
	mux.HandleFunc("/GeneroBDL/jfrog-test/1.0.0/fglpkg.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"name":"jfrog-test","version":"1.0.0","description":"a test","genero":"^6.0.0","dependencies":{"fgl":{"logft":"^2.0.0","qrcode":{"version":"0.2.0","registry":"acme"}}}}`)
	})
	// other-lib versions (for search LatestVersion).
	mux.HandleFunc("/api/storage/GeneroBDL/other-lib", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/2.1.0","folder":true}]}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func newTestProvider(url string) *ArtifactoryProvider {
	reg := config.Registry{Name: "acme", Type: config.TypeArtifactory, URL: url, RepoKey: "GeneroBDL", Auth: config.AuthAnonymous}
	return NewArtifactoryProvider(reg, nil, nil)
}

func TestArtifactory_FetchVersions(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)
	vs, err := p.FetchVersions("jfrog-test")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	// 0.0.1 and 1.0.0 parse; "latest" is skipped.
	if len(vs) != 2 {
		t.Fatalf("want 2 versions, got %d: %+v", len(vs), vs)
	}
}

func TestArtifactory_FetchVersions_NotFound(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)
	_, err := p.FetchVersions("does-not-exist")
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("want ErrNotFound (wrapping registry.ErrNotFound), got %v", err)
	}
}

func TestArtifactory_FetchInfo_SelectsVariantAndReadsSidecar(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)

	info, err := p.FetchInfo("jfrog-test", "1.0.0", "6")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.Variant != "genero6" {
		t.Fatalf("variant = %q, want genero6", info.Variant)
	}
	if info.Checksum != "aaa6" {
		t.Fatalf("checksum = %q", info.Checksum)
	}
	if !strings.HasSuffix(info.DownloadURL, "jfrog-test-1.0.0-genero6.zip") {
		t.Fatalf("downloadURL = %q", info.DownloadURL)
	}
	if info.Source != "acme" {
		t.Fatalf("source = %q, want acme", info.Source)
	}
	if info.GeneroConstraint != "^6.0.0" {
		t.Fatalf("genero constraint = %q", info.GeneroConstraint)
	}
	if info.FGLDeps["logft"] != "^2.0.0" || info.FGLDeps["qrcode"] != "0.2.0" {
		t.Fatalf("fgl deps = %+v", info.FGLDeps)
	}
	// The object-form dep's registry pin must survive into FGLDepPins so the
	// resolver can route qrcode to acme even when the name also exists elsewhere.
	if info.FGLDepPins["qrcode"] != "acme" {
		t.Fatalf("fgl dep pins = %+v, want qrcode→acme", info.FGLDepPins)
	}
}

func TestArtifactory_FetchInfo_PicksRequestedMajor(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)
	info, err := p.FetchInfo("jfrog-test", "1.0.0", "7")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.Variant != "genero7" || info.Checksum != "bbb7" {
		t.Fatalf("wrong variant: %q %q", info.Variant, info.Checksum)
	}
}

func TestArtifactory_AuthFailureNotConflatedWithAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	p := newTestProvider(srv.URL)
	_, err := p.FetchVersions("jfrog-test")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("401 must NOT be treated as not-found: %v", err)
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected auth-failure error, got %v", err)
	}
}

func TestArtifactory_Search(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)
	results, err := p.Search("jfrog")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Name != "jfrog-test" {
		t.Fatalf("results = %+v", results)
	}
	if results[0].LatestVersion != "1.0.0" {
		t.Fatalf("latest = %q", results[0].LatestVersion)
	}
}

func TestArtifactory_AuthApplierInvoked(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeJSON(w, `{"children":[{"uri":"/1.0.0","folder":true}]}`)
	}))
	t.Cleanup(srv.Close)
	reg := config.Registry{Name: "acme", Type: config.TypeArtifactory, URL: srv.URL, RepoKey: "GeneroBDL", Auth: config.AuthBearer}
	p := NewArtifactoryProvider(reg, nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") })
	if _, err := p.FetchVersions("x"); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth header = %q", gotAuth)
	}
}
