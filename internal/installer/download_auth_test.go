package installer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDownloadAndVerify_AnonymousRepoDoesNotLeakRegistryToken is the regression
// test for GIS-267: when a download URL belongs to a configured secondary
// repository whose auth scheme is "anonymous" (matched, but no headers), the GI
// registry bearer must NOT be attached — even though registryToken is set.
func TestDownloadAndVerify_AnonymousRepoDoesNotLeakRegistryToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	// repoMatched=true (the host is a configured secondary repo), no repo
	// headers (anonymous), and a GI registryToken IS present — the leak
	// precondition. Empty checksum skips verification.
	err := downloadAndVerify(
		srv.URL+"/acme-utils/1.0.0/acme-utils-1.0.0-genero6.zip",
		"", "acme-utils", &buf,
		"",                /* githubToken */
		"gi-secret-bearer", /* registryToken */
		nil,               /* repoHeaders */
		true,              /* repoMatched */
	)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("anonymous secondary repo received Authorization %q — GI token leaked", gotAuth)
	}
}

// TestDownloadAndVerify_SecondaryRepoSendsItsOwnAuth confirms a non-anonymous
// secondary repo still gets its configured header (and not the GI token).
func TestDownloadAndVerify_SecondaryRepoSendsItsOwnAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	err := downloadAndVerify(
		srv.URL+"/x.zip", "", "x", &buf,
		"", "gi-secret-bearer",
		map[string]string{"Authorization": "Basic dXNlcjp0b2tlbg=="},
		true,
	)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if gotAuth != "Basic dXNlcjp0b2tlbg==" {
		t.Fatalf("secondary repo Authorization = %q, want the configured Basic header", gotAuth)
	}
}

// TestDownloadAndVerify_GIStillGetsRegistryToken confirms the GI path (no
// configured secondary repo matched) still receives the registry bearer.
func TestDownloadAndVerify_GIStillGetsRegistryToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	err := downloadAndVerify(
		srv.URL+"/pkg.zip", "", "pkg", &buf,
		"", "gi-secret-bearer",
		nil,   /* repoHeaders */
		false, /* repoMatched — not a configured secondary repo */
	)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if gotAuth != "Bearer gi-secret-bearer" {
		t.Fatalf("GI download Authorization = %q, want Bearer gi-secret-bearer", gotAuth)
	}
}
