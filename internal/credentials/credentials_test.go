package credentials_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
	"github.com/4js-mikefolcher/fglpkg/internal/oauth"
)

const (
	registryURL = "https://registry.fglpkg.dev"
	testToken   = "abc123def456"
)

func TestLoadMissing(t *testing.T) {
	f, err := credentials.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(f.Registries) != 0 {
		t.Errorf("expected empty registries, got %d", len(f.Registries))
	}
}

func TestSetAndGet(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")

	e, ok := f.Get(registryURL)
	if !ok {
		t.Fatal("Get returned false after Set")
	}
	if e.Pat != testToken {
		t.Errorf("Pat = %q, want %q", e.Pat, testToken)
	}
	if e.Username != "alice" {
		t.Errorf("username = %q, want %q", e.Username, "alice")
	}
	if e.SavedAt == "" {
		t.Error("SavedAt should not be empty")
	}
}

func TestSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	if err := f.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f2, err := credentials.Load(home)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	e, ok := f2.Get(registryURL)
	if !ok {
		t.Fatal("Get returned false after save/load round-trip")
	}
	if e.Pat != testToken {
		t.Errorf("Pat = %q, want %q", e.Pat, testToken)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows maps only the read-only bit; a writable file always
		// reports 0666 regardless of the 0600 passed at write time.
		t.Skip("Unix file permissions not enforced on Windows")
	}
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Save(home) //nolint:errcheck

	info, err := os.Stat(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

func TestDelete(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Delete(registryURL)

	_, ok := f.Get(registryURL)
	if ok {
		t.Error("Get should return false after Delete")
	}
}

// TestClearOAuthLetsPatWin reproduces the `fglpkg login` then
// `fglpkg login --token` scenario: an explicit PAT login must clear the stored
// OAuth token so ActiveBearer resolves to the new PAT rather than the stale
// (unexpired) OAuth token that would otherwise win.
func TestClearOAuthLetsPatWin(t *testing.T) {
	os.Unsetenv("FGLPKG_TOKEN")

	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.SetOAuth(registryURL, oauth.Tokens{
		AccessToken: "stored-oauth",
		ExpiresAt:   time.Now().Add(time.Hour),
		ClientID:    "c",
	}, "alice")

	// Simulate `login --token`: drop OAuth, then store the PAT.
	f.ClearOAuth(registryURL)
	f.Set(registryURL, testToken, "")
	f.Save(home) //nolint:errcheck

	e, ok := f.Get(registryURL)
	if !ok {
		t.Fatal("entry missing after ClearOAuth + Set")
	}
	if e.OAuth != nil {
		t.Errorf("OAuth should be nil after ClearOAuth, got %+v", e.OAuth)
	}
	if e.Pat != testToken {
		t.Errorf("Pat = %q, want %q", e.Pat, testToken)
	}

	tok, err := credentials.ActiveBearer(context.Background(), home, registryURL, nil)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if tok != testToken {
		t.Errorf("ActiveBearer = %q, want %q (PAT should win once OAuth is cleared)", tok, testToken)
	}
}

func TestClearOAuthNoEntryIsNoop(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.ClearOAuth(registryURL) // must not panic or create an entry
	if _, ok := f.Get(registryURL); ok {
		t.Error("ClearOAuth created an entry for an unknown registry")
	}
}

func TestURLNormalisation(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)

	f.Set("https://Registry.Example.com/", testToken, "bob")

	if _, ok := f.Get("https://registry.example.com"); !ok {
		t.Error("URL normalisation failed: Get with different casing/trailing slash returned false")
	}
}

// ─── Legacy `token` field migration ──────────────────────────────────────────

func TestLegacyTokenMigratesToPat(t *testing.T) {
	home := t.TempDir()
	legacy := `{
	  "registries": {
	    "https://legacy.example.com": {
	      "token": "old-pat",
	      "username": "alice",
	      "savedAt": "2025-01-01T00:00:00Z"
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(home, "credentials.json"), []byte(legacy), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f, err := credentials.Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := f.Get("https://legacy.example.com")
	if !ok {
		t.Fatal("entry missing after load")
	}
	if e.Pat != "old-pat" {
		t.Errorf("Pat = %q, want old-pat", e.Pat)
	}
	if e.Token != "" {
		t.Errorf("Token should be cleared in memory, got %q", e.Token)
	}

	// Persisted shape must omit `token`.
	if err := f.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "credentials.json"))
	var on struct {
		Registries map[string]map[string]any `json:"registries"`
	}
	if err := json.Unmarshal(raw, &on); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	entry := on.Registries["https://legacy.example.com"]
	if _, hasToken := entry["token"]; hasToken {
		t.Error("Save still wrote `token` field — should be omitted after migration")
	}
	if entry["pat"] != "old-pat" {
		t.Errorf("pat field = %v, want old-pat", entry["pat"])
	}
}

// ─── Env var resolution ──────────────────────────────────────────────────────

func TestConsumerEnvBearer(t *testing.T) {
	t.Setenv("FGLPKG_TOKEN", "consumer-tok")
	if got := credentials.ConsumerEnvBearer(); got != "consumer-tok" {
		t.Errorf("ConsumerEnvBearer = %q, want consumer-tok", got)
	}
}

func TestTokenForEnvVarOverride(t *testing.T) {
	t.Setenv("FGLPKG_TOKEN", "env-token")
	tok := credentials.TokenFor(t.TempDir(), registryURL)
	if tok != "env-token" {
		t.Errorf("TokenFor = %q, want env-token", tok)
	}
}

func TestTokenForCredentialsFile(t *testing.T) {

	os.Unsetenv("FGLPKG_TOKEN")
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Save(home) //nolint:errcheck

	tok := credentials.TokenFor(home, registryURL)
	if tok != testToken {
		t.Errorf("TokenFor = %q, want %q", tok, testToken)
	}
}

func TestTokenForNotFound(t *testing.T) {

	os.Unsetenv("FGLPKG_TOKEN")
	tok := credentials.TokenFor(t.TempDir(), registryURL)
	if tok != "" {
		t.Errorf("TokenFor = %q, want empty", tok)
	}
}

// ─── ActiveBearer ────────────────────────────────────────────────────────────

func TestActiveBearerEnvWins(t *testing.T) {
	t.Setenv("FGLPKG_TOKEN", "env-bearer")
	// Stored OAuth + PAT should be irrelevant when env is set.
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.SetOAuth(registryURL, oauth.Tokens{
		AccessToken: "stored-oauth",
		ExpiresAt:   time.Now().Add(time.Hour),
		ClientID:    "c",
	}, "alice")
	f.Save(home) //nolint:errcheck

	tok, err := credentials.ActiveBearer(context.Background(), home, registryURL, nil)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if tok != "env-bearer" {
		t.Errorf("ActiveBearer = %q, want env-bearer", tok)
	}
}

func TestActiveBearerOAuthUnexpired(t *testing.T) {
	os.Unsetenv("FGLPKG_TOKEN")

	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.SetOAuth(registryURL, oauth.Tokens{
		AccessToken: "stored-oauth",
		ExpiresAt:   time.Now().Add(time.Hour),
		ClientID:    "c",
	}, "alice")
	f.Save(home) //nolint:errcheck

	tok, err := credentials.ActiveBearer(context.Background(), home, registryURL, nil)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if tok != "stored-oauth" {
		t.Errorf("ActiveBearer = %q, want stored-oauth", tok)
	}
}

func TestActiveBearerRefreshesAndPersists(t *testing.T) {
	os.Unsetenv("FGLPKG_TOKEN")

	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.SetOAuth(registryURL, oauth.Tokens{
		AccessToken:  "expired",
		RefreshToken: "ref-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
		ClientID:     "c",
	}, "alice")
	f.Save(home) //nolint:errcheck

	called := false
	refresh := func(ctx context.Context, base string, prev oauth.Tokens) (oauth.Tokens, error) {
		called = true
		if prev.RefreshToken != "ref-1" {
			t.Errorf("refresh called with RefreshToken = %q, want ref-1", prev.RefreshToken)
		}
		return oauth.Tokens{
			AccessToken:  "fresh",
			RefreshToken: "ref-2",
			ExpiresAt:    time.Now().Add(time.Hour),
			ClientID:     prev.ClientID,
		}, nil
	}
	tok, err := credentials.ActiveBearer(context.Background(), home, registryURL, refresh)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if !called {
		t.Fatal("refresh was not invoked for an expired OAuth token")
	}
	if tok != "fresh" {
		t.Errorf("ActiveBearer = %q, want fresh", tok)
	}

	// Persisted rotation: next Load should see the new tokens.
	f2, _ := credentials.Load(home)
	e2, _ := f2.Get(registryURL)
	if e2.OAuth == nil || e2.OAuth.AccessToken != "fresh" || e2.OAuth.RefreshToken != "ref-2" {
		t.Errorf("persisted OAuth = %+v, want fresh/ref-2", e2.OAuth)
	}
}

func TestActiveBearerFallsThroughToPatOnRefreshFailure(t *testing.T) {
	os.Unsetenv("FGLPKG_TOKEN")

	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.SetOAuth(registryURL, oauth.Tokens{
		AccessToken:  "expired",
		RefreshToken: "ref-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
		ClientID:     "c",
	}, "")
	f.Set(registryURL, "fallback-pat", "")
	f.Save(home) //nolint:errcheck

	refresh := func(ctx context.Context, base string, prev oauth.Tokens) (oauth.Tokens, error) {
		return oauth.Tokens{}, context.DeadlineExceeded
	}
	tok, err := credentials.ActiveBearer(context.Background(), home, registryURL, refresh)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if tok != "fallback-pat" {
		t.Errorf("ActiveBearer = %q, want fallback-pat", tok)
	}
}

func TestActiveBearerAnonymousWhenNothingStored(t *testing.T) {
	os.Unsetenv("FGLPKG_TOKEN")

	tok, err := credentials.ActiveBearer(context.Background(), t.TempDir(), registryURL, nil)
	if err != nil {
		t.Fatalf("ActiveBearer: %v", err)
	}
	if tok != "" {
		t.Errorf("ActiveBearer = %q, want empty (no stored creds)", tok)
	}
}

// ─── ActivePublishBearer ─────────────────────────────────────────────────────

func TestActivePublishBearerEnvWins(t *testing.T) {
	t.Setenv("FGLPKG_TOKEN", "env-pub")
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, "stored-pat", "")
	f.Save(home) //nolint:errcheck

	if got := credentials.ActivePublishBearer(home, registryURL); got != "env-pub" {
		t.Errorf("ActivePublishBearer = %q, want env-pub", got)
	}
}

func TestActivePublishBearerFallsToStored(t *testing.T) {

	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, "stored-pat", "")
	f.Save(home) //nolint:errcheck

	if got := credentials.ActivePublishBearer(home, registryURL); got != "stored-pat" {
		t.Errorf("ActivePublishBearer = %q, want stored-pat", got)
	}
}
