package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// detailStubServer responds to GET /registry/packages/:slug with the
// supplied per-slug version-and-variant map. Unknown slugs produce a 404.
//
// versions map: slug → version → []variants.
func detailStubServer(t *testing.T, versions map[string]map[string][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expect: /registry/packages/<slug>
		const prefix = "/registry/packages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		slug := strings.TrimPrefix(r.URL.Path, prefix)
		// Reject the empty trailing path (browse endpoint, not detail).
		if slug == "" || strings.Contains(slug, "/") {
			http.NotFound(w, r)
			return
		}
		byVersion, ok := versions[slug]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var versionsArr []map[string]any
		for version, variants := range byVersion {
			arts := make([]map[string]any, 0, len(variants))
			for _, v := range variants {
				arts = append(arts, map[string]any{
					"variant":      v,
					"sha256":       "abc",
					"download_url": "/dl/" + slug + "/" + version + "/" + v,
				})
			}
			versionsArr = append(versionsArr, map[string]any{
				"version":   version,
				"artifacts": arts,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":     slug,
			"versions": versionsArr,
		})
	}))
}

func TestCheckVariantNotPublishedFirstPublish(t *testing.T) {
	ts := detailStubServer(t, nil) // every slug → 404
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	m := manifest.New("brand-new", "0.1.0", "", "")
	if err := checkVariantNotPublished(m, "6"); err != nil {
		t.Errorf("expected nil for first publish, got %v", err)
	}
}

func TestCheckVariantNotPublishedSameVariantBlocks(t *testing.T) {
	ts := detailStubServer(t, map[string]map[string][]string{
		"demo": {"1.0.2": {"genero6"}},
	})
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	m := manifest.New("demo", "1.0.2", "", "")
	err := checkVariantNotPublished(m, "6")
	if err == nil {
		t.Fatal("expected error when same variant already published")
	}
	if !strings.Contains(err.Error(), "Genero 6") {
		t.Errorf("err = %v, want one mentioning 'Genero 6'", err)
	}
	if !strings.Contains(err.Error(), "fglpkg version") {
		t.Errorf("err = %v, want guidance pointing at `fglpkg version`", err)
	}
}

// The regression Laurent hit in SUPNA-10506: publishing a Genero 5 variant
// of an existing-on-Genero-6 version used to be blocked. With the new check
// (and the new registry's variants-per-version view), it succeeds.
func TestCheckVariantNotPublishedNewVariantAllowed(t *testing.T) {
	ts := detailStubServer(t, map[string]map[string][]string{
		"genero-crypto-api": {"1.0.2": {"genero6"}},
	})
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	m := manifest.New("genero-crypto-api", "1.0.2", "", "")
	if err := checkVariantNotPublished(m, "5"); err != nil {
		t.Errorf("expected nil when adding new variant to existing version, got %v", err)
	}
}

func TestCheckVariantNotPublishedDifferentVersion(t *testing.T) {
	ts := detailStubServer(t, map[string]map[string][]string{
		"demo": {"1.0.0": {"genero6"}, "1.1.0": {"genero6"}},
	})
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	m := manifest.New("demo", "2.0.0", "", "")
	if err := checkVariantNotPublished(m, "6"); err != nil {
		t.Errorf("expected nil when bumping past existing versions, got %v", err)
	}
}

func TestCheckVariantNotPublishedRegistryDown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	m := manifest.New("demo", "1.0.0", "", "")
	err := checkVariantNotPublished(m, "6")
	if err == nil {
		t.Fatal("expected error when registry is unreachable")
	}
	if !strings.Contains(err.Error(), "cannot check") {
		t.Errorf("err = %v, want one starting with 'cannot check'", err)
	}
}

// TestFetchVersionListWrapsErrNotFound verifies the sentinel survives
// the fmt.Errorf("...: %w", ...) wrapping inside FetchVersionList so
// callers can use errors.Is.
func TestFetchVersionListWrapsErrNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	_, err := registry.FetchVersionList("anything")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false; err = %v", err)
	}
}
