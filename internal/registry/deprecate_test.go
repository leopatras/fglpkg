package registry_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// capturedRequest records what a deprecate PATCH sent, so tests can assert the
// method, path, and JSON body.
type capturedRequest struct {
	method string
	path   string
	body   map[string]any
}

// deprecateServer returns an httptest server that records the request and
// replies with the given status. FGLPKG_REGISTRY is pointed at it.
func deprecateServer(t *testing.T, status int, rec *capturedRequest) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		w.WriteHeader(status)
	}))
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	return ts
}

func TestPublishDeprecateVersionSendsPatch(t *testing.T) {
	var rec capturedRequest
	ts := deprecateServer(t, http.StatusOK, &rec)
	defer ts.Close()

	if err := registry.PublishDeprecateVersion("chart-3d", "1.2.3", "please upgrade", "chart-3d-ng", false); err != nil {
		t.Fatalf("PublishDeprecateVersion: %v", err)
	}
	if rec.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", rec.method)
	}
	if want := "/registry/packages/chart-3d/versions/1.2.3"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.body["deprecated"] != true {
		t.Errorf("body[deprecated] = %v, want true", rec.body["deprecated"])
	}
	if rec.body["deprecationMessage"] != "please upgrade" {
		t.Errorf("body[deprecationMessage] = %v, want %q", rec.body["deprecationMessage"], "please upgrade")
	}
	if rec.body["movedTo"] != "chart-3d-ng" {
		t.Errorf("body[movedTo] = %v, want chart-3d-ng", rec.body["movedTo"])
	}
}

func TestPublishDeprecatePackageSendsPatch(t *testing.T) {
	var rec capturedRequest
	ts := deprecateServer(t, http.StatusOK, &rec)
	defer ts.Close()

	if err := registry.PublishDeprecatePackage("chart-3d", "moved", "chart-3d-ng@2.0.0", false); err != nil {
		t.Fatalf("PublishDeprecatePackage: %v", err)
	}
	if want := "/registry/packages/chart-3d"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.body["movedTo"] != "chart-3d-ng@2.0.0" {
		t.Errorf("body[movedTo] = %v, want chart-3d-ng@2.0.0", rec.body["movedTo"])
	}
}

// An --undo request sends only {deprecated:false} — no message/movedTo, so the
// registry clears them.
func TestPublishDeprecateVersionUndoBody(t *testing.T) {
	var rec capturedRequest
	ts := deprecateServer(t, http.StatusOK, &rec)
	defer ts.Close()

	if err := registry.PublishDeprecateVersion("chart-3d", "1.2.3", "", "", true); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if rec.body["deprecated"] != false {
		t.Errorf("body[deprecated] = %v, want false", rec.body["deprecated"])
	}
	if _, present := rec.body["deprecationMessage"]; present {
		t.Errorf("undo body should not carry deprecationMessage: %v", rec.body)
	}
	if _, present := rec.body["movedTo"]; present {
		t.Errorf("undo body should not carry movedTo: %v", rec.body)
	}
}

// movedTo is omitted from the body when empty (a plain deprecate, no successor).
func TestPublishDeprecateVersionOmitsEmptyMovedTo(t *testing.T) {
	var rec capturedRequest
	ts := deprecateServer(t, http.StatusOK, &rec)
	defer ts.Close()

	if err := registry.PublishDeprecateVersion("chart-3d", "1.2.3", "unmaintained", "", false); err != nil {
		t.Fatalf("deprecate: %v", err)
	}
	if _, present := rec.body["movedTo"]; present {
		t.Errorf("empty movedTo should be omitted, body = %v", rec.body)
	}
}

func TestPublishDeprecateStatusErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, registry.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, registry.ErrForbidden},
		{"notfound", http.StatusNotFound, registry.ErrNotFound},
		{"toolong", http.StatusBadRequest, registry.ErrMessageTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec capturedRequest
			ts := deprecateServer(t, tc.status, &rec)
			defer ts.Close()

			err := registry.PublishDeprecateVersion("chart-3d", "1.2.3", "m", "", false)
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d → %v, want errors.Is(..., %v)", tc.status, err, tc.want)
			}
		})
	}
}

// A package-level deprecation must surface on every version through FetchInfo,
// even when the version itself carries no version-level flag (the OR-merge).
func TestFetchInfoMergesPackageLevelDeprecation(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug":               "chart-3d",
		"deprecated":         true,
		"deprecation_message": "whole package retired",
		"moved_to":           "chart-3d-ng",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "aa", "download_url": "https://r2/x.zip"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	info, err := registry.FetchInfo("chart-3d", "1.0.0")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if !info.Deprecated {
		t.Error("package-level deprecation should mark the version deprecated")
	}
	if info.DeprecationMessage != "whole package retired" {
		t.Errorf("DeprecationMessage = %q, want package-level fallback", info.DeprecationMessage)
	}
	if info.MovedTo != "chart-3d-ng" {
		t.Errorf("MovedTo = %q, want chart-3d-ng", info.MovedTo)
	}
}

// A version-level message wins over the package-level one when both are set.
func TestFetchInfoVersionLevelDeprecationWins(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug":               "chart-3d",
		"deprecated":         true,
		"deprecation_message": "package msg",
		"moved_to":           "pkg-ng",
		"versions": []map[string]any{
			{
				"version":             "1.0.0",
				"deprecated":          true,
				"deprecation_message": "version msg",
				"moved_to":            "ver-ng",
				"artifacts": []map[string]any{
					{"variant": "default", "sha256": "aa", "download_url": "https://r2/x.zip"},
				},
			},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	info, err := registry.FetchInfo("chart-3d", "1.0.0")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.DeprecationMessage != "version msg" || info.MovedTo != "ver-ng" {
		t.Errorf("version-level should win: msg=%q movedTo=%q", info.DeprecationMessage, info.MovedTo)
	}
}
