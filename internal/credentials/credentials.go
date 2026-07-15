// Package credentials manages per-registry authentication credentials stored
// in ~/.fglpkg/credentials.json (mode 0600).
//
// Each entry may carry:
//   - OAuth tokens (preferred — produced by `fglpkg login` browser flow)
//   - A PAT (legacy / CI / --token fallback)
//   - A GitHub token used for downloading from private GitHub Releases
//   - Both — OAuth wins at use time
//
// The on-disk schema is forward-compatible: unknown fields are preserved.
// A legacy `token` field is read once and migrated to `pat` on the next Save.
//
// Example shape:
//
//	{
//	  "registries": {
//	    "https://service.generointelligence.ai": {
//	      "oauth": {
//	        "access_token":  "…",
//	        "refresh_token": "…",
//	        "expires_at":    "2026-06-04T21:30:22Z",
//	        "scope":         "registry:read",
//	        "client_id":     "abc123"
//	      },
//	      "pat": "gpr_…",
//	      "username": "alice",
//	      "githubToken": "ghp_…",
//	      "savedAt": "2026-05-29T21:30:22Z"
//	    }
//	  }
//	}
package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/oauth"
)

const filename = "credentials.json"

// Entry holds the stored credentials for one registry URL.
type Entry struct {
	OAuth       *oauth.Tokens `json:"oauth,omitempty"`
	Pat         string        `json:"pat,omitempty"`
	Token       string        `json:"token,omitempty"` // legacy: read-only, migrated to Pat on next Save
	Username    string        `json:"username,omitempty"`
	GitHubToken string        `json:"githubToken,omitempty"`
	// APIKey is the JFrog X-JFrog-Art-Api key, used when a repository's auth
	// scheme is "apikey". bearer/basic reuse Pat (secret) + Username.
	APIKey  string `json:"apiKey,omitempty"`
	SavedAt string `json:"savedAt"`
}

// File is the top-level credentials file structure.
type File struct {
	Registries map[string]Entry `json:"registries"`
}

// Load reads the credentials file from the fglpkg home directory.
// Returns an empty File if the file does not exist. Legacy `token` fields
// are migrated to `pat` in memory; the change is persisted on next Save.
func Load(home string) (*File, error) {
	path := filepath.Join(home, filename)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{Registries: make(map[string]Entry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid credentials file: %w", err)
	}
	if f.Registries == nil {
		f.Registries = make(map[string]Entry)
	}
	// One-shot migration: lift legacy `token` into `pat`.
	for k, e := range f.Registries {
		if e.Pat == "" && e.Token != "" {
			e.Pat = e.Token
		}
		e.Token = "" // never written back out
		f.Registries[k] = e
	}
	return &f, nil
}

// Save writes the credentials file with mode 0600.
func (f *File) Save(home string) error {
	if err := os.MkdirAll(home, 0700); err != nil {
		return fmt.Errorf("cannot create credentials directory: %w", err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(home, filename)
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// Set stores a PAT for the given registry URL. Existing OAuth tokens on the
// entry are preserved.
func (f *File) Set(registryURL, token, username string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	e.Pat = token
	if username != "" {
		e.Username = username
	}
	e.SavedAt = nowRFC3339()
	f.Registries[key] = e
}

// SetOAuth stores OAuth tokens for the given registry URL. Existing PAT on
// the entry is preserved (OAuth wins at use time).
func (f *File) SetOAuth(registryURL string, t oauth.Tokens, username string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	tc := t
	e.OAuth = &tc
	if username != "" {
		e.Username = username
	}
	e.SavedAt = nowRFC3339()
	f.Registries[key] = e
}

// SetAPIKey stores a JFrog API key for the given registry URL, for the
// "apikey" auth scheme. Existing OAuth/PAT are preserved.
func (f *File) SetAPIKey(registryURL, apiKey string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	e.APIKey = apiKey
	e.SavedAt = nowRFC3339()
	f.Registries[key] = e
}

// SetBasic stores a username + secret (password or access token) for the
// "basic" auth scheme. Existing OAuth is preserved.
func (f *File) SetBasic(registryURL, username, secret string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	e.Username = username
	e.Pat = secret
	e.SavedAt = nowRFC3339()
	f.Registries[key] = e
}

// Get retrieves the credential entry for registryURL.
func (f *File) Get(registryURL string) (Entry, bool) {
	e, ok := f.Registries[normalise(registryURL)]
	return e, ok
}

// Auth scheme names (mirrors internal/config; duplicated to avoid an import).
const (
	SchemeBearer    = "bearer"
	SchemeBasic     = "basic"
	SchemeAPIKey    = "apikey"
	SchemeAnonymous = "anonymous"
)

// AuthHeaders returns the HTTP headers implementing the given auth scheme for
// registryURL, using stored credentials. Returns nil for anonymous, an unknown
// scheme, or when the required secret is absent. Used for both Artifactory
// storage-API reads and artifact downloads.
func (f *File) AuthHeaders(registryURL, scheme string) map[string]string {
	e, _ := f.Get(registryURL)
	switch scheme {
	case SchemeBearer:
		if e.Pat != "" {
			return map[string]string{"Authorization": "Bearer " + e.Pat}
		}
	case SchemeBasic:
		if e.Pat != "" {
			raw := e.Username + ":" + e.Pat
			return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))}
		}
	case SchemeAPIKey:
		if e.APIKey != "" {
			return map[string]string{"X-JFrog-Art-Api": e.APIKey}
		}
	}
	return nil
}

// Delete removes the entire credential entry (OAuth + PAT + GitHub token) for
// registryURL.
func (f *File) Delete(registryURL string) {
	delete(f.Registries, normalise(registryURL))
}

// SetGitHubToken stores a GitHub token alongside any existing registry
// credentials for registryURL.
func (f *File) SetGitHubToken(registryURL, githubToken string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	e.GitHubToken = githubToken
	if e.SavedAt == "" {
		e.SavedAt = nowRFC3339()
	}
	f.Registries[key] = e
}

// ─── Env var resolution ──────────────────────────────────────────────────────

// ConsumerEnvBearer returns the bearer to send from env on the consumer side.
func ConsumerEnvBearer() string {
	return strings.TrimSpace(os.Getenv("FGLPKG_TOKEN"))
}

// ─── Bearer resolution ───────────────────────────────────────────────────────

// Refresher is the subset of internal/oauth used by ActiveBearer. Tests stub
// this out so they don't need a live registry.
type Refresher func(ctx context.Context, base string, prev oauth.Tokens) (oauth.Tokens, error)

// OAuthSkew is how close to ExpiresAt we treat an OAuth token as expired.
// Exposed for tests; do not rely on this from production code.
var OAuthSkew = 30 * time.Second

// ActiveBearer returns the bearer to send when talking to the consumer
// registry at registryURL.
//
// Priority:
//  1. ConsumerEnvBearer()
//  2. Unexpired OAuth access_token from credentials.json.
//  3. Refresh the OAuth token; if successful, persist + use it.
//  4. PAT from credentials.json.
//  5. "" — caller should treat as anonymous.
//
// If a refresh succeeds, the credentials file is rewritten in place.
// `refresh` may be nil in unit tests that don't exercise the OAuth path; in
// that case the refresh step is skipped.
func ActiveBearer(ctx context.Context, home, registryURL string, refresh Refresher) (string, error) {
	if t := ConsumerEnvBearer(); t != "" {
		return t, nil
	}
	f, err := Load(home)
	if err != nil {
		return "", err
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return "", nil
	}
	if e.OAuth != nil {
		if !oauth.Expired(*e.OAuth, OAuthSkew) {
			return e.OAuth.AccessToken, nil
		}
		if e.OAuth.RefreshToken != "" && refresh != nil {
			fresh, err := refresh(ctx, registryURL, *e.OAuth)
			if err == nil {
				e.OAuth = &fresh
				e.SavedAt = nowRFC3339()
				f.Registries[normalise(registryURL)] = e
				_ = f.Save(home)
				return fresh.AccessToken, nil
			}
			// fall through to PAT
		}
	}
	if e.Pat != "" {
		return e.Pat, nil
	}
	return "", nil
}

// ForceRefresh ignores the OAuth expiry check and runs the refresh flow if
// a refresh_token is on file for registryURL. Used by the registry HTTP
// client's one-shot 401-retry path. Returns true iff the refresh succeeded
// and the new tokens were persisted.
func ForceRefresh(ctx context.Context, home, registryURL string, refresh Refresher) bool {
	if refresh == nil {
		return false
	}
	f, err := Load(home)
	if err != nil {
		return false
	}
	e, ok := f.Get(registryURL)
	if !ok || e.OAuth == nil || e.OAuth.RefreshToken == "" {
		return false
	}
	fresh, err := refresh(ctx, registryURL, *e.OAuth)
	if err != nil {
		return false
	}
	e.OAuth = &fresh
	e.SavedAt = nowRFC3339()
	f.Registries[normalise(registryURL)] = e
	return f.Save(home) == nil
}

// ActivePublishBearer returns the bearer for publisher-side calls.
// env > stored PAT > stored legacy token > "".
func ActivePublishBearer(home, registryURL string) string {
	if t := ConsumerEnvBearer(); t != "" {
		return t
	}
	f, err := Load(home)
	if err != nil {
		return ""
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return ""
	}
	if e.Pat != "" {
		return e.Pat
	}
	return e.Token // already migrated to Pat on Load, but be defensive
}

// TokenFor is the legacy single-bearer resolver. Kept for callers that
// haven't moved to ActiveBearer / ActivePublishBearer yet.
//
// Deprecated: prefer ActiveBearer or ActivePublishBearer.
func TokenFor(home, registryURL string) string {
	if t := ConsumerEnvBearer(); t != "" {
		return t
	}
	f, err := Load(home)
	if err != nil {
		return ""
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return ""
	}
	if e.Pat != "" {
		return e.Pat
	}
	return e.Token
}

// GitHubTokenFor returns the GitHub token stored for the given registry.
func GitHubTokenFor(home, registryURL string) string {
	f, err := Load(home)
	if err != nil {
		return ""
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return ""
	}
	return e.GitHubToken
}

// normalise lowercases and strips trailing slashes from a registry URL so
// "https://Registry.Example.com/" and "https://registry.example.com" map to
// the same key.
func normalise(u string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(u)), "/")
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
