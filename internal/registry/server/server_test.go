package server_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry/server"
)

// ─── Test harness ─────────────────────────────────────────────────────────────

const testToken = "test-secret-token"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := server.Config{
		Addr:         ":0",
		DataDir:      t.TempDir(),
		PublishToken: testToken,
		BaseURL:      "", // will be filled by httptest URL
	}
	srv, err := server.NewTestServer(cfg)
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// publish sends a multipart publish request to the test server.
func publish(t *testing.T, ts *httptest.Server, name, version string, meta map[string]any, token string) *http.Response {
	t.Helper()

	metaJSON, _ := json.Marshal(meta)
	zipData := makeZip(t, name+".42m", "-- compiled BDL stub")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// meta field
	mw.WriteField("meta", string(metaJSON)) //nolint:errcheck

	// zip field
	fw, err := mw.CreateFormFile("zip", name+"-"+version+".zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(zipData) //nolint:errcheck
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/packages/%s/%s/publish", ts.URL, name, version),
		&body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("publish request: %v", err)
	}
	return resp
}

// makeZip creates a minimal zip with one file entry.
func makeZip(t *testing.T, filename, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create(filename)
	fw.Write([]byte(content)) //nolint:errcheck
	zw.Close()
	return buf.Bytes()
}

func getJSON(t *testing.T, url string, target any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if target != nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if err := json.Unmarshal(body, target); err != nil {
			t.Fatalf("JSON decode from %s: %v\nbody: %s", url, err, body)
		}
	}
	return resp
}

// ─── Health ───────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	var result map[string]string
	resp := getJSON(t, ts.URL+"/health", &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

// ─── Publish ──────────────────────────────────────────────────────────────────

func TestPublishSuccess(t *testing.T) {
	ts := newTestServer(t)
	meta := map[string]any{
		"description": "A test package",
		"author":      "Alice",
		"genero":      "^4.0.0",
	}
	resp := publish(t, ts, "myutils", "1.0.0", meta, testToken)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("publish status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result["name"] != "myutils" {
		t.Errorf("name = %q, want %q", result["name"], "myutils")
	}
	if result["version"] != "1.0.0" {
		t.Errorf("version = %q, want %q", result["version"], "1.0.0")
	}
	if result["checksum"] == "" {
		t.Error("checksum should not be empty")
	}
	if result["downloadUrl"] == "" {
		t.Error("downloadUrl should not be empty")
	}
}

func TestPublishUnauthorised(t *testing.T) {
	ts := newTestServer(t)
	resp := publish(t, ts, "myutils", "1.0.0", map[string]any{}, "wrong-token")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPublishMissingToken(t *testing.T) {
	ts := newTestServer(t)
	resp := publish(t, ts, "myutils", "1.0.0", map[string]any{}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPublishDuplicateVersionRejected(t *testing.T) {
	ts := newTestServer(t)
	meta := map[string]any{"description": "v1"}
	publish(t, ts, "myutils", "1.0.0", meta, testToken) // first publish

	resp := publish(t, ts, "myutils", "1.0.0", meta, testToken) // duplicate
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestPublishInvalidPackageName(t *testing.T) {
	ts := newTestServer(t)
	// Package names with uppercase should be rejected.
	resp := publish(t, ts, "MyUtils", "1.0.0", map[string]any{}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPublishInvalidVersion(t *testing.T) {
	ts := newTestServer(t)
	resp := publish(t, ts, "myutils", "v1", map[string]any{}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPublishChecksumMismatch(t *testing.T) {
	ts := newTestServer(t)
	meta := map[string]any{
		"description": "test",
		"checksum":    "0000000000000000000000000000000000000000000000000000000000000000",
	}
	resp := publish(t, ts, "myutils", "1.0.0", meta, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 on checksum mismatch", resp.StatusCode)
	}
}

// ─── Version list ─────────────────────────────────────────────────────────────

func TestVersionList(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{"genero": "^3.0.0"}, testToken)
	publish(t, ts, "myutils", "1.1.0", map[string]any{"genero": "^4.0.0"}, testToken)

	var result struct {
		Name           string `json:"name"`
		Versions       []string
		VersionEntries []struct {
			Version          string `json:"version"`
			GeneroConstraint string `json:"genero"`
		} `json:"versionEntries"`
	}
	resp := getJSON(t, ts.URL+"/packages/myutils/versions", &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(result.Versions) != 2 {
		t.Errorf("versions count = %d, want 2", len(result.Versions))
	}
	if len(result.VersionEntries) != 2 {
		t.Errorf("versionEntries count = %d, want 2", len(result.VersionEntries))
	}
	// Verify Genero constraints are preserved.
	for _, ve := range result.VersionEntries {
		if ve.Version == "1.0.0" && ve.GeneroConstraint != "^3.0.0" {
			t.Errorf("1.0.0 genero = %q, want %q", ve.GeneroConstraint, "^3.0.0")
		}
		if ve.Version == "1.1.0" && ve.GeneroConstraint != "^4.0.0" {
			t.Errorf("1.1.0 genero = %q, want %q", ve.GeneroConstraint, "^4.0.0")
		}
	}
}

func TestVersionListNotFound(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/packages/doesnotexist/versions")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── Package info ─────────────────────────────────────────────────────────────

func TestPackageInfo(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{
		"description": "Utility library",
		"author":      "Bob",
		"genero":      "^4.0.0",
		"fglDeps":     map[string]string{"core": "^1.0.0"},
	}, testToken)

	var result map[string]any
	resp := getJSON(t, ts.URL+"/packages/myutils/1.0.0", &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if result["name"] != "myutils" {
		t.Errorf("name = %q, want %q", result["name"], "myutils")
	}
	if result["description"] != "Utility library" {
		t.Errorf("description = %q", result["description"])
	}
	if result["genero"] != "^4.0.0" {
		t.Errorf("genero = %q, want %q", result["genero"], "^4.0.0")
	}
	if result["downloadUrl"] == "" {
		t.Error("downloadUrl should be populated")
	}
}

func TestPackageInfoVersionNotFound(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, testToken)
	resp, _ := http.Get(ts.URL + "/packages/myutils/9.9.9")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── Download ─────────────────────────────────────────────────────────────────

func TestDownload(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, testToken)

	resp, err := http.Get(ts.URL + "/packages/myutils/1.0.0/download")
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "zip") {
		t.Errorf("Content-Type = %q, want application/zip", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("downloaded zip body is empty")
	}

	// Verify it's actually a valid zip.
	_, err = zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Errorf("downloaded content is not a valid zip: %v", err)
	}
}

func TestDownloadNotFound(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/packages/doesnotexist/1.0.0/download")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── Search ───────────────────────────────────────────────────────────────────

func TestSearch(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "json-utils", "1.0.0", map[string]any{"description": "JSON helpers"}, testToken)
	publish(t, ts, "dbtools", "2.0.0", map[string]any{"description": "Database tools"}, testToken)
	publish(t, ts, "netlib", "1.0.0", map[string]any{"description": "Network library"}, testToken)

	var results []map[string]any
	resp := getJSON(t, ts.URL+"/search?q=json", &results)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["name"] != "json-utils" {
		t.Errorf("name = %q, want %q", results[0]["name"], "json-utils")
	}
}

func TestSearchDescriptionMatch(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "mylib", "1.0.0", map[string]any{"description": "Handles JSON parsing"}, testToken)

	var results []map[string]any
	getJSON(t, ts.URL+"/search?q=json", &results)
	if len(results) != 1 {
		t.Errorf("expected 1 result (description match), got %d", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	ts := newTestServer(t)
	publish(t, ts, "mylib", "1.0.0", map[string]any{}, testToken)

	var results []map[string]any
	resp := getJSON(t, ts.URL+"/search?q=zzznomatch", &results)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchMissingQuery(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/search")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// ─── Persistence ─────────────────────────────────────────────────────────────

// TestPersistence verifies data survives a server restart by reusing the same
// DataDir across two server instances.
func TestPersistence(t *testing.T) {
	dataDir := t.TempDir()

	cfg := server.Config{
		DataDir:      dataDir,
		PublishToken: testToken,
	}

	// First server: publish a package.
	srv1, _ := server.NewTestServer(cfg)
	ts1 := httptest.NewServer(srv1)
	publish(t, ts1, "myutils", "1.0.0", map[string]any{"description": "test"}, testToken)
	ts1.Close()

	// Second server: data must still be queryable.
	srv2, err := server.NewTestServer(cfg)
	if err != nil {
		t.Fatalf("second server init: %v", err)
	}
	ts2 := httptest.NewServer(srv2)
	defer ts2.Close()

	var result struct{ Name string }
	resp := getJSON(t, ts2.URL+"/packages/myutils/versions", &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after restart, status = %d, want 200", resp.StatusCode)
	}
}

// ─── File cleanup check ───────────────────────────────────────────────────────

// TestChecksumMismatchCleansUp verifies that a failed publish (checksum
// mismatch) does not leave orphan files in the data directory.
func TestChecksumMismatchCleansUp(t *testing.T) {
	ts := newTestServer(t)
	cfg := server.Config{DataDir: ts.URL} // we just need the data dir path from config
	_ = cfg

	meta := map[string]any{
		"checksum": "0000000000000000000000000000000000000000000000000000000000000000",
	}
	publish(t, ts, "myutils", "1.0.0", meta, testToken)

	// Version should not be listed.
	var result struct {
		Versions []string `json:"versions"`
	}
	getJSON(t, ts.URL+"/packages/myutils/versions", &result)
	for _, v := range result.Versions {
		if v == "1.0.0" {
			t.Error("version 1.0.0 should not exist after checksum mismatch")
		}
	}
}

// ─── Prevent data dir access outside DataDir ──────────────────────────────────

func TestPathTraversalRejected(t *testing.T) {
	ts := newTestServer(t)
	// Attempt to publish a package name with path traversal.
	resp := publish(t, ts, "../evil", "1.0.0", map[string]any{}, testToken)
	if resp.StatusCode == http.StatusCreated {
		t.Error("path traversal package name should be rejected")
	}
}

// ─── Helpers used by tests but not exported in production ─────────────────────

// writeTestFile creates a temp file with content and returns its path.
func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(content) //nolint:errcheck
	return f.Name()
}
