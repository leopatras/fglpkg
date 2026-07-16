# Spec: Align fglpkg with fglpkg-cli (registry URL, auth, whoami)

**Status:** ✅ Implemented — shipped (single GI registry base, OAuth auth-code+PKCE, `whoami`)
**Date:** 2026-05-29
**Author:** Mike Folcher
**Tracking:** Cross-repo alignment with [fglpkg-cli](https://github.com/4js-ai/fglpkg-cli)

---

## Summary

Bring fglpkg's **consumer-facing** behaviour into line with the TypeScript fglpkg-cli a co-worker has been shipping, so a developer who knows one CLI knows the other. Three areas change:

1. **Registry URL** — consumer commands default to `https://service.generointelligence.ai` (the cli's registry); **publisher commands keep defaulting to `https://fglpkg-registry.fly.dev`** until that endpoint set is confirmed on the new server. Two distinct defaults, two distinct env overrides.
2. **Authentication** — add OAuth (auth code + PKCE + DCR) as the default `fglpkg login` flow against the consumer registry; add `--token <PAT>` for non-interactive. Introduce `FGLPKG_TOKEN` as the consumer bearer env var (with `FGLPKG_PUBLISH_TOKEN` accepted as a back-compat fallback on the consumer side). `FGLPKG_PUBLISH_TOKEN` stays as the canonical publisher bearer.
3. **whoami** — call `/registry/whoami` (with `/auth/whoami` fallback), render the four-line cli-style output.

Nothing fglpkg can do today is removed. All publisher/owner/workspace/sbom/audit/etc. commands are untouched in behaviour; some just continue to point at the old URL by default.

## Motivation

A co-worker built [fglpkg-cli](https://github.com/4js-ai/fglpkg-cli) — a Node consumer CLI talking to a Cloudflare-backed registry at `service.generointelligence.ai`. The server already supports the OAuth code+PKCE flow with dynamic client registration (`/register`, `/authorize`, `/token`) and a richer whoami (`/registry/whoami`). Our Go CLI talks to a different default registry, uses a different env var name, prompts interactively for everything, and renders whoami in its own format. Two CLIs against the same backend should agree on these basics or users get confused which token works where.

## Goals

- `fglpkg` defaults to the same registry URL **and protocol** as `fglpkg-cli`. Consumer read-side endpoints (`GET /registry/packages`, `GET /registry/packages/<slug>`, `GET /registry/whoami`) become the canonical client calls; download URLs come from the artifact response directly (no GitHub-Releases indirection on the consumer side).
- `fglpkg login` (no args) opens a browser and runs OAuth + PKCE end-to-end.
- `fglpkg login --token <gpr_…>` stores a PAT non-interactively. Token must start with `gpr_` (warn but accept anything else, in case the prefix changes).
- `FGLPKG_TOKEN` env var works as a bearer override; takes precedence over `FGLPKG_PUBLISH_TOKEN` (which keeps working for CI back-compat).
- `fglpkg whoami` output matches fglpkg-cli's four-line format when the new endpoint is available; falls back to the old endpoint + old format on 404.
- OAuth access tokens are refreshed silently when expired (with a 30-second skew); failed refresh falls through to PAT.
- One-shot 401 retry on authenticated API requests: a 401 triggers a refresh attempt, then re-tries the original request once.
- Credentials file is forward-compatible: new fields added, old `token` field still readable.

## Non-goals

- **Removing functionality.** GitHub-token storage (`githubToken` field), `FGLPKG_PUBLISH_TOKEN`, `FGLPKG_GITHUB_TOKEN`, `FGLPKG_GITHUB_REPO`, `publish`, `pack`, `owner`, `token`, `config`, `workspace`/`ws`, `run`, `bdl`, `docs`, `outdated`, `info`/`view`, `sbom`, `init` — all stay.
- **Renaming or repackaging the CLI.** Still `fglpkg`, still distributed as a Go binary.
- **Server-side work.** This spec covers the client only. The OAuth endpoints, the `/registry/whoami` endpoint, and the `gpr_` PAT prefix are assumed to exist on `service.generointelligence.ai`.
- **Device-code OAuth flow** (for headless / SSH boxes). On fglpkg-cli's v1.1 roadmap; not implemented here.
- **Hidden-input prompt** when typing a PAT. fglpkg-cli has the same gap; future ticket.

## Behaviour

### 1. Split default registries

`defaultRegistry()` is now the **consumer** registry; a new `defaultPublishRegistry()` is the **publisher** registry. Both live in [internal/cli/cli.go](../internal/cli/cli.go).

```go
// Consumer (install, search, audit, info, outdated, list, env, whoami, login, logout).
// Env override: FGLPKG_REGISTRY.
func defaultRegistry() string {
    if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
        return strings.TrimRight(r, "/")
    }
    return "https://service.generointelligence.ai" // was fglpkg-registry.fly.dev
}

// Publisher (publish, unpublish, owner, token, config).
// Env override: FGLPKG_PUBLISH_REGISTRY (new), then FGLPKG_REGISTRY as a fallback
// for existing single-registry setups, then the old default.
func defaultPublishRegistry() string {
    if r := os.Getenv("FGLPKG_PUBLISH_REGISTRY"); r != "" {
        return strings.TrimRight(r, "/")
    }
    if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
        return strings.TrimRight(r, "/")
    }
    return "https://fglpkg-registry.fly.dev"
}
```

**Note on `FGLPKG_REGISTRY` fallback for publishers**: today's users who set only `FGLPKG_REGISTRY` and run both consumer and publisher commands keep working — the publisher half honours that env if it's the only one set. New users running against the new registry don't set it (defaults handle them). Users hosting their own registry and publishing to it set `FGLPKG_REGISTRY` once and it covers both halves.

Commands that switch from `defaultRegistry()` to `defaultPublishRegistry()`:
- `cmdPublish` ([cli.go:515](../internal/cli/cli.go))
- `cmdUnpublish` ([cli.go:1024](../internal/cli/cli.go))
- `cmdOwner` / `cmdOwnerList` / `cmdOwnerAdd` / `cmdOwnerRemove` ([cli.go:1177+](../internal/cli/cli.go))
- `cmdToken` ([cli.go:1270](../internal/cli/cli.go))
- `cmdConfig` and its subcommands that talk to the registry ([cli.go:1350+](../internal/cli/cli.go))

All other call sites of `defaultRegistry()` stay as-is and pick up the new consumer default.

### 2. Bearer env var

The bearer resolution is split per side. The consumer side gains a new canonical env var; the publisher side keeps the existing one.

```go
// internal/credentials/credentials.go

// ConsumerEnvBearer is used by all read-side commands (install, search, audit, etc).
// FGLPKG_TOKEN is canonical; FGLPKG_PUBLISH_TOKEN is accepted as a back-compat
// fallback so existing single-token CI scripts keep working.
func ConsumerEnvBearer() string {
    if t := strings.TrimSpace(os.Getenv("FGLPKG_TOKEN")); t != "" { return t }
    if t := strings.TrimSpace(os.Getenv("FGLPKG_PUBLISH_TOKEN")); t != "" { return t }
    return ""
}

// PublisherEnvBearer is used by publish/unpublish/owner/token/config.
// Unchanged from today: only FGLPKG_PUBLISH_TOKEN is honoured.
func PublisherEnvBearer() string {
    return strings.TrimSpace(os.Getenv("FGLPKG_PUBLISH_TOKEN"))
}
```

`TokenFor(home, url)` no longer auto-reads any env var — callers pass in the relevant `EnvBearer()` value explicitly. (Or, callers use `ActiveBearer` / `ActivePublishBearer`, see §4 below.)

The README env-var table gains an entry for `FGLPKG_TOKEN` (consumer) and `FGLPKG_PUBLISH_REGISTRY` (publisher). `FGLPKG_PUBLISH_TOKEN` is documented as "canonical for publish commands; also accepted as a back-compat fallback by consumer commands".

### 3. OAuth tokens — new package `internal/oauth`

A new package adds the browser auth-code + PKCE flow. Files:

```
internal/oauth/
├── pkce.go          # S256 verifier + challenge per RFC 7636
├── flow.go          # DCR + authorize URL + token exchange + refresh
├── server.go        # loopback HTTP server for the /callback hop
├── browser.go       # open(url) per-OS (open / xdg-open / rundll32 url.dll)
└── *_test.go
```

Public surface:

```go
type Tokens struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    ExpiresAt    time.Time `json:"expires_at"`
    Scope        string    `json:"scope"`
    ClientID     string    `json:"client_id"`
    ClientSecret string    `json:"client_secret,omitempty"`
}

// RunLogin opens the browser, runs auth code + PKCE against base,
// returns the resulting tokens. base is the registry URL (no trailing slash).
func RunLogin(ctx context.Context, base, scope string) (Tokens, error)

// Refresh exchanges a refresh token for a new access token.
// Returns the new tokens (refresh_token may rotate).
func Refresh(ctx context.Context, base string, prev Tokens) (Tokens, error)

// Expired reports whether t is within skew of its ExpiresAt.
func Expired(t Tokens, skew time.Duration) bool
```

Flow detail (matches fglpkg-cli/src/lib/oauth/flow.ts):

1. Bind a TCP listener on `127.0.0.1:0` and read back the chosen port. Form `redirect_uri = http://127.0.0.1:<port>/callback`.
2. POST to `<base>/register` with `client_name=fglpkg`, `redirect_uris=[redirect_uri]`, `token_endpoint_auth_method=none`, `grant_types=[authorization_code, refresh_token]`, `response_types=[code]`. Read `client_id` (and `client_secret` if returned).
3. Generate a 32-byte verifier (`base64url(rand)`), challenge = `base64url(sha256(verifier))`, and a random `state`.
4. Open the system browser at `<base>/authorize?response_type=code&client_id=…&redirect_uri=…&scope=…&state=…&code_challenge=…&code_challenge_method=S256`.
5. The loopback server accepts `GET /callback?code=…&state=…`, writes a small "you can close this window" HTML page, and signals the waiting goroutine.
6. Verify state matches; POST to `<base>/token` with `grant_type=authorization_code, code, redirect_uri, client_id, code_verifier` (and `client_secret` if DCR returned one). Parse `access_token, refresh_token, expires_in, scope`.
7. Persist tokens (caller's responsibility).

Cancellation: `RunLogin` honours `ctx` and on cancel shuts the loopback server. Ctrl-C therefore exits cleanly.

No new go.mod deps — everything uses `net/http`, `crypto/rand`, `crypto/sha256`, `encoding/base64`, `encoding/json`, `os/exec`, stdlib only.

### 4. Credentials schema — additive

[internal/credentials/credentials.go](../internal/credentials/credentials.go) `Entry` grows two fields:

```go
type Entry struct {
    OAuth        *oauth.Tokens `json:"oauth,omitempty"`
    Pat          string        `json:"pat,omitempty"`
    Token        string        `json:"token,omitempty"`        // legacy alias for Pat, kept for read
    Username     string        `json:"username,omitempty"`
    GitHubToken  string        `json:"githubToken,omitempty"`
    SavedAt      string        `json:"savedAt"`
}
```

On `Load`:
- If `Token != ""` and `Pat == ""`, copy `Token → Pat` and clear `Token`. (One-shot migration; next `Save` writes the new shape.)

On `Save`:
- `Token` field is always omitted (zero value).
- Mode stays `0600`.

New accessors:

```go
// ActiveBearer returns the bearer to send when talking to the consumer registry.
// Priority: ConsumerEnvBearer() > unexpired OAuth > refresh OAuth > stored PAT > "".
// May rewrite the credentials file if a refresh succeeded.
func ActiveBearer(ctx context.Context, home, registryURL string) (string, error)

// ActivePublishBearer returns the bearer to send when talking to the publisher
// registry. Priority: PublisherEnvBearer() > stored PAT (no OAuth — the
// publisher endpoints are PAT-only today). Never refreshes.
func ActivePublishBearer(home, registryURL string) string
```

`TokenFor(home, url)` stays for the (rare) callers that just want the raw stored PAT for back-compat; new code uses `ActiveBearer` / `ActivePublishBearer`.

### 5. `fglpkg login`

[internal/cli/cli.go](../internal/cli/cli.go) `cmdLogin` rewritten:

```
Usage: fglpkg login [--token <PAT>]

  --token <PAT>   Skip the browser; store the supplied PAT. PAT should start
                  with "gpr_" (warning printed if it does not, but stored
                  anyway).

With no flags, opens a browser, completes OAuth + PKCE, and stores the
resulting access + refresh tokens. The browser shows a small confirmation
page and the CLI exits as soon as the callback arrives.
```

Behaviour:
- Registry URL = `defaultRegistry()` (no prompt).
- `--token <PAT>`: warn if PAT doesn't start with `gpr_`; store PAT; call whoami to verify and print confirmation; on whoami failure print a warning but still keep the token stored (so offline CI bootstrap works).
- No flags: run `oauth.RunLogin`; on success, persist tokens; call whoami; print `✓ Logged in to <url> as <email> (<partner>)`.
- The interactive prompts (registry URL + optional GitHub token) are removed. GitHub token continues to be settable via `FGLPKG_GITHUB_TOKEN` env var; for users that need it in `credentials.json`, a follow-up `fglpkg config set github-token <token>` is on the v1.1 list (out of scope here).

### 6. `fglpkg logout`

- Drops the registry URL prompt. Uses `defaultRegistry()`.
- Clears the entire registry entry (OAuth + PAT) — same behaviour as fglpkg-cli's logout.

### 7. `fglpkg whoami`

`whoamiRequest` is replaced with a richer call:

```go
type WhoAmI struct {
    User    struct{ ID, Email, Name string } `json:"user"`
    Partner *struct{ ID, Name string }       `json:"partner"`
    Scopes  []string                         `json:"scopes"`
    // legacy fallback when calling /auth/whoami:
    Username string                          `json:"username,omitempty"`
}

// First try GET /registry/whoami. On 404, retry GET /auth/whoami
// and synthesise {User.Name: Username}.
func whoamiRequest(ctx context.Context, registryURL, bearer string) (WhoAmI, error)
```

Output (new endpoint available):

```
Registry: https://service.generointelligence.ai
User:     Jane Developer <jane@acme.com>
Partner:  ACME
Scopes:   registry:read
```

Output (legacy endpoint — only `username` available):

```
Registry: https://fglpkg-registry.fly.dev
User:     alice
Partner:  (none)
Scopes:   (none)
```

The existing `GitHub token: configured` / `GitHub token: not configured` line is kept (appended after the four-line block) because that's functionality fglpkg-cli doesn't have but we still need.

### 8. Registry HTTP client — protocol port

`internal/registry/registry.go` is rewritten so consumer functions speak the new protocol against the consumer registry; publisher-related calls keep speaking the old protocol against the publish registry.

**Two base URL helpers** (in addition to the `default*Registry()` already added in §1):

```go
// consumerBase returns the URL for /registry/packages, /registry/whoami, etc.
func consumerBase() string {
    if r := os.Getenv("FGLPKG_REGISTRY"); r != "" { return strings.TrimRight(r, "/") }
    return "https://service.generointelligence.ai"
}

// publisherBase mirrors defaultPublishRegistry() in cli.go.
func publisherBase() string {
    if r := os.Getenv("FGLPKG_PUBLISH_REGISTRY"); r != "" { return strings.TrimRight(r, "/") }
    if r := os.Getenv("FGLPKG_REGISTRY"); r != "" { return strings.TrimRight(r, "/") }
    return "https://fglpkg-registry.fly.dev"
}
```

(In practice `consumerBase` and the cli's `defaultRegistry()` return the same value — keeping them separate avoids a cli→registry dependency loop. The constants are kept in sync via tests.)

**New consumer types** (mirror fglpkg-cli/src/types.ts):

```go
type Artifact struct {
    Variant     string `json:"variant"`     // "default", "genero6", etc.
    Filename    string `json:"filename"`
    SizeBytes   int64  `json:"size_bytes"`
    SHA256      string `json:"sha256"`
    DownloadURL string `json:"download_url"`
}

type VersionSummary struct {
    Version       string              `json:"version"`
    Status        string              `json:"status"` // "approved", "withdrawn", etc.
    Changelog     string              `json:"changelog"`
    Tags          map[string][]string `json:"tags"`
    Artifacts     []Artifact          `json:"artifacts"`
    SubmittedAt   string              `json:"submitted_at"`
    PublishedAt   string              `json:"published_at"`
    ReviewComment string              `json:"review_comment"`
}

type ListedPackage struct {
    Slug          string              `json:"slug"`
    Name          string              `json:"name"`
    Description   string              `json:"description"`
    Visibility    string              `json:"visibility"`
    Owner         struct{ PartnerID, Name string } `json:"owner"`
    Status        string              `json:"status"` // "active" | "deprecated"
    LatestVersion string              `json:"latest_version"`
    Downloads     int64               `json:"downloads"`
    Tags          map[string][]string `json:"tags"`
}

type PackageDetail struct {
    ListedPackage
    Versions []VersionSummary `json:"versions"`
}

type BrowseResponse struct {
    Packages []ListedPackage `json:"packages"`
    Page     int             `json:"page"`
    PageSize int             `json:"pageSize"`
    Total    int             `json:"total"`
}
```

**Function rewrite** — public API is preserved where callers can; new fetches use the new endpoint:

| Public fn (unchanged signature)                    | Old impl                              | New impl                                                                              |
|----------------------------------------------------|---------------------------------------|---------------------------------------------------------------------------------------|
| `FetchVersionList(name) (*VersionList, error)`     | `GET /packages/<name>/versions`       | `GET /registry/packages/<name>`, project `versions[].version`                          |
| `FetchInfo(name, version) (*PackageInfo, error)`   | `GET /packages/<name>/<version>`      | `GET /registry/packages/<name>`, find matching version, pick artifact (variant=default) |
| `FetchInfoForGenero(name, version, gMajor)`        | `GET /packages/<name>/<version>?genero=N` | same fetch, pick artifact whose `variant == "genero<N>"`; fall back to `"default"` |
| `Resolve(name, constraint, gMajor)`                | composite                             | one fetch then local resolution                                                       |
| `Search(term) ([]SearchResult, error)`             | `GET /search?q=…`                     | `GET /registry/packages?q=…`, map `ListedPackage` to `SearchResult`                   |
| `FetchConfig() (*RegistryConfig, error)`           | `GET /config` (against old default)   | `GET /config` against **publisher** base — moved to publisher API since the new registry doesn't expose this |

`PackageInfo.DownloadURL` is populated from the matching `Artifact.DownloadURL`. The URL may be absolute (R2-hosted) or relative (`/registry/packages/<slug>/versions/<v>/artifacts/<variant>`); callers normalise via `strings.HasPrefix(url, "http")`.

`PackageInfo.Checksum` is the artifact `sha256`.

`PackageInfo.JavaDeps` / `FGLDeps` / `GeneroConstraint` / `Variants` — the new endpoint does not return these. They are populated as empty when calling the new registry. Callers that depend on them (e.g., the resolver for transitive deps) keep working when the user points at the old registry via `FGLPKG_REGISTRY`; against the new registry, transitive resolution is a degraded no-op (matches fglpkg-cli v1 limitations).

**HTTP authentication** — new client helper:

```go
// httpGetAuthed fetches u and returns the body. bearer is sent as
// Authorization: Bearer ... when non-empty.
func httpGetAuthed(u, bearer string) ([]byte, error)
```

Consumer fetches resolve the bearer via `credentials.ActiveBearer(...)` and pass it through. Anonymous reads (search, public package info) work with an empty bearer.

### 9. Installer — registry-token-aware downloads

`internal/installer/installer.go` gains a `registryToken string` field on `Installer`. `downloadAndVerify` sends `Authorization: Bearer <registryToken>` when:
- URL is not a GitHub URL (existing GitHub branch unchanged — it sends the GitHub token), **and**
- `registryToken` is non-empty.

`newInstaller(home)` in [cli.go:2039](../internal/cli/cli.go) resolves the registry token via `credentials.ActiveBearer(ctx, globalHome, registryURL)` and passes it to `installer.New(...)`. The GitHub-token resolution stays as-is.

`resolveGitHubRepo` ([cli.go:2018](../internal/cli/cli.go)) is updated:
- Still honours `FGLPKG_GITHUB_REPO` as the primary source.
- The registry-config fallback now calls the **publisher** `FetchConfig()` (because that's where the GitHub-repos config lives). If the user is publishing to a registry that doesn't expose `/config` (e.g., self-hosted with no GH integration), they must set `FGLPKG_GITHUB_REPO` explicitly. Same as today's behaviour — error message gains a hint to set `FGLPKG_PUBLISH_REGISTRY` if pointing at a non-default registry.

### 10. Authenticated API requests — 401 retry with refresh

`authDo` (the http helper that adds the bearer) wraps every request so that:

1. Resolve current bearer via `ActiveBearer`.
2. Send the request.
3. If response is `401` AND OAuth tokens are present AND we haven't retried, attempt a refresh; if refresh succeeds, persist and re-send the request once.
4. Return whatever the second response is.

This covers clock skew and server-side revocation without forcing the user to re-login.

## CLI surface change summary

| Command         | Before                                              | After                                                  |
|-----------------|-----------------------------------------------------|--------------------------------------------------------|
| `login`         | prompts for URL + token (+ optional GH token)       | `--token <PAT>` for PAT; no args = browser OAuth (consumer reg) |
| `logout`        | prompts for URL                                     | no prompt; clears the consumer registry entry          |
| `whoami`        | one line: `Logged in to <url> as <user>` + GH line  | 4-line cli format + GH line (consumer reg)             |
| `publish`       | targeted `defaultRegistry()`                        | targeted `defaultPublishRegistry()` (old URL by default) |
| `unpublish`     | targeted `defaultRegistry()`                        | targeted `defaultPublishRegistry()`                    |
| `owner *`       | targeted `defaultRegistry()`                        | targeted `defaultPublishRegistry()`                    |
| `token *`       | targeted `defaultRegistry()`                        | targeted `defaultPublishRegistry()`                    |
| `config *`      | targeted `defaultRegistry()` (where applicable)     | targeted `defaultPublishRegistry()`                    |

| Var                       | Before                       | After                                                                 |
|---------------------------|------------------------------|-----------------------------------------------------------------------|
| consumer default URL      | `https://fglpkg-registry.fly.dev` | `https://service.generointelligence.ai`                        |
| publisher default URL     | `https://fglpkg-registry.fly.dev` | unchanged — `https://fglpkg-registry.fly.dev`                   |
| consumer URL env override | `FGLPKG_REGISTRY`            | unchanged                                                             |
| publisher URL env override| (none)                       | `FGLPKG_PUBLISH_REGISTRY` (new); falls back to `FGLPKG_REGISTRY`      |
| consumer token env        | `FGLPKG_PUBLISH_TOKEN`       | `FGLPKG_TOKEN` (canonical) + `FGLPKG_PUBLISH_TOKEN` (back-compat fallback) |
| publisher token env       | `FGLPKG_PUBLISH_TOKEN`       | unchanged                                                             |

All other commands (install, search, audit, info, outdated, list, env, pack, sbom, init, run, bdl, docs, completion, version, help, workspace/ws) are unchanged in behaviour; they just pick up the new consumer default URL.

## Tests

- `internal/oauth/pkce_test.go` — verifier length, base64url charset, S256 challenge matches RFC 7636 §4.6 worked example.
- `internal/oauth/flow_test.go` — DCR + token exchange against a `httptest.Server` standing in for the registry. Refresh round-trip.
- `internal/oauth/server_test.go` — loopback callback success + state-mismatch rejection.
- `internal/credentials/credentials_test.go` — added cases:
  - legacy `token` field migrates to `pat` on load.
  - `FGLPKG_TOKEN` env wins over `FGLPKG_PUBLISH_TOKEN` env wins over stored PAT.
  - `ActiveBearer` refreshes an expired OAuth token and persists the rotation.
  - `ActiveBearer` falls through to PAT when refresh fails.
- `internal/cli/whoami_test.go` — calls `httptest.Server`:
  - `/registry/whoami` returns 200 → new format.
  - `/registry/whoami` returns 404 → falls back to `/auth/whoami`, prints legacy-shaped data into the new layout.
  - 401 with refreshable OAuth → refresh + retry succeeds.
- `internal/cli/login_test.go` — `--token` happy path; PAT prefix warning when not `gpr_`.
- `internal/registry/registry_test.go` — protocol port:
  - `FetchVersionList("foo")` against a fake `/registry/packages/foo` returns the versions array projected from the new response shape.
  - `FetchInfoForGenero("foo", "1.0.0", "6")` picks the artifact whose `variant == "genero6"`, falls back to `"default"`.
  - `Search("x")` against `/registry/packages?q=x` maps `BrowseResponse` → `[]SearchResult` correctly.
  - `httpGetAuthed` sends `Authorization: Bearer …` when bearer is non-empty.

No browser-flow test — that requires opening a real browser; covered manually.

## Migration / back-compat

- Existing `credentials.json` with `{token: "…"}` per registry is read transparently. First subsequent `Save` rewrites it with `{pat: "…"}`. No user action required.
- Existing CI scripts setting `FGLPKG_PUBLISH_TOKEN`:
  - Publisher commands (`publish`, `unpublish`, `owner`, `token`, `config`) — unaffected; same env var, same default URL.
  - Consumer commands (`install`, `search`, `audit`, etc.) — `FGLPKG_PUBLISH_TOKEN` is still honoured as a fallback when `FGLPKG_TOKEN` isn't set, so they keep working too.
- **Consumer commands now talk to a new registry by default.** Users still expecting `install`/`search`/`audit` against `fglpkg-registry.fly.dev` must set `FGLPKG_REGISTRY=https://fglpkg-registry.fly.dev`. Call this out in the release notes for the next version.
- **Publisher commands still talk to the old registry by default**, so no migration is needed for the publish side. When the new server's publish surface is ready, the maintainer flips `defaultPublishRegistry()` in a follow-up change; that's a one-line patch.
- Users hosting their own all-in-one registry (publish + consume on one URL) set `FGLPKG_REGISTRY` once and it covers both halves — see the fallback logic in `defaultPublishRegistry()`.
- Anyone who had typed their token into the interactive prompt before is unaffected: stored credentials still resolve, just under the new schema after first write.

## Roll-out

One commit / one PR is fine — the changes are tightly coupled (login, logout, whoami, credentials schema, http auth retry all reference each other) and splitting would create awkward intermediate states. The PR description points at this spec.

Version bump from 2.0.2 → 2.1.0 (minor: new feature, backwards-compatible storage).

## Open questions

- The PAT prefix `gpr_` — is it final, or is the registry still settling on it? If it might change, drop the warning and just accept whatever the user pastes. **Default in this spec: warn-but-accept**, so it's forgiving either way.
- `fglpkg.lock` shape difference — fglpkg-cli writes `lockfileVersion: 1` and `packages[].installedAt`; we write `version: 1, generatedAt, generoVersion, rootManifest, packages, jars`. These are written by different sides of the same project (consumer vs publisher), and there's no immediate reason to converge. Flagging here for awareness but **not in scope** for this spec.
- Future: `fglpkg config set github-token <token>` to replace the dropped interactive prompt. Out of scope; tracked separately.
