# Spec: Artifactory as a secondary package repository

**Status:** ◐ Partial — GIS-249. Phases 1–2 (consume + publish) **merged to `main` 2026-07-15** via PR #12 (commits `60581f5`, `65e0c5d`, `b32291c`, `f82ada1`). The security-critical core — multi-provider routing, the hard collision guard, native-SHA-256 integrity, fail-closed 401/403 handling — shipped and matches this spec. Four open issues found in the post-merge review (§18), one a security fix. Phase 3 not started. Design decisions resolved 2026-07-10 (§17).
**Date:** 2026-07-10
**Author:** Mike Folcher
**Motivation:** A customer hosts their **internal** BDL packages in their own **JFrog
Artifactory** instance and does not want to use the Genero Intelligence (GI) registry's
*private-package* feature — but they **do** want to keep pulling **public** packages from the
GI registry. fglpkg is single-source today (one GI registry via `FGLPKG_REGISTRY`), so there
is no way to draw internal packages from Artifactory while still drawing public packages from GI.
**Related:** [specs/dependency-crosscheck-fallback.md](dependency-crosscheck-fallback.md)
(`LockedJAR.Source` provenance, the pattern this extends to packages),
[specs/package-signing.md](package-signing.md) (provenance/integrity trust model),
[security/threat-model.md](../security/threat-model.md) (dependency-confusion is a named threat),
[docs/market-readiness-gaps.md](../docs/market-readiness-gaps.md#L229) (§6 "corporate-mirror"
open question — this is that customer).

---

## Summary

Add support for **one or more secondary package repositories** backed by **JFrog Artifactory**,
alongside the existing GI registry, for **FGL/BDL packages** (both consuming and publishing).
The design is:

1. **A repositories config** — a new `registries` block (in `fglpkg.json`, with a
   `~/.fglpkg/config.json` global fallback) listing named, priority-ordered repositories. The
   built-in GI registry is always present unless overridden. Credentials never live here.
2. **A `Provider` abstraction** — the current GI client and a new Artifactory client both
   implement one interface (`FetchVersions` / `FetchInfo` / `Search` / `Publish`). A multi-provider
   front-end plugs into the resolver's existing injectable-fetcher seam
   ([`resolver.NewWithFetchers`](../internal/resolver/resolver.go#L186)).
3. **An Artifactory generic-repo layout** for FGL package zips + a sidecar `fglpkg.json`, with
   version discovery via the Artifactory storage API and integrity via Artifactory's native
   SHA-256 checksums.
4. **Priority-order routing with a hard collision guard** — a package name that resolves in
   **more than one** repository is a hard error demanding an explicit pin; there is no silent
   precedence. This closes the dependency-confusion hole that a naïve fallback chain would open.
5. **Lockfile source-pinning** — each locked package records the repository it resolved from,
   making installs reproducible and preventing a later-appearing collision from silently
   re-routing a dependency.

**Scope this spec commits to:** FGL/BDL packages only, consume **and** publish, auth via JFrog
access token (Bearer) / HTTP Basic (`user:password` or `user:token`) / API key
(`X-JFrog-Art-Api`) / anonymous read. **Everything is client-side** — no GI backend change.
(Basic and the anonymous-disabled default were confirmed against a live JFrog Cloud trial —
see [§8](#8-authentication).)

**Out of scope** (see [§16](#16-non-goals)): routing Java JARs through Artifactory (they stay on
Maven Central), Maven/npm/Docker Artifactory repo types, transitive POM
resolution, scoped names, offline cache.

## 1. What the customer actually wants (grounding)

| | Source | fglpkg behaviour today | Desired |
|---|---|---|---|
| **Public** BDL packages | GI registry | ✅ works | ✅ keep |
| **Private/internal** BDL packages | ~~GI private visibility~~ → **their Artifactory** | GI-private only | **Artifactory** |
| Java JARs | Maven Central | ✅ works | ✅ keep on Maven Central (this spec) |

The customer is simply **not a GI private tenant**, so the GI registry already yields them only
public packages (private packages 404 to non-members — see
[README.md](../README.md) "Private Packages"). Their private surface moves wholesale to
Artifactory. No "turn off GI private" switch is needed; it falls out of them not authenticating
to GI as a tenant member.

## 2. Current architecture — the single-source seams

Where the change lands (all verified in the tree):

- **One registry base.** [`registry.registryBase()`](../internal/registry/registry.go#L533)
  returns `FGLPKG_REGISTRY` or the hardcoded `service.generointelligence.ai`. Every consumer
  call (`FetchVersionList`, `FetchInfoForGenero`, `Search`, publish) is a **package-level
  function** that reads that single base.
- **Global auth hooks.** [`registry.Bearer` / `registry.TryRefresh`](../internal/registry/registry.go#L37)
  are process-global function pointers, wired once in
  [`cli.init`](../internal/cli/cli.go#L34) to the single default registry's credentials.
- **Injectable resolver.** [`resolver.NewWithFetchers`](../internal/resolver/resolver.go#L186)
  already accepts a `VersionFetcher` + `InfoFetcher`. The live path
  ([`registryVersions` / `registryInfo`](../internal/resolver/resolver.go#L594)) is a thin
  wrapper over the GI client. **This is the seam the multi-provider front-end plugs into.**
- **Two-case download auth.** [`installer.downloadAndVerify`](../internal/installer/installer.go#L471)
  branches only on *GitHub-URL → github token* vs *else → registry token*. It must generalize to
  *match the URL's host against a configured repo, apply that repo's auth scheme*.
- **URL-keyed credentials.** [`credentials.json`](../internal/credentials/credentials.go) is
  already a `map[registryURL]Entry`. Adding Artifactory entries is natural; only a new
  **auth-scheme** notion is required.
- **Lockfile.** [`LockedPackage`](../internal/lockfile/lockfile.go#L77) pins name / version /
  `DownloadURL` / checksum but records **no source repository**. That field is the anti-confusion
  pin ([§9](#9-lockfile-source-pinning)).
- **No config file exists today** — env vars + `credentials.json` only. We are introducing the
  first one.

## 3. Design overview

```
              fglpkg.json .registries  +  ~/.fglpkg/config.json  (+ FGLPKG_REGISTRY)
                                   │  cascade → ordered repo list
                                   ▼
        ┌──────────────────────────────────────────────────────────┐
        │                    RepositorySet                          │
        │   [ gi (genero, prio 1) , acme-int (artifactory, prio 2) ]│
        └──────────────────────────────────────────────────────────┘
             │ implements VersionFetcher / InfoFetcher (routing + collision guard)
             ▼
   resolver.NewWithFetchers(...)  ──►  Plan{ Packages[ {…, Source:"acme-int"} ], JARs }
             │                                    │
             ▼                                    ▼
   per-name routing:                       lockfile: records Source per package
     • pinned (manifest/lock) → that repo         │
     • else query ALL repos:                      ▼
         0 hits → not found              installer.downloadAndVerify
         1 hit  → use + pin                (auth scheme chosen by URL-host match)
        ≥2 hits → COLLISION ERROR
             │
   ┌─────────┴───────────┐
   ▼                     ▼
 GeneroProvider     ArtifactoryProvider
 (/registry/… API)  (/api/storage + generic-repo layout)
```

## 4. Configuration model

### 4.1 The `registries` block

A repository is declared by a **descriptor** (no secrets):

```jsonc
{
  "name": "acme-internal",          // logical id; used in --registry, lock, credentials key
  "type": "artifactory",            // "genero" | "artifactory"
  "url": "https://artifactory.acme.example/artifactory",  // base (incl. context path)
  "repoKey": "fgl-internal-generic",// Artifactory repo key (generic repo). Required for type=artifactory
  "priority": 2,                    // lower = tried first; ties are an error at load
  "auth": "basic",                  // "bearer" | "basic" | "apikey" | "anonymous"  (default "bearer")
  "packages": ["acme-*"]            // OPTIONAL name-scope filter (§7.4). Omit = owns any name.
}
```

The built-in GI registry is injected as if declared:

```jsonc
{ "name": "gi", "type": "genero",
  "url": "https://service.generointelligence.ai", "priority": 1, "auth": "bearer" }
```

`FGLPKG_REGISTRY`, if set, overrides the **`gi` entry's `url`** (back-compat: existing single-registry
users are unaffected and unaware of the new block).

### 4.2 Cascade & where it lives

Resolved in increasing precedence, later wins per `name`:

1. **Built-in default** — the `gi` entry above.
2. **Global** — `~/.fglpkg/config.json` (`{"registries": [...]}`). Machine-wide; an ops team can
   provision the Artifactory entry here once so every project inherits it.
3. **Project** — a `registries` array in `fglpkg.json`. **Committed**, so `git clone && fglpkg
   install` gives a teammate the Artifactory URL automatically; only their *credentials* are
   per-developer. (This mirrors Maven's `pom.xml <repositories>` + user `settings.xml` split.)

Entries merge by `name`; a project entry named `gi` can retarget the default registry.

**Credentials never appear in any of these.** They stay in
[`~/.fglpkg/credentials.json`](../internal/credentials/credentials.go), keyed by the repo `url`,
written only by `fglpkg login` ([§8](#8-authentication)).

### 4.3 New package `internal/config`

```go
type Registry struct {
    Name     string   `json:"name"`
    Type     string   `json:"type"`      // "genero" | "artifactory"
    URL      string   `json:"url"`
    RepoKey  string   `json:"repoKey,omitempty"`
    Priority int      `json:"priority,omitempty"`
    Auth     string   `json:"auth,omitempty"`     // bearer|basic|apikey|anonymous
    Packages []string `json:"packages,omitempty"` // optional glob allow-list
}

// Load merges built-in + global + project (fglpkg.json), validates, and returns
// the priority-sorted set. Errors on duplicate priorities, unknown type, or an
// artifactory entry missing repoKey.
func Load(projectDir string) ([]Registry, error)
```

The `fglpkg.json` [manifest struct](../internal/manifest/manifest.go#L24) gains one field
(`Registries []config.Registry json:"registries,omitempty"`) and the
[JSON schema](../schema/fglpkg.schema.json) gains the matching definition.

## 5. Provider abstraction

New interface (in `internal/registry`, or a new `internal/provider`):

```go
type Provider interface {
    Name() string   // logical repo name, e.g. "acme-internal"

    // FetchVersions lists available versions + their genero constraints.
    // Returns provider.ErrNotFound (wrapping registry.ErrNotFound) if the name
    // is absent — the routing layer relies on this to count hits.
    FetchVersions(name string) ([]resolver.CandidateVersion, error)

    // FetchInfo returns full metadata + an absolute download URL for name@version,
    // variant-selected by generoMajor ("" = default).
    FetchInfo(name, version, generoMajor string) (*registry.PackageInfo, error)

    Search(term string) ([]registry.SearchResult, error)

    // Publish uploads one built variant. Genero uses the /registry submit flow;
    // Artifactory does a direct PUT (§10).
    Publish(req PublishRequest) error
}
```

Two implementations:

- **`GeneroProvider`** — wraps today's [`registry`](../internal/registry/registry.go) functions.
  A near-mechanical extraction: the package-level funcs move behind a struct that carries its own
  base URL + bearer resolver instead of reading process globals. (The globals can stay as a thin
  shim during migration.)
- **`ArtifactoryProvider`** — new; [§7](#7-artifactory-fgl-package-layout).

`registry.PackageInfo` gains a `Source string` (the resolving provider's `Name()`), threaded
through `ResolvedPackage.Source` into the lock.

## 6. Routing & the collision guard

The `RepositorySet` implements `VersionFetcher`/`InfoFetcher` for the resolver and encodes routing.

```
resolve(name):
  if name is PINNED (manifest dep has "registry:", OR lock has a Source):
        query only that provider          # deterministic short-circuit
  else:
        candidates = providers whose "packages" filter admits name   # §7.4 (all, if none set)
        hits = [ p for p in candidates if p.FetchVersions(name) != NotFound ]
        switch len(hits):
          0 -> ErrNotFound (aggregated: "not found in gi, acme-internal")
          1 -> resolve from hits[0]; record Source
          ≥2 -> COLLISION ERROR  (see below)
```

**Collision error** (the security-critical case — a name present in ≥2 repos):

```
error: package "utils" is available from more than one repository:
    gi            1.2.0, 1.3.0
    acme-internal 0.9.0
  Refusing to guess. Pin the source in fglpkg.json:
      "dependencies": { "fgl": { "utils": { "version": "^1.0.0", "registry": "acme-internal" } } }
  or rename so the name is unique to one repository.
```

**Per-dependency pin** — an FGL dependency value may be the string form (`"^1.0.0"`, unchanged)
**or** an object:

```jsonc
"dependencies": { "fgl": {
  "logft":  "^2.0.0",                                        // unpinned → routed
  "utils":  { "version": "^1.0.0", "registry": "acme-internal" }  // pinned
}}
```

This is an additive change to [`Dependencies.UnmarshalJSON`](../internal/manifest/manifest.go#L180)
(accept string or object per entry) and to the `dependencies.fgl` schema (a `oneOf`).

**Why query all repos (no first-match short-circuit)?** Detecting a collision *requires* knowing
whether a second repo also has the name. For the customer's 2-repo setup that is one extra
`FetchVersions` per unpinned package — cheap, and skipped entirely once the lock exists (locked
installs read `Source` and go straight to one repo). The optional `packages` allow-list
([§7.4](#74-optional-name-scope-filter)) prunes the fan-out further. Transitive FGL deps of a
package inherit routing the same way (each resolved once, cached in `state.resolved`).

## 7. Artifactory FGL package layout

Artifactory does not speak the GI `/registry/…` protocol, so fglpkg defines a layout in an
Artifactory **generic** repository (`repoKey`). Generic repos are path-addressable blob stores
with a REST listing/metadata API — a clean fit.

### 7.1 Path layout

```
{url}/{repoKey}/{name}/{version}/{name}-{version}-genero{N}.zip      # one per Genero major variant
{url}/{repoKey}/{name}/{version}/fglpkg.json                          # version metadata sidecar
```

Example: `…/fgl-internal-generic/acme-utils/1.2.0/acme-utils-1.2.0-genero6.zip`.
The `-genero{N}` suffix reuses the existing variant scheme
([`pickArtifact`](../internal/registry/registry.go#L506) selects `genero<major>`); a
webcomponent variant would be `-webcomponent.zip` if ever needed (out of scope here).

### 7.2 Version discovery — storage API

`GET {url}/api/storage/{repoKey}/{name}` returns Folder Info; the `children` array (folders) are
the versions:

```jsonc
// GET …/api/storage/fgl-internal-generic/acme-utils
{ "repo": "fgl-internal-generic", "path": "/acme-utils",
  "children": [ { "uri": "/1.1.0", "folder": true }, { "uri": "/1.2.0", "folder": true } ] }
```

A `404` here ⇒ the package is absent in this repo ⇒ `provider.ErrNotFound` (drives the hit-count
in [§6](#6-routing--the-collision-guard)). A `401`/`403` is an **auth failure**, surfaced as a hard
error — **never** folded into "absent". This matters for the collision guard: a mis-configured or
expired credential must not silently drop a repo from the hit-count and let a package mis-route.
(The trial instance returns `401` on an unauthenticated read, so this is a live code path, not
theoretical.) Each `uri` is parsed as a semver; non-semver folders
are skipped with a warning. Genero constraints per version come from the sidecar (§7.3), so the
`CandidateVersion.GeneroConstraint` is filled after a cheap metadata read (or lazily during
`FetchInfo`).

### 7.3 Metadata & variant listing

`GET {url}/api/storage/{repoKey}/{name}/{version}` lists the version folder; the `.zip` children
reveal which `genero{N}` variants exist. The **sidecar `fglpkg.json`**
(`GET {url}/{repoKey}/{name}/{version}/fglpkg.json`) supplies dependencies + genero constraint —
the analog of GI's per-version `dependencies`/`genero` fields
([`apiVersionSummary`](../internal/registry/registry.go#L428)). Reusing the on-disk manifest format
means the publish step just uploads the package's own `fglpkg.json` verbatim (§10), and the
consumer parses it with the existing [`manifest`](../internal/manifest/manifest.go) package.

### 7.4 Optional name-scope filter

A repo descriptor may declare `"packages": ["acme-*", "internal-*"]` (globs). Names not matching
are never queried against that repo — an optimization *and* an intent declaration ("this repo owns
the `acme-*` namespace"). Omitted ⇒ the repo is consulted for any name. This is a lightweight
stand-in for scoped names (gap #14) without a registry-schema change.

### 7.5 Integrity — native SHA-256

Artifactory computes and stores SHA-256 at deploy time (v5.5+) and returns it in File Info
(real response from the trial instance, trimmed):

```jsonc
// GET …/api/storage/GeneroBDL/acme-utils/1.2.0/acme-utils-1.2.0-genero6.zip
{ "downloadUri": "https://…/artifactory/GeneroBDL/acme-utils/1.2.0/acme-utils-1.2.0-genero6.zip",
  "mimeType": "application/zip", "size": "869",
  "checksums":         { "sha1": "1a6e…", "md5": "b581…", "sha256": "ef4416…" },
  "originalChecksums": { "sha256": "ef4416…" } }  // ← the X-Checksum-Sha256 the client sent, verified on deploy
```

`FetchInfo` reads `checksums.sha256` into [`PackageInfo.Checksum`](../internal/registry/registry.go#L61)
and `downloadUri` into `DownloadURL`. The existing streaming
[`downloadAndVerify`](../internal/installer/installer.go#L471) then verifies the SHA-256 with **no
manual checksum field** — parity with GI. The download response also carries
`X-Checksum-Sha256` / `-Sha1` / `-Md5` headers, so integrity can be checked even without the prior
storage-info call.

> **Validated against the live trial** (`trialflprhv.jfrog.io`, 2026-07-10) — confirmed exactly as
> written: `/api/storage` Folder Info `children[].{uri,folder}`; File Info `checksums.sha256` +
> `downloadUri` + `size`; `PUT` deploy → `201` with `originalChecksums` echoing the client
> `X-Checksum-Sha256` (Artifactory verifies it on receipt); download-back byte-identical with
> `X-Checksum-*` headers; `404` on an absent path, `204` on delete. The E2E smoke
> ([§15](#15-test-plan)) automates this round-trip.

## 8. Authentication

Four schemes. **Validated against a live JFrog Cloud trial** (`trialflprhv.jfrog.io`, 2026-07-10):
that instance requires auth for *every* read (anonymous **disabled**) and advertises
`WWW-Authenticate: Basic realm="Artifactory Realm"` — so **Basic is a first-class scheme**, not a
future add. A JFrog access token can be sent either as `Authorization: Bearer <token>` **or** as
the Basic password (`user:<token>`); Artifactory accepts Bearer even when it advertises Basic.

| `auth` | Header sent | Notes |
|---|---|---|
| `bearer` | `Authorization: Bearer <access-token>` | **Recommended.** JFrog access/identity token; no username needed. |
| `basic` | `Authorization: Basic base64(user:secret)` | `secret` = password **or** access token. This is the JFrog "Set Me Up" `curl -u` form and what the trial instance uses. |
| `apikey` | `X-JFrog-Art-Api: <key>` | Supported; **JFrog API keys are deprecated / EOL in newer Artifactory** — recommend migrating to access tokens. |
| `anonymous` | *(none)* | Only if the repo permits unauthenticated read — **often disabled** (it is on the trial). Login still needed to **publish**. |

### 8.1 Credential storage

[`credentials.Entry`](../internal/credentials/credentials.go#L49) already carries `Username` and the
`Pat` bearer slot; it gains one field for the API-key scheme. The scheme itself comes from the repo
descriptor, so the entry only holds the secret(s):

```go
// APIKey is the JFrog X-JFrog-Art-Api key, when the repo's auth scheme is "apikey".
// bearer/basic reuse the existing Pat (secret) + Username slots.
APIKey string `json:"apiKey,omitempty"`
```

Keyed by repo `url` exactly as today:
- **bearer** — access token in the existing `Pat` slot; `ActiveBearer` already resolves it.
- **basic** — `Username` + secret (`Pat`); the secret may be an account password **or** an access token.
- **apikey** — `APIKey`.

### 8.2 `fglpkg login` / `logout` gain `--registry`

```bash
fglpkg login  --registry acme-internal                                 # prompts for the scheme's secret
fglpkg login  --registry acme-internal --token <access-token>          # CI, bearer
fglpkg login  --registry acme-internal --user <u> --password <p|token> # CI, basic
fglpkg login  --registry acme-internal --api-key <key>                 # CI, apikey
fglpkg logout --registry acme-internal
```

Default (`--registry` omitted) targets `gi`, preserving today's behaviour. `anonymous` repos need
no login. The prompt/flags are chosen by the repo descriptor's `auth` scheme.

### 8.3 Installer auth generalization

Replace the two-case switch in
[`downloadAndVerify`](../internal/installer/installer.go#L471) with a small **auth resolver**: given
a download URL, find the configured repo whose `url` is a prefix of it and apply that repo's scheme
(bearer → `Authorization: Bearer`, basic → `Authorization: Basic`, apikey → `X-JFrog-Art-Api`, anonymous → none). The existing GitHub-token
redirect-preserving path is retained for the legacy GitHub-Releases URLs. The installer is
constructed with the resolved repo set instead of a single `(githubToken, registryToken)` pair
([`newInstaller`](../internal/cli/cli.go#L2160)).

## 9. Lockfile source-pinning

Add one field to [`LockedPackage`](../internal/lockfile/lockfile.go#L77) (and, symmetrically, the
`Registry`/source notion to `LockedWebcomponent`):

```go
// Registry is the logical repository this package resolved from ("gi",
// "acme-internal"). Empty = the default GI registry, so pre-Artifactory locks
// parse unchanged (additive, omitempty — no lockfileVersion bump).
Registry string `json:"registry,omitempty"`
```

Set by [`FromPlan`](../internal/lockfile/lockfile.go#L162) from `ResolvedPackage.Source`. This is the
**dependency-confusion pin**:

- **`install` from an existing lock** fetches each package from its recorded `Registry` — a locked
  internal package can never be silently re-routed to a public repo that later squats its name.
- **`update` / re-resolve** re-runs routing; a name that has newly appeared in a *second* repo now
  trips the collision guard ([§6](#6-routing--the-collision-guard)) instead of flipping sources.
- A lock referencing a `Registry` name absent from the current config is a clear error ("locked
  package X came from repository 'acme-internal', which is not configured").

This directly extends the provenance pattern established for JARs in
[dependency-crosscheck-fallback.md §8](dependency-crosscheck-fallback.md) (`LockedJAR.Source`).

## 10. Publishing to Artifactory

`fglpkg publish --registry acme-internal` reuses the entire existing build path — the
[`pack`](../internal/cli/pack.go) flow and per-Genero-major variant loop are unchanged — then swaps
the GI 5-step submit protocol for a direct deploy:

1. Build the variant zip(s) exactly as today; SHA-256 each.
2. `PUT {url}/{repoKey}/{name}/{version}/{name}-{version}-genero{N}.zip` — body = zip, auth header
   per scheme, `X-Checksum-Sha256: <hex>`. Artifactory verifies it on receipt (confirmed on the
   trial: the deploy response echoes it as `originalChecksums`, and a mismatch is rejected).
3. `PUT {url}/{repoKey}/{name}/{version}/fglpkg.json` — the package manifest, for consumer metadata.
4. **No pending/approval step** (that is GI-specific; Artifactory access is governed by Artifactory
   permissions) and **no `visibility`** (the whole repo's access is an Artifactory concern —
   `visibility` in the manifest is ignored with a note when publishing to Artifactory).

**Overwrite policy.** Publishing a *new* variant under an existing version is fine (additive, like
GI). Re-publishing an *existing* variant is guarded by fglpkg: a re-`PUT` of the same path returns
`201` and **silently overwrites** (confirmed on the trial — a standard generic repo does *not* stop
a clobber), so a storage-info pre-check refuses it unless `--force` is passed (or the repo is
configured immutable, which returns `409` and we surface it). `publish --dry-run` prints the exact
PUT URLs without touching the network, mirroring today.

## 11. Consuming commands

| Command | Behaviour change |
|---|---|
| `install` / `update` | Multi-provider resolve + routing + collision guard; lock records `Registry`. `--registry <name>` optionally restricts resolution to one repo. |
| `search <term>` | Fan-out to **all** repos, merge, **tag each result with its source repo**; dedupe by name (collisions shown as "in gi and acme-internal"). |
| `info <pkg>` | Resolve via routing; show the owning repo in the output. |
| `outdated` | Check each FGL package against **its own** source repo (uses the locked `Registry`). |
| `audit` | Unchanged mechanism (OSV by coordinate). **Caveat:** private Artifactory packages are not in OSV, so they report no advisories — documented, not a silent gap. |
| `registry list` *(new)* | Print configured repos, priority, auth scheme, and login status. `registry add`/`remove` may follow; editing config by hand works in the meantime. |

Env: no new *required* vars. `FGLPKG_REGISTRY` keeps overriding the `gi` URL. (A future
`FGLPKG_REGISTRY_<NAME>_TOKEN` convention for CI could be added if needed, but `--token` on
`login` covers CI today.)

## 12. Security posture — dependency confusion

This is the central risk of any secondary-repository feature (Birsan-style dependency confusion),
and it is a named threat in [threat-model.md](../security/threat-model.md). Defenses, in depth:

1. **Hard collision guard ([§6](#6-routing--the-collision-guard))** — a name in ≥2 repos never
   resolves silently; it errors until pinned. There is *no* "internal wins" or "public wins"
   default to exploit.
2. **Lockfile source-pinning ([§9](#9-lockfile-source-pinning))** — once resolved, a package's
   repository is frozen; a later squat can't re-route it.
3. **Per-dependency pin + optional `packages` allow-list ([§7.4](#74-optional-name-scope-filter))** —
   let a team declare intent ("`acme-*` is ours") so the public registry is never even consulted for
   internal names.
4. **Integrity** — Artifactory's native SHA-256 is verified on every download
   ([§7.5](#75-integrity--native-sha-256)). This is *transport/storage* integrity, **not**
   provenance; package **signing** ([package-signing.md](package-signing.md)) is the provenance
   story and can extend to Artifactory-hosted zips later (they can carry the same detached
   signatures — no re-signing at the mirror).

**Recommendation to the customer:** give internal packages a distinctive prefix (e.g. `acme-`) and
set the `packages` allow-list on the Artifactory repo. That makes the public/internal split
*structural*, not merely ordered — the strongest available defense short of scoped names.

## 13. CLI & config surface (summary)

```bash
# config (fglpkg.json)         # global (~/.fglpkg/config.json) — same shape
"registries": [ { "name": "acme-internal", "type": "artifactory",
                  "url": "https://artifactory.acme.example/artifactory",
                  "repoKey": "fgl-internal-generic", "auth": "bearer",
                  "packages": ["acme-*"] } ]

fglpkg login  --registry acme-internal [--token … | --api-key …]
fglpkg install                          # routes acme-* → Artifactory, rest → GI
fglpkg publish --registry acme-internal [--dry-run] [--force]
fglpkg search foo                        # merged, source-tagged
fglpkg registry list                     # configured repos + auth status
```

## 14. Rollout phases

| Phase | Status | Content |
|---|---|---|
| **1 — Consume** | ✅ Merged 2026-07-15 (PR #12) — gaps in §18 | `internal/config` + cascade; `Provider` interface + `GeneroProvider` extraction; `ArtifactoryProvider` (storage-API discovery, sidecar metadata, SHA-256); routing + collision guard; auth schemes + `login --registry`; installer auth generalization; lock `Registry` pin. Delivers install from Artifactory — **but `info`/`outdated` are still GI-only, `search` lacks dedup, and `update --registry` is unwired (ISSUE-C).** |
| **2 — Publish** | ✅ Merged 2026-07-15 (PR #12) | `publish --registry` deploy path (PUT zip + sidecar, checksum header, overwrite guard, dry-run). |
| **3 — Later (not committed)** | 📋 Not started | JARs via an Artifactory Maven repo (global Maven-base override + Maven auth branch — see §16); `registry add/remove` (**requested 2026-07-15 — pull forward, ISSUE-H**); AQL-based search for very large repos; signing for Artifactory artifacts. |

## 15. Test plan

- **Unit — config cascade:** built-in ⊕ global ⊕ project merge by name; duplicate-priority error;
  missing `repoKey` on `type=artifactory`; `FGLPKG_REGISTRY` retargets `gi`.
- **Unit — routing/collision (table-driven):** {name in gi only, art only, both, neither} ×
  {pinned, unpinned} → {resolve-from-X, collision-error, not-found}. Include transitive-dep
  inheritance and the `packages` allow-list pruning.
- **Unit — Artifactory client:** parse Folder Info `children` → versions; parse File Info
  `checksums.sha256`/`downloadUri`; variant selection from the version-folder listing; auth-header
  selection per scheme. Driven by **recorded JSON fixtures**.
- **Unit — lock round-trip:** `Registry` set/empty; empty = GI back-compat; byte-identical lock to
  today when no Artifactory repo is configured.
- **Integration — mock Artifactory (`httptest`):** serve `/api/storage/*` + download + `PUT`
  endpoints → resolve+install a package end-to-end; assert collision errors; assert `publish` issues
  the right PUTs with the checksum header; assert source-tagged `search`.
- **E2E smoke — real instance (offered):** stand up Artifactory (JFrog free tier, or
  `docker run releases-docker.jfrog.io/jfrog/artifactory-jcr`/OSS), create a generic repo,
  `fglpkg publish --registry` a fixture package (mirroring the fgl-log4j smoke in
  [webcomponent-packages.md §](webcomponent-packages.md)), then `fglpkg install` it into a clean
  home and run the compiled result. **This is what pins the exact REST shapes from
  [§7](#7-artifactory-fgl-package-layout).** The full round-trip (deploy zip + sidecar → version
  discovery → File Info → download-back → delete) was **manually validated against a JFrog Cloud
  trial on 2026-07-10** under both `bearer` and `basic`; the automated smoke re-runs it in CI and
  adds `apikey` where a key is provisioned.

## 16. Non-goals

- **Java JARs through Artifactory.** Out of scope by decision — JARs stay on Maven Central. The
  hooks already exist for a future add: the per-dep `url` override
  ([`MavenURL`](../internal/manifest/manifest.go#L272)) and the schema's mirror note
  ([schema:213](../schema/fglpkg.schema.json#L213)); a global Maven-base override + a Maven auth
  branch in `downloadAndVerify` would complete it (Phase 3). Artifactory's *native* Maven repo would
  serve JARs directly then.
- **Other Artifactory repo types** (Maven/npm/Docker/PyPI) and **AQL** as the primary discovery
  mechanism (storage API suffices; AQL is a Phase-3 scale option).
- **Transitive POM resolution** — fglpkg has none today and this spec adds none.
- **Scoped names (`@scope/name`, gap #14)** — the `packages` allow-list is the interim; true scopes
  are a separate registry-schema effort.
- **GI backend changes** — none; entirely client-side.
- **Provenance/signing for Artifactory artifacts** — deferred to
  [package-signing.md](package-signing.md).
- **Offline / air-gapped cache** (gap #28).

## 17. Decisions — resolved (2026-07-10)

All design decisions are settled; this section is retained as a decision log for the implementer.
Each landed on the recommended option.

1. **Config location → manifest + global fallback.** ✅ Descriptors live in a `registries` block in
   `fglpkg.json` (committed, team-shared) with `~/.fglpkg/config.json` as a machine-wide fallback
   ([§4.2](#42-cascade--where-it-lives)); credentials stay in `credentials.json`. Chosen for
   zero-touch teammate onboarding.
2. **Collision → hard error.** ✅ A name resolvable from ≥2 repos aborts with a disambiguation
   message; there is **no** global precedence knob ([§6](#6-routing--the-collision-guard)). Follows
   from the chosen routing model.
3. **Collision escape hatch → per-dependency inline pin.** ✅ `dependencies.fgl` accepts an object
   form `{ "version": …, "registry": … }` alongside the plain string ([§6](#6-routing--the-collision-guard)).
   This is the *only* override — deliberately no priority-wins precedence mode, so the
   dependency-confusion guard cannot be globally disabled.
4. **Publish overwrite → guarded, `--force` to override.** ✅ A storage-info pre-check refuses to
   clobber an existing variant unless `--force` is passed; new variants under an existing version
   remain additive ([§10](#10-publishing-to-artifactory)). Justified by the confirmed
   silent-overwrite behaviour.
5. **`type` value → `"artifactory"`.** ✅ A single value now; a future *subtype* field (not a new
   `type` string) would carry Maven/JAR support if it lands ([§16](#16-non-goals)).
6. **E2E instance → done.** ✅ JFrog Cloud trial (`trialflprhv.jfrog.io`, generic repo `GeneroBDL`)
   validated [§7](#7-artifactory-fgl-package-layout)'s full round-trip on 2026-07-10 (§7.5 / §15).
7. **Auth schemes → settled.** ✅ `bearer` + `basic` + `apikey` + `anonymous`
   ([§8](#8-authentication)); Basic was promoted to first-class after the trial showed the instance
   advertises `Basic` and disables anonymous read.

## 18. Post-merge status & open issues (reviewed 2026-07-15)

Phases 1–2 landed on `main` via **PR #12** (`60581f5` "Add JFrog Artifactory as a secondary package
repository", `65e0c5d` "Complete Artifactory routing, publish defaults, and docs", `b32291c`
"resolver: defer registry collisions so declared pins win regardless of order", `f82ada1` "Fix user
message and add fglpkg.json reference doc"). This section is the delta between what shipped and this
spec, from a section-by-section code review.

### 18.1 Shipped and matching the spec ✅

- **§4 configuration model** — `registries` block in `fglpkg.json` + `~/.fglpkg/config.json` global
  fallback + built-in `gi`; merge-by-name cascade; duplicate-priority / unknown-type / missing-`repoKey`
  validation. (`internal/config/config.go`, `internal/manifest/manifest.go`, `schema/fglpkg.schema.json`.)
- **§5 / §7 provider + Artifactory client** — `Provider` interface, `GeneroProvider`,
  `ArtifactoryProvider`; storage-API version discovery, sidecar `fglpkg.json` metadata, path layout,
  `packages` glob filter, native SHA-256 integrity. (`internal/provider/*`.)
- **§7.2 fail-closed auth** — 401/403 is a hard error; only 404 counts as "absent"
  (`internal/provider/artifactory.go`), with a regression test. This is the property that stops a
  mis-configured or expired credential from silently dropping a repo out of the collision hit-count.
- **§6 routing + collision guard** — query-all-then-count → 0/1/≥2 = not-found / resolve+pin / hard
  collision error; per-dependency inline pin (`{version, registry}`); no global precedence knob.
  (`internal/provider/repositoryset.go`, `internal/manifest/manifest.go`.) The `b32291c` change
  **improves** on the spec: a collision is deferred until the resolve queue drains, so the verdict no
  longer depends on Go's randomized map-iteration order — the guard itself is unchanged, only *when* it
  renders its verdict. An unpinned name in ≥2 repos still hard-errors (regression-tested).
- **§8 auth schemes** — bearer / basic / apikey / anonymous header construction; `credentials.APIKey`;
  URL-keyed storage. (`internal/credentials/credentials.go`.)
- **§10 publish** — Artifactory deploy (PUT zip + sidecar, `X-Checksum-Sha256`, overwrite guard +
  `--force`, `--dry-run`, no approval step, `visibility` ignored with a note).
  (`internal/provider/artifactory.go`, `internal/cli/cli.go`.)
- **§8.2 / §11** — `login`/`logout --registry`; `registry list` command.

### 18.2 Open issues found in review

**Ticket routing (2026-07-15):** ISSUE-A → **GIS-267** (Security Defect — *fixed*). ISSUE-E/F/G →
**GIS-268**, with their own spec [search-metadata-and-keywords.md](search-metadata-and-keywords.md).
ISSUE-B, C, D and H remain under the Artifactory ticket **GIS-249**.

**ISSUE-A (→ GIS-267, FIXED 2026-07-15) — 🔴 security: GI bearer token is sent to an `anonymous` secondary repo.**
`installer.downloadAndVerify` applies the matched repo's auth headers only when it produced some
(`len(repoHeaders) > 0`). An `anonymous` repo produces none, so the request falls through to the
`!isGH && registryToken != ""` branch and attaches the user's **GI bearer token to the secondary
(Artifactory) host** — a cross-host credential exposure. §8/§8.3 require anonymous → no header. It
triggers only when a repo is configured `anonymous` while a GI token is present (the mainline customer
uses `bearer`/`basic`), but it is a real leak. **Fix:** scope the `registryToken` branch to the GI base
URL (or to `type=genero` repos), not "any non-GitHub URL"; add a test asserting an anonymous download
carries no `Authorization`. (`internal/installer/installer.go`.)

**Fix (shipped):** `matchRepoAuth` now also returns whether a configured repo matched;
`buildRepositorySet` registers a `RepoAuth` entry for every secondary repo including anonymous ones;
`downloadAndVerify` applies the matched repo's headers (possibly none) and never falls through to the
GI token. Regression tests in `internal/installer/download_auth_test.go`.

**ISSUE-B — 🟠 the lock `Registry` field is write-only; §6/§9 lock-pinning is unwired.**
`FromPlan` writes `LockedPackage.Registry` / `LockedWebcomponent.Registry`, but nothing reads it.
Consequently (a) §6's "*or the lock has a Source* → query only that provider" short-circuit does not
exist — `update`/re-resolve routes from manifest pins only; and (b) §9's "a lock referencing a
`Registry` absent from the current config is a clear error" is **missing** — a lock naming a removed
repo installs silently. The anti-confusion guarantee for locked installs still holds via the existing
absolute-`DownloadURL`+checksum pin, so this is robustness/correctness, not an active hole. **Fix:**
read `Registry` in `installFromLock` to short-circuit routing to that provider, and add the
absent-registry check to `lockfile.Validate`. (`internal/lockfile/lockfile.go`,
`internal/installer/installer.go`.)

**Fix (shipped):** the lock `Registry` is now read. `RepositorySet.VersionsFrom`
resolves a package against exactly its recorded source repository (bypassing the collision guard —
the §6 "lock has a Source → query only that provider" short-circuit); it backs `outdated` (ISSUE-C).
`LockFile.CheckRegistries` errors when a locked package/webcomponent names a repository absent from
the configured set; wired into `Installer` via `WithConfiguredRegistries` and checked before any
install-from-lock. Tests in `internal/lockfile/registry_pin_test.go`,
`internal/provider/repositoryset_test.go`.

**ISSUE-C — 🟠 several §11 consuming commands are not Artifactory-aware.**
The §11 table claims these route through the multi-provider set; they do not:
- `info <pkg>` and `outdated` still call the GI-only `registry.*` functions, so an Artifactory-sourced
  package is queried against GI (wrong / 404). (`internal/cli/info.go`, `internal/cli/outdated.go`.)
- `search` fans out and source-tags results but does **not** dedup by name or show the "in gi and
  acme-internal" collision line. (`internal/cli/cli.go`.)
- `update --registry <name>` errors (`parseInstallFlags` rejects the flag with no package argument);
  only `install --registry` works, though §11 groups both. (`internal/cli/cli.go`.)
**Fix:** route `info`/`outdated` through the `RepositorySet` (using each locked package's source); add
search dedup; accept `--registry` on `update`.

**Fix (shipped):** `info` routes through the `RepositorySet` (`infoVersionList`/
`infoFetch`) and prints the owning repo (`Source`); `outdated` checks each package against its locked
source via `VersionsFrom`; `search` dedups by name across providers, shows all sources for a colliding
name, and prints a collision note; `update --registry <name>` restricts re-resolution to one repo (the
"requires a package" rule moved from `parseInstallFlags` to `cmdInstall`). (`internal/cli/info.go`,
`internal/cli/outdated.go`, `internal/cli/cli.go`.)

**ISSUE-D — 🟡 decision needed: author-declared transitive pins can suppress a collision (broader than
decision #3).** A dependency's own manifest/sidecar can carry `FGLDepPins` that pin *its* transitive
deps and thereby quietly resolve a name present in ≥2 repos (`internal/provider/repositoryset.go`,
`DeclarePin`). Decision #3 (§17) states the *consumer's* inline pin is the only override. Rails exist
(consumer root pin wins; conflicting declared pins hard-error; an unpinned collision still errors), but
a trusted transitive author could steer a colliding name toward the public repo without the consumer's
acknowledgement. **Decide:** keep and document as intended, warn on it, or restrict overrides to
consumer pins only.

**Fix (shipped):** decision = *warn*. `RepositorySet.DeclarePin`
(`internal/provider/repositoryset.go`) now emits a one-time stderr warning naming the package and
registry whenever a **transitive** (author-declared) pin is recorded, advising an explicit pin in
`fglpkg.json` to confirm the source. The consumer's own root pin still wins silently (returns before
the warning) and a repeat of the same declared pin is idempotent (no re-warn). Rails unchanged.

The following three surfaced in follow-up CLI testing (2026-07-15):

**ISSUE-E (→ GIS-268) — 🟠 `search` shows no description/author for Artifactory results (fglpkg client; Artifactory-specific).**
`ArtifactoryProvider.Search` (`internal/provider/artifactory.go`, ~L203) returns `SearchResult{Name,
LatestVersion}` only — it never reads the version sidecar `fglpkg.json`, so `Description`/`Author` are
always blank for Artifactory-sourced rows. (The GI path fills these correctly, so this is Artifactory-
only.) **Fix:** in `Search`, best-effort fetch the latest version's sidecar and populate
Description/Author — one extra metadata read per hit, pruned by the `packages` allow-list; or defer the
enrichment to `info`. This is the Artifactory half of the "descriptions not appearing" report.

**Fix (shipped):** `Search` now stamps `Source` and, for each hit, best-effort reads
the latest version's sidecar via a new `fetchSidecar` helper (shared with `FetchInfo`) to populate
`Description`/`Author`. A missing/unparseable sidecar leaves the fields blank without failing the
search. (F/G remain GI-service work, tracked under GIS-268 in `search-metadata-and-keywords.md`.)

**ISSUE-F (→ GIS-268) — 🟡 package `description` is publish-write-once (fglpkg client + GI service; general, not Artifactory-specific).**
`registry.PublishCreatePackage` (`internal/registry/registry.go`, ~L281) sets the package `description`
only when it *creates* the slug; on republish the slug exists → `409` → no-op, and there is no
metadata-update call, so a description added or changed in `fglpkg.json` after the first publish never
reaches the registry (GI `POST /registry/packages` writes `description` once — `registry-routes.ts`
~L568). This is why a GI `search` can still show a blank description. **Fix:** add a GI owner-only
metadata update (e.g. `PATCH /registry/packages/:slug`) called from publish, or refresh the package
description from the latest approved version's manifest at approval time — GI-service work plus a small
client call. Related: [publish-rich-metadata.md](publish-rich-metadata.md).

**ISSUE-G (→ GIS-268) — 🟡 keywords are not searchable end-to-end (GI service + fglpkg client; general, not Artifactory-specific).**
Manifest `keywords` (`internal/manifest/manifest.go:45`) are collected and documented but (1) never
sent by publish, (2) not stored — `registry_packages` has no keywords column (GI
`migrations/0020_registry.sql`), and (3) not matched by the browse filter, which `LIKE`s only
`p.name` / `p.slug` / `p.description` (GI `registry-routes.ts` ~L312). So `fglpkg search <keyword>` can
never match on a keyword. **Fix (GI-led):** store package keywords (new column or join table), ingest
at publish, and add them to the `q` match; **client:** send `keywords` on publish (currently dropped).
Decide whether these map onto the existing `tag` facet system (`registry_version_tags`) or a dedicated
field. Best tracked with [genero-aware-search.md](genero-aware-search.md) (GIS-254) as a search-quality
item rather than as Artifactory work.

**ISSUE-H (→ GIS-249) — 🟢 add `fglpkg registry add`/`remove` (fglpkg client; pull forward from §14 Phase 3).**
Today `fglpkg registry` supports only `list` (`internal/cli/cli.go`, `cmdRegistry` ~L2369); `add` /
`remove` return "unknown registry subcommand". §11 and §14 deferred them to Phase 3 with "editing config
by hand works in the meantime," but testing confirmed hand-editing `~/.fglpkg/config.json` is the real
friction point. **Fix (client-only, no GI change):**
`fglpkg registry add <name> <url> [--type genero|artifactory] [--repo-key K] [--auth
bearer|basic|apikey|anonymous] [--priority N] [--packages 'acme-*']` writes a validated
`config.Registry` descriptor into `~/.fglpkg/config.json` (global) or `fglpkg.json` with `--project`;
`registry remove <name>` deletes it. Reuse `config.Registry` + its existing validation; credentials
still flow through `login --registry`.

**Fix (shipped):** `cmdRegistry` now dispatches `list`/`add`/`remove` (`rm` alias).
`registry add <name> <url>` (type defaults to `artifactory`) validates the descriptor against the
prospective effective set via `config.Resolve` (type/auth/repoKey + unique priority; priority
auto-assigned to max+1 when omitted), refuses a duplicate name and redefining the built-in `gi`, then
writes to `~/.fglpkg/config.json` (or the project `fglpkg.json` with `--project`). `registry remove`
deletes an entry (clearing `defaultRegistry` if it pointed there) and refuses to remove `gi`. New
config helpers `LoadGlobalFile`/`WriteGlobalFile` back the read-modify-write; credentials still flow
through `login --registry`.

### 18.3 Intentional divergences (not issues, noted for the record)

- The `Provider` interface omits `Publish`; it is a concrete method on `*ArtifactoryProvider`
  (documented in `provider.go`).
- `config.Load(home, fglpkgRegistry, projectRegistries)` differs from §4.3's `Load(projectDir)` — the
  caller pre-parses `fglpkg.json` to avoid a `manifest`→`config` import cycle.
- A new `defaultRegistry` manifest field + `FGLPKG_PUBLISH_REGISTRY` env select the publish target
  (additive to §11's "no new required vars").
- Extra config validations beyond §4.3's three (unknown `auth`, missing name/url, non-positive priority).
