package registry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
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

// TestFetchInfoForGeneroResolvesRelativeDownloadURL verifies that a
// site-relative download_url (what the registry actually returns) is made
// absolute against the consumer base, so the installer's GET has a scheme +
// host instead of failing with "unsupported protocol scheme".
func TestFetchInfoForGeneroResolvesRelativeDownloadURL(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug": "qrcode",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "genero6", "sha256": "bb",
					"download_url": "/registry/packages/qrcode/versions/1.0.0/artifacts/genero6"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	info, err := registry.FetchInfoForGenero("qrcode", "1.0.0", "6")
	if err != nil {
		t.Fatalf("FetchInfoForGenero: %v", err)
	}
	want := ts.URL + "/registry/packages/qrcode/versions/1.0.0/artifacts/genero6"
	if info.DownloadURL != want {
		t.Errorf("DownloadURL = %q, want %q", info.DownloadURL, want)
	}
	if len(info.Variants) != 1 || info.Variants[0].DownloadURL != want {
		t.Errorf("Variants[0].DownloadURL = %+v, want %q", info.Variants, want)
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

// A package-level deprecation on the browse listing must surface on the
// SearchResult so `search` can flag it inline without a detail fetch.
func TestSearchSurfacesDeprecation(t *testing.T) {
	ts := newPackagesServer(t, nil, map[string]any{
		"packages": []map[string]any{
			{
				"slug":           "chart-3d",
				"name":           "chart-3d",
				"description":    "3D charts",
				"latest_version": "1.2.3",
				"owner":          map[string]any{"name": "ACME"},
				"deprecated":     true,
				"moved_to":       "chart-3d-ng",
			},
			{
				"slug":           "chart-lite",
				"name":           "chart-lite",
				"latest_version": "0.9.0",
				"owner":          map[string]any{"name": "ACME"},
			},
		},
		"total": 2,
	})
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	results, err := registry.Search("chart")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].Deprecated || results[0].MovedTo != "chart-3d-ng" {
		t.Errorf("deprecated result = %+v, want Deprecated=true MovedTo=chart-3d-ng", results[0])
	}
	if results[1].Deprecated || results[1].MovedTo != "" {
		t.Errorf("live result = %+v, want no deprecation", results[1])
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

// TestPublishCreateVersionSendsMetadata verifies the rich per-version
// metadata (repository/author/license/genero/dependencies/readme/userguide)
// is marshalled into the create-version payload with the shapes the registry
// expects — in particular dependencies as {fgl:{...}, java:[{...}]}.
func TestPublishCreateVersionSendsMetadata(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	meta := registry.VersionMeta{
		Repository: "https://github.com/acme/demo",
		Author:     "Acme <dev@acme.com>",
		License:    "MIT",
		Genero:     "^6.0.0",
		Dependencies: manifest.Dependencies{
			FGL:  map[string]string{"json-path": "^1.0.0"},
			Java: []manifest.JavaDependency{{GroupID: "com.acme", ArtifactID: "x", Version: "1.2.3"}},
		},
		Readme:    "# Demo",
		Userguide: "## Guide",
	}
	if err := registry.PublishCreateVersion("demo", "1.2.0", "", nil, meta); err != nil {
		t.Fatalf("PublishCreateVersion: %v", err)
	}

	for k, want := range map[string]string{
		"version":    "1.2.0",
		"repository": "https://github.com/acme/demo",
		"author":     "Acme <dev@acme.com>",
		"license":    "MIT",
		"genero":     "^6.0.0",
		"readme":     "# Demo",
		"userguide":  "## Guide",
	} {
		if got, _ := gotBody[k].(string); got != want {
			t.Errorf("body[%q] = %q, want %q", k, got, want)
		}
	}

	deps, ok := gotBody["dependencies"].(map[string]any)
	if !ok {
		t.Fatalf("dependencies not an object: %T", gotBody["dependencies"])
	}
	if fgl, _ := deps["fgl"].(map[string]any); fgl["json-path"] != "^1.0.0" {
		t.Errorf("dependencies.fgl[json-path] = %v, want ^1.0.0", fgl["json-path"])
	}
	java, _ := deps["java"].([]any)
	if len(java) != 1 {
		t.Fatalf("dependencies.java len = %d, want 1", len(java))
	}
	if j0, _ := java[0].(map[string]any); j0["groupId"] != "com.acme" || j0["artifactId"] != "x" || j0["version"] != "1.2.3" {
		t.Errorf("java[0] = %v, want {com.acme x 1.2.3}", java[0])
	}
}

// TestPublishCreateVersionOmitsEmptyMetadata verifies an empty VersionMeta
// adds no metadata keys to the payload, keeping older registries and the
// no-docs case unaffected.
func TestPublishCreateVersionOmitsEmptyMetadata(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if err := registry.PublishCreateVersion("demo", "1.0.0", "", nil, registry.VersionMeta{}); err != nil {
		t.Fatalf("PublishCreateVersion: %v", err)
	}
	for _, k := range []string{"repository", "author", "license", "genero", "dependencies", "readme", "userguide"} {
		if _, present := gotBody[k]; present {
			t.Errorf("empty metadata should omit %q from the payload, but it was present", k)
		}
	}
}

// TestPublishUpdateMetadataSendsDescriptionAndKeywords verifies the GIS-268 F/G
// metadata sync: one PATCH /registry/packages/<slug> carrying the manifest's
// current description and keywords (keywords verbatim — the registry normalizes).
func TestPublishUpdateMetadataSendsDescriptionAndKeywords(t *testing.T) {
	var (
		gotMethod, gotPath string
		gotBody            map[string]any
		calls              int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if err := registry.PublishUpdateMetadata("demo", "A neat package", []string{"CLI", "Tool"}); err != nil {
		t.Fatalf("PublishUpdateMetadata: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request, got %d", calls)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/registry/packages/demo" {
		t.Errorf("path = %s, want /registry/packages/demo", gotPath)
	}
	if got, _ := gotBody["description"].(string); got != "A neat package" {
		t.Errorf("description = %q, want %q", got, "A neat package")
	}
	kw, _ := gotBody["keywords"].([]any)
	if len(kw) != 2 || kw[0] != "CLI" || kw[1] != "Tool" {
		t.Errorf("keywords = %v, want [CLI Tool] verbatim", gotBody["keywords"])
	}
}

// TestPublishUpdateMetadataDescriptionOnlyOmitsKeywords: a manifest with a
// description but no keywords sends description only (no keywords key), so an
// absent keyword list never clears keywords already stored on the registry.
func TestPublishUpdateMetadataDescriptionOnlyOmitsKeywords(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if err := registry.PublishUpdateMetadata("demo", "only a description", nil); err != nil {
		t.Fatalf("PublishUpdateMetadata: %v", err)
	}
	if _, present := gotBody["description"]; !present {
		t.Error("description should be present in the payload")
	}
	if _, present := gotBody["keywords"]; present {
		t.Error("keywords should be omitted when the manifest declares none")
	}
}

// TestPublishUpdateMetadataEmptyIsNoop: nothing declared → no request at all.
func TestPublishUpdateMetadataEmptyIsNoop(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if err := registry.PublishUpdateMetadata("demo", "", nil); err != nil {
		t.Fatalf("empty metadata should be a silent no-op, got %v", err)
	}
	if calls != 0 {
		t.Errorf("expected no HTTP request for empty metadata, got %d", calls)
	}
}

// TestPublishUpdateMetadataNon2xxIsError: an older registry (or a rejected
// value) yields an error the publish path can log as non-fatal.
func TestPublishUpdateMetadataNon2xxIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unknown operation"}`, http.StatusBadRequest)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	if err := registry.PublishUpdateMetadata("demo", "x", []string{"y"}); err == nil {
		t.Fatal("expected an error on HTTP 400, got nil")
	}
}
