# Spec: Genero Intelligence service — `fglpkg` latest-release endpoint

**Status:** 📋 Not started — GIS-256 (spec ready)
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** The fglpkg client's [self-update.md](self-update.md) needs a registry endpoint that
reports the latest fglpkg version and where to download the right binary. This spec defines that
endpoint on the Genero Intelligence (GI) service. Deliberately minimal: **the service only tracks a
version string and hands back GitHub download URLs** — the release binaries continue to live on
GitHub Releases; GI never hosts or proxies them.
**Companion spec:** [self-update.md](self-update.md) (the fglpkg client consumer of this endpoint).
**Related:** [gi-registry-workstream-c.md](gi-registry-workstream-c.md) (prior GI-side spec; same
repo, same conventions).

> **Repo note.** Unless a path is a workspace link, all `src/...`, `migrations/...`, and `file:line`
> citations refer to the **`4js-genero-intelligence`** repo (branch `package-management`, at
> `~/4js-bitbucket/4js-genero-intelligence`), **not** to fglpkg. fglpkg files are workspace links.

---

## Summary

Add one **public** read endpoint and one **admin** write endpoint to the registry router
([src/gateway/registry-routes.ts](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts)):

- `GET /registry/fglpkg/latest` — returns the latest stable fglpkg version, a per-platform GitHub
  download URL for each of the six release assets, the URL of the release's `checksums.txt` **and its
  detached Ed25519 signature** (`checksums.txt.sig`), plus an operator-configurable manual-download URL
  and instructions for the client's recovery path. Unauthenticated; cacheable.
- `PUT /registry/fglpkg/latest` — admin-only setter that records the current latest version (and the
  optional recovery URL / instructions) in a D1 config row. This is how the version is bumped when a
  release is cut — **no redeploy required**.

The service stores **only the version string** (plus optional release notes and recovery URL/
instructions). Every download URL — including the binaries, `checksums.txt`, and `checksums.txt.sig` —
is **derived** from that version via a fixed template, because GitHub Release assets follow a stable
naming scheme (`fglpkg-<os>-<arch>[.exe]` under `releases/download/v<version>/`). The repo coordinates
and URL template are Worker configuration, not data; GI never signs or holds keys.

## Background — grounding facts

### The router and its conventions

`registry` is a Hono app (`const registry = new Hono<HonoEnv>()`,
[registry-routes.ts:30](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L30))
mounted at `/registry`, with `HonoEnv = { Bindings: Env; Variables: { user, ctx } }`
([:28](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L28)). Handlers
read `c.env.DB` (D1) and return `c.json(obj, status)`.

- **Public GET precedent:** `/tags` ([:366](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L366))
  and `/packages` ([:290](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L290))
  serve unauthenticated. `/.well-known/keys.json`
  ([:274](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L274)) is the
  precedent for a **cacheable** response — it sets
  `Cache-Control: public, max-age=3600, s-maxage=300` on a raw `Response`. The new GET follows this.
- **Role guard precedent:** admin-gated writes use `requireRole(...)` from `../auth/middleware.js`
  ([:10](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L10)). Roles
  in play are `super_admin` / `admin` (checked directly, e.g.
  [:84](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L84)) and
  `PUBLISH_ROLES = ["partner_admin", "developer"]`
  ([:34](../../../4js-bitbucket/4js-genero-intelligence/src/gateway/registry-routes.ts#L34)). The
  setter here is **admin-only** — bumping the shipped CLI version is an operator action, not a
  partner/publisher one.

### Migrations are sequential

Latest is `0038_registry_provenance.sql`
([migrations/](../../../4js-bitbucket/4js-genero-intelligence/migrations/)). The new one is
`0039_fglpkg_release.sql`.

### The GitHub asset naming is fixed

The fglpkg release workflow ([.github/workflows/release.yml](../.github/workflows/release.yml)) builds
six binaries via [cmd/build.sh](../cmd/build.sh) named `fglpkg-<os>-<arch>[.exe]` and a
`checksums.txt`, uploaded to the GitHub Release for tag `v<version>`. So for a version `V`:

```
https://github.com/4js-mikefolcher/fglpkg/releases/download/v<V>/fglpkg-<os>-<arch>[.exe]
https://github.com/4js-mikefolcher/fglpkg/releases/download/v<V>/checksums.txt
```

is fully determined by `V`. The service never needs to store URLs — only `V`.

## Data model

`migrations/0039_fglpkg_release.sql` — a single-row config table (enforced by a fixed PK):

```sql
CREATE TABLE IF NOT EXISTS fglpkg_release (
  id           INTEGER PRIMARY KEY CHECK (id = 1),  -- single-row table
  version      TEXT NOT NULL,                        -- e.g. "3.4.0" (no leading v)
  notes_url    TEXT,                                 -- optional; defaults to the GH release tag page
  manual_url   TEXT,                                 -- optional; manual-download URL for the client recovery path (GIS-255 R2)
  instructions TEXT,                                 -- optional; human-readable recovery steps the client prints verbatim (R2)
  updated_by   TEXT,                                 -- user id/email of the admin who set it
  updated_at   TEXT NOT NULL                         -- ISO-8601
);
```

No seed row required — the GET handler treats "no row" as "unknown" (see below). An operator sets it
once via the PUT endpoint after this ships.

## Configuration (Worker env / `wrangler`)

Repo coordinates and the URL template are configuration, not data — they change far less often than
the version and never at runtime:

| Var | Default | Purpose |
|---|---|---|
| `FGLPKG_RELEASE_REPO` | `4js-mikefolcher/fglpkg` | `owner/repo` for building download URLs |
| `FGLPKG_RELEASE_BASE` | `https://github.com` | Host base (lets the download origin move without code changes) |

Asset filenames (`fglpkg-<os>-<arch>[.exe]`) are a hard-coded constant list in the handler — they are
part of the release contract, not deployment config.

## `GET /registry/fglpkg/latest`

Public, unauthenticated, cacheable.

**Behavior:**
1. Read the single `fglpkg_release` row.
2. If **absent** → `404 { "error": "No fglpkg release configured" }`. The client
   ([self-update.md](self-update.md)) already swallows a 404 as "no update info", so this is a safe
   pre-provisioning state.
3. Otherwise build the response by expanding the template over the fixed asset matrix.

**200 response:**

```json
{
  "version": "3.4.0",
  "notes": "https://github.com/4js-mikefolcher/fglpkg/releases/tag/v3.4.0",
  "checksumsUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/download/v3.4.0/checksums.txt",
  "checksumsSigUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/download/v3.4.0/checksums.txt.sig",
  "manualUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/tag/v3.4.0",
  "instructions": "Download the binary for your platform, verify its SHA-256, and replace your fglpkg executable.",
  "assets": [
    { "os": "linux",   "arch": "arm64", "url": ".../v3.4.0/fglpkg-linux-arm64" },
    { "os": "linux",   "arch": "amd64", "url": ".../v3.4.0/fglpkg-linux-amd64" },
    { "os": "darwin",  "arch": "arm64", "url": ".../v3.4.0/fglpkg-darwin-arm64" },
    { "os": "darwin",  "arch": "amd64", "url": ".../v3.4.0/fglpkg-darwin-amd64" },
    { "os": "windows", "arch": "arm64", "url": ".../v3.4.0/fglpkg-windows-arm64.exe" },
    { "os": "windows", "arch": "amd64", "url": ".../v3.4.0/fglpkg-windows-amd64.exe" }
  ]
}
```

- `os`/`arch` values are Go's `runtime.GOOS`/`runtime.GOARCH` spellings, so the client can match
  directly without translation.
- `checksumsUrl` points at GitHub's `checksums.txt` for the release — this is what lets the client's
  self-update perform its SHA-256 integrity check ([self-update.md § self-update flow](self-update.md))
  while GI still only "contains URLs".
- `checksumsSigUrl` points at the detached **Ed25519 signature** over `checksums.txt`
  (`checksums.txt.sig`, another release asset; its URL is derived like the others). This is the
  client's *authenticity* gate — it verifies the signature back to its pinned root before trusting the
  checksums ([self-update.md § Release signing & verification](self-update.md#release-signing--verification)).
  GI only provides the URL; it neither signs nor holds any key.
- `manualUrl` + `instructions` are the **operator-configurable recovery path** (GIS-255 R2): where to
  download by hand and human-readable steps, which the client prints verbatim when an update is blocked
  or fails. Both come from the config row (`manual_url` / `instructions`); `manualUrl` defaults to the
  release tag page when unset and `instructions` to a built-in default. Because they are data, the
  download location can move (e.g. off GitHub) without a client release.
- `notes` defaults to the release tag page unless `notes_url` overrides it.

**Optional single-asset form** (client convenience, not required by the client spec): if
`?os=<goos>&arch=<goarch>` are supplied, return only the matching asset:

```json
{ "version": "3.4.0", "os": "darwin", "arch": "arm64",
  "url": ".../v3.4.0/fglpkg-darwin-arm64", "checksumsUrl": "..." }
```

Unknown `os`/`arch` → `404 { "error": "No fglpkg binary for darwin/riscv64" }`.

**Caching:** set `Cache-Control: public, max-age=3600, s-maxage=300` (mirrors
`/.well-known/keys.json`). A daily-checking client and Cloudflare's edge cache mean GitHub and the
Worker see negligible load.

**No auth, no download counting.** This endpoint is metadata only; it neither streams bytes nor
touches `registry_packages.downloads`.

## `PUT /registry/fglpkg/latest`

Admin-only setter — `registry.put("/fglpkg/latest", requireRole("super_admin", "admin"), …)`.

**Request body:**

```json
{ "version": "3.4.0", "notesUrl": "https://…", "manualUrl": "https://…", "instructions": "…" }
// notesUrl, manualUrl, instructions all optional
```

**Behavior:**
1. Validate `version` is a plausible semver (`^\d+\.\d+\.\d+(-[\w.]+)?$`). Reject with `400`
   otherwise. Store **without** a leading `v`.
2. `INSERT … ON CONFLICT(id) DO UPDATE` the single row, stamping `updated_by` (from `c.get("user")`)
   and `updated_at`. `manualUrl` / `instructions` (GIS-255 R2) persist to `manual_url` / `instructions`;
   an omitted field leaves the stored value unchanged.
3. Return `200 { "version": "3.4.0", "updatedAt": "…" }`.

The endpoint does **not** verify the GitHub release actually exists — validation that the assets are
published is the release operator's responsibility (a future enhancement could HEAD one asset). Keep
the writer dumb.

> **Ops note.** The [admin portal](reference-genero-intelligence) already fronts admin actions; a
> small "fglpkg release" field there can call this endpoint so version bumps need neither a redeploy
> nor a raw API call. Portal wiring is out of scope for this spec — the endpoint is the contract.

## Non-goals

- **GI does not host or proxy binaries.** URLs point at GitHub Releases. If the download origin ever
  moves, only `FGLPKG_RELEASE_BASE`/`FGLPKG_RELEASE_REPO` change — no client change.
- **No checksum or signature storage/serving.** The client fetches GitHub's `checksums.txt` (via
  `checksumsUrl`) and its detached Ed25519 signature (via `checksumsSigUrl`); GI only *derives* those
  URLs — it never computes/stores SHA-256, never signs, and never holds a signing key. Release-signing
  keys are managed entirely on the fglpkg release side
  ([self-update.md § Release signing & verification](self-update.md#release-signing--verification)).
- **No auto-sync from GitHub.** The version is set explicitly by an admin (chosen over a live GitHub
  poll to avoid a server-side GitHub dependency + rate-limit handling). Auto-sync is a possible
  future enhancement, not part of this spec.
- **No pre-release / channel support.** Single "latest stable" version only, matching
  [self-update.md](self-update.md)'s scope.
- **No per-Genero-major variants.** fglpkg is a standalone Go binary independent of Genero; the
  `genero<major>` artifact-variant machinery used for packages does not apply.

## Testing

- **GET, row present:** builds all six asset URLs from the stored version + configured template;
  `checksumsUrl`, `checksumsSigUrl`, and `notes` correct; `os`/`arch` spellings are Go's.
- **GET recovery fields:** `manualUrl` / `instructions` reflect the config row when set, and fall back
  to the defaults (release tag page / built-in text) when the columns are null.
- **GET, row absent:** `404` with the documented body (the client's safe no-op state).
- **GET single-asset:** `?os/&arch` returns one asset; unknown pair → `404`.
- **GET caching header** present.
- **PUT auth:** non-admin → `403`; unauthenticated → `401`.
- **PUT validation:** malformed version → `400`; leading `v` stripped; round-trips through GET.
- **PUT recovery fields:** `manualUrl` / `instructions` persist and round-trip through GET; omitting a
  field on a later PUT leaves the stored value unchanged.
- **PUT idempotency:** second PUT updates the same single row (no duplicate rows), refreshes
  `updated_at`/`updated_by`.

## Rollout

1. Apply `0039_fglpkg_release.sql`; set `FGLPKG_RELEASE_REPO`/`FGLPKG_RELEASE_BASE` (defaults are
   fine for now).
2. Deploy the two handlers. GET returns `404` until an admin PUTs the first version — harmless, and
   the fglpkg client already treats it as "no update info".
3. On each fglpkg release, PUT the new version (or use the admin-portal field once wired).
