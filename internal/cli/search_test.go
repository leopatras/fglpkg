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

func TestSearchDeprecatedStatus(t *testing.T) {
	cases := []struct {
		name       string
		deprecated bool
		movedTo    string
		want       string
	}{
		{"live", false, "", ""},
		{"live ignores stray movedTo", false, "chart-3d-ng", ""},
		{"deprecated no successor", true, "", "deprecated"},
		{"deprecated with successor", true, "chart-3d-ng", "deprecated -> chart-3d-ng"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := searchDeprecatedStatus(c.deprecated, c.movedTo); got != c.want {
				t.Errorf("searchDeprecatedStatus(%v, %q) = %q, want %q",
					c.deprecated, c.movedTo, got, c.want)
			}
		})
	}
}

// TestCmdSearchStatusColumnConditional confirms the STATUS column is omitted
// entirely when no match is deprecated (byte-for-byte the original layout) and
// appears only once at least one match carries a deprecation.
func TestCmdSearchStatusColumnConditional(t *testing.T) {
	cases := []struct {
		name       string
		packages   []map[string]any
		wantStatus bool   // STATUS header expected in output
		wantSubstr string // value that must appear when deprecated
	}{
		{
			name: "all live omits STATUS column",
			packages: []map[string]any{
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
				{"slug": "chart-pro", "latest_version": "2.0.0", "description": "pro charts"},
			},
			wantStatus: false,
		},
		{
			name: "deprecated match shows STATUS column",
			packages: []map[string]any{
				{"slug": "chart-3d", "latest_version": "1.2.3", "description": "3D charts",
					"deprecated": true, "moved_to": "chart-3d-ng"},
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
			},
			wantStatus: true,
			wantSubstr: "deprecated -> chart-3d-ng",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"packages": c.packages})
			}))
			defer ts.Close()
			t.Setenv("FGLPKG_REGISTRY", ts.URL)
			// Isolate the fglpkg home so a developer's real config doesn't add a
			// second provider and divert us onto the multi-provider path.
			t.Setenv("FGLPKG_HOME", t.TempDir())

			out, err := captureStdout(t, func() error { return cmdSearch([]string{"--all"}) })
			if err != nil {
				t.Fatalf("cmdSearch: %v", err)
			}
			if got := strings.Contains(out, "STATUS"); got != c.wantStatus {
				t.Errorf("STATUS column present = %v, want %v\noutput:\n%s", got, c.wantStatus, out)
			}
			if c.wantStatus {
				if !strings.Contains(out, c.wantSubstr) {
					t.Errorf("output missing %q\noutput:\n%s", c.wantSubstr, out)
				}
			} else if strings.Contains(out, "deprecated") {
				t.Errorf("live-only output unexpectedly mentions deprecation\noutput:\n%s", out)
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
