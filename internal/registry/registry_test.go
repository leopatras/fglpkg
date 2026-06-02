package registry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// newPackagesServer responds to GET /registry/packages and
// GET /registry/packages/<slug> with shape matching the cli protocol.
func newPackagesServer(t *testing.T, detail map[string]any, browse map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/registry/packages/"):
			_ = json.NewEncoder(w).Encode(detail)
		case r.URL.Path == "/registry/packages":
			_ = json.NewEncoder(w).Encode(browse)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestFetchVersionListProjectsVersions(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug": "demo-utils",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "aa", "download_url": "https://r2/x.zip"},
			}},
			{"version": "1.1.0", "artifacts": []map[string]any{
				{"variant": "genero6", "sha256": "bb", "download_url": "https://r2/y.zip"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	vl, err := registry.FetchVersionList("demo-utils")
	if err != nil {
		t.Fatalf("FetchVersionList: %v", err)
	}
	if got := vl.Versions; len(got) != 2 || got[0] != "1.0.0" || got[1] != "1.1.0" {
		t.Errorf("Versions = %v, want [1.0.0 1.1.0]", got)
	}
}

func TestFetchInfoForGeneroPicksMatchingVariant(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug": "demo-utils",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "aa", "download_url": "https://r2/default.zip"},
				{"variant": "genero6", "sha256": "bb", "download_url": "https://r2/g6.zip"},
				{"variant": "genero7", "sha256": "cc", "download_url": "https://r2/g7.zip"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	info, err := registry.FetchInfoForGenero("demo-utils", "1.0.0", "6")
	if err != nil {
		t.Fatalf("FetchInfoForGenero: %v", err)
	}
	if info.DownloadURL != "https://r2/g6.zip" {
		t.Errorf("DownloadURL = %q, want g6.zip", info.DownloadURL)
	}
	if info.Checksum != "bb" {
		t.Errorf("Checksum = %q, want bb", info.Checksum)
	}
}

func TestFetchInfoForGeneroFallsBackToDefault(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug": "demo-utils",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "aa", "download_url": "https://r2/default.zip"},
				{"variant": "genero7", "sha256": "cc", "download_url": "https://r2/g7.zip"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	// Ask for genero6 but only default + genero7 exist → default.
	info, err := registry.FetchInfoForGenero("demo-utils", "1.0.0", "6")
	if err != nil {
		t.Fatalf("FetchInfoForGenero: %v", err)
	}
	if info.DownloadURL != "https://r2/default.zip" {
		t.Errorf("DownloadURL = %q, want default.zip (fallback)", info.DownloadURL)
	}
}

func TestSearchMapsBrowseResponse(t *testing.T) {
	ts := newPackagesServer(t, nil, map[string]any{
		"packages": []map[string]any{
			{
				"slug":           "hello-genero",
				"name":           "hello-genero",
				"description":    "A friendly starter",
				"latest_version": "1.2.3",
				"owner":          map[string]any{"name": "ACME"},
			},
		},
		"total": 1,
	})
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	results, err := registry.Search("hello")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Name != "hello-genero" || r.LatestVersion != "1.2.3" || r.Author != "ACME" {
		t.Errorf("result = %+v, fields wrong", r)
	}
}

func TestFetchVersionListMissingPackage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	_, err := registry.FetchVersionList("nope")
	if err == nil {
		t.Fatal("expected ErrNotFound on 404")
	}
}

func TestBearerHookSendsAuthorizationHeader(t *testing.T) {
	gotAuth := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":     "demo",
			"versions": []map[string]any{},
		})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	prev := registry.Bearer
	t.Cleanup(func() { registry.Bearer = prev })
	registry.Bearer = func() string { return "test-bearer" }

	if _, err := registry.FetchVersionList("demo"); err != nil {
		t.Fatalf("FetchVersionList: %v", err)
	}
	if gotAuth != "Bearer test-bearer" {
		t.Errorf("Authorization = %q, want Bearer test-bearer", gotAuth)
	}
}

func TestTryRefreshTriggeredOn401(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": "demo", "versions": []map[string]any{}})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	// First call: stale bearer; refresh succeeds; second call: fresh.
	bearer := "stale"
	prevB := registry.Bearer
	prevR := registry.TryRefresh
	t.Cleanup(func() {
		registry.Bearer = prevB
		registry.TryRefresh = prevR
	})
	registry.Bearer = func() string { return bearer }
	refreshed := false
	registry.TryRefresh = func() bool {
		bearer = "fresh"
		refreshed = true
		return true
	}

	if _, err := registry.FetchVersionList("demo"); err != nil {
		t.Fatalf("FetchVersionList: %v", err)
	}
	if !refreshed {
		t.Error("TryRefresh was not called on 401")
	}
	if calls != 2 {
		t.Errorf("server saw %d requests, want 2 (initial + retry after refresh)", calls)
	}
}

// PublisherVersionList now talks to the LEGACY (fly.dev) base unconditionally
// — env override was dropped because the legacy URL is the only place the old
// /packages/<name>/versions endpoint exists. Tests swap registry.LegacyBase to
// a httptest URL.
func TestPublisherVersionListUsesLegacyBase(t *testing.T) {
	gotPath := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":     "demo",
			"versions": []string{"1.0.0"},
		})
	}))
	defer ts.Close()
	prev := registry.LegacyBase
	registry.LegacyBase = ts.URL
	t.Cleanup(func() { registry.LegacyBase = prev })

	vl, err := registry.PublisherVersionList("demo")
	if err != nil {
		t.Fatalf("PublisherVersionList: %v", err)
	}
	if gotPath != "/packages/demo/versions" {
		t.Errorf("path = %q, want /packages/demo/versions", gotPath)
	}
	if len(vl.Versions) != 1 || vl.Versions[0] != "1.0.0" {
		t.Errorf("Versions = %v, want [1.0.0]", vl.Versions)
	}
}
