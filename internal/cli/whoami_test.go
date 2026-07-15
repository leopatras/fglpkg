package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoamiRequestNewEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/whoami" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user":    map[string]any{"id": "u1", "email": "jane@acme.com", "name": "Jane Developer"},
			"partner": map[string]any{"id": "p1", "name": "ACME"},
			"scopes":  []string{"registry:read"},
		})
	}))
	defer ts.Close()

	got, err := whoamiRequest(ts.URL, "tok")
	if err != nil {
		t.Fatalf("whoamiRequest: %v", err)
	}
	if got.User.Name != "Jane Developer" || got.User.Email != "jane@acme.com" {
		t.Errorf("User = %+v, want Jane / jane@acme.com", got.User)
	}
	if got.Partner == nil || got.Partner.Name != "ACME" {
		t.Errorf("Partner = %+v, want ACME", got.Partner)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "registry:read" {
		t.Errorf("Scopes = %v, want [registry:read]", got.Scopes)
	}
}

func TestWhoamiRequestFallsBackToLegacyOn404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry/whoami":
			http.NotFound(w, r)
		case "/auth/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	got, err := whoamiRequest(ts.URL, "tok")
	if err != nil {
		t.Fatalf("whoamiRequest: %v", err)
	}
	if got.User.Name != "alice" {
		t.Errorf("User.Name = %q, want alice (synthesised from legacy username)", got.User.Name)
	}
	if got.Partner != nil {
		t.Errorf("Partner = %+v, want nil from legacy endpoint", got.Partner)
	}
}

func TestWhoamiRequest401ReturnsInvalidToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer ts.Close()

	_, err := whoamiRequest(ts.URL, "tok")
	if err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Errorf("err = %v, want one mentioning 'invalid or expired'", err)
	}
}

func TestWhoamiSubjectFormatting(t *testing.T) {
	cases := []struct {
		name string
		w    whoamiResult
		want string
	}{
		{"name+email", mkWho("Jane", "jane@acme.com", "u1"), "Jane <jane@acme.com>"},
		{"email only", mkWho("", "jane@acme.com", "u1"), "jane@acme.com"},
		{"name only", mkWho("Jane", "", "u1"), "Jane"},
		{"id only", mkWho("", "", "u1"), "u1"},
		{"none", mkWho("", "", ""), "(user)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := whoamiSubject(c.w); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func mkWho(name, email, id string) whoamiResult {
	var w whoamiResult
	w.User.Name = name
	w.User.Email = email
	w.User.ID = id
	return w
}

func TestParseLoginArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantPAT string
		wantErr bool
	}{
		{"no args", nil, "", false},
		{"token", []string{"--token", "gpr_abc"}, "gpr_abc", false},
		{"token missing value", []string{"--token"}, "", true},
		{"unknown", []string{"--what"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLoginArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if got.token != c.wantPAT {
				t.Errorf("token = %q, want %q", got.token, c.wantPAT)
			}
		})
	}
}

func TestParseLoginArgs_Registry(t *testing.T) {
	got, err := parseLoginArgs([]string{"--registry", "acme", "--user", "u", "--password", "p", "--api-key", "k"})
	if err != nil {
		t.Fatalf("parseLoginArgs: %v", err)
	}
	if got.registry != "acme" || got.user != "u" || got.password != "p" || got.apiKey != "k" {
		t.Fatalf("parsed = %+v", got)
	}
}
