package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSearchArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantTerm string
		wantAll  bool
		wantErr  string
	}{
		{"keyword", []string{"foo"}, "foo", false, ""},
		{"all", []string{"--all"}, "", true, ""},
		{"no args errors", nil, "", false, "usage:"},
		{"all + term conflict", []string{"--all", "foo"}, "", false, "mutually exclusive"},
		{"two terms errors", []string{"foo", "bar"}, "", false, "extra argument"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, all, err := parseSearchArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if term != c.wantTerm || all != c.wantAll {
				t.Errorf("term=%q all=%v, want term=%q all=%v",
					term, all, c.wantTerm, c.wantAll)
			}
		})
	}
}

// TestCmdSearchAllSurfacesCleanErrorOn400 simulates an old registry that
// still rejects empty `q` and confirms the client surfaces the upgrade
// hint rather than leaking the raw HTTP 400.
func TestCmdSearchAllSurfacesCleanErrorOn400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			http.Error(w, "missing query parameter q", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate the fglpkg home so a developer's real ~/.fglpkg/config.json
	// (e.g. a globally-configured Artifactory repo) doesn't add a second
	// provider that satisfies the search and masks the GI 400 under test.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	err := cmdSearch([]string{"--all"})
	if err == nil {
		t.Fatal("expected error from old server's 400, got nil")
	}
	if !strings.Contains(err.Error(), "doesn't support --all") {
		t.Errorf("err = %v, want one explaining --all unsupported", err)
	}
}
