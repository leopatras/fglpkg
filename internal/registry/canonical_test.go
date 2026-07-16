package registry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// TestFetchCanonicalizesNameInURL verifies a non-canonical package name is
// normalized to its slug before the registry URL is built (GIS-271): "foo_bar"
// must be fetched from /registry/packages/foo-bar.
func TestFetchCanonicalizesNameInURL(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug": "foo-bar",
			"versions": []map[string]any{
				{"version": "1.0.0", "artifacts": []map[string]any{
					{"variant": "default", "sha256": "aa", "download_url": "https://r2/x.zip"},
				}},
			},
		})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if _, err := registry.FetchVersionList("foo_bar"); err != nil {
		t.Fatalf("FetchVersionList: %v", err)
	}
	if gotPath != "/registry/packages/foo-bar" {
		t.Errorf("request path = %q, want /registry/packages/foo-bar", gotPath)
	}
}
