# fglpkg — Outstanding Work & R&D Handoff Plan

**Status:** Handoff draft
**Date:** 2026-06-15
**Audience:** R&D team taking ownership of fglpkg
**Supersedes the status (not the vision) of:** [fglpkg-enhancement-roadmap.md](fglpkg-enhancement-roadmap.md)

---

## 1. Context & current state

fglpkg is a package manager for Genero BDL projects — think npm for Genero. It
resolves and installs BDL packages and Java JAR dependencies, with a lockfile for
reproducibility, semver constraint resolution, per-Genero-version build variants,
and a publish flow that uploads to the **Genero Intelligence (GI)** registry.

- **Version:** 2.4.0 · **Branch:** `gi-registry` (ahead of `origin`, pushed).
- **Registry backend:** the GI registry at `https://service.generointelligence.ai`
  (consumer + publish, the `/registry/*` protocol). A legacy flat-file server at
  `fglpkg-registry.fly.dev` is still referenced by four admin commands — see
  Workstream A.
- **Architecture:** see [architecture.md](architecture.md).

### Already shipped (the April roadmap lists several of these as gaps — they are done)

| Capability | Where |
|---|---|
| `fglpkg docs` + `docs` glob bundling | [internal/cli/cli.go](../internal/cli/cli.go), [internal/cli/readme.go](../internal/cli/readme.go) |
| `fglpkg audit` (OSV.dev vuln scan for Java JARs) | [internal/audit/audit.go](../internal/audit/audit.go) |
| `fglpkg outdated` | [internal/cli/outdated.go](../internal/cli/outdated.go) |
| `fglpkg sbom` (CycloneDX) | [internal/sbom/cyclonedx.go](../internal/sbom/cyclonedx.go) |
| OAuth (auth-code + PKCE) login with silent refresh | [internal/oauth/](../internal/oauth/), [internal/credentials/](../internal/credentials/) |
| Rich publish metadata (repo/author/license/genero/deps + README/USERGUIDE) | [internal/cli/cli.go](../internal/cli/cli.go), [internal/registry/registry.go](../internal/registry/registry.go) |
| `fglpkg init --template <library\|app>` | [internal/cli/templates.go](../internal/cli/templates.go) |
| `keywords` manifest field | [internal/manifest/manifest.go](../internal/manifest/manifest.go) |
| `fglpkg publish --ci` (non-interactive) | [internal/cli/cli.go](../internal/cli/cli.go) |
| Parallel downloads, prepublish validation, `search --all` | [internal/installer/parallel.go](../internal/installer/parallel.go), [internal/cli/publish_validation.go](../internal/cli/publish_validation.go) |

### The three headline workstreams

| # | Workstream | Owner repo | Net new build |
|---|---|---|---|
| A | Jettison the registry — repo hosts only the CLI | fglpkg | Removal only |
| B | Private packages (customer/tenant scoped) | mostly **GI (done)** + small fglpkg | Small |
| C | Remaining roadmap items | mixed | Varies |

For the full parity picture against npm/gem/maven, **§7 reconciles this plan
against all 33 items in [market-readiness-gaps.md](market-readiness-gaps.md)** and
calls out the gaps not yet tracked here.

---

## 2. Workstream A — Jettison the registry (repo becomes CLI-only)

**Goal:** the registry is owned by `4js-genero-intelligence`. This repo ships only
the `fglpkg` CLI. Remove the embedded/standalone registry server and the legacy
fly.dev coupling.

**Decision (confirmed):** **clean break** on the legacy admin commands — remove
them now rather than migrate or keep fly.dev alive. Re-add admin features later if
and when the GI registry exposes equivalent endpoints.

### A.1 Remove the standalone server (no CLI imports it — verified)

Only `cmd/registry/main.go` imports `internal/registry/server`. Delete:

- `internal/registry/server/` — entire directory (`server.go`, `store.go`,
  `auth.go`, `blob.go`, `testing.go`, `server_test.go`, `auth_test.go`)
- `cmd/registry/main.go`
- `Dockerfile`, `fly.toml`, `scripts/setup-fly.sh` (all serve the server)

### A.2 Drop server-only dependencies

`github.com/aws/aws-sdk-go-v2{,/config,/credentials,/service/s3}` are used **only**
by `internal/registry/server/blob.go` (R2 uploads). Remove the four direct
requires from [go.mod](../go.mod) and run `go mod tidy` to drop the ~13 indirect
AWS lines. This is the bulk of the dependency-tree shrink.

### A.3 Remove the legacy admin commands (clean break)

These talk only to `registry.LegacyBase` (`fglpkg-registry.fly.dev`) and break if
the server is gone. Remove from [internal/cli/cli.go](../internal/cli/cli.go):

- `cmdUnpublish` (dispatch `"unpublish"`, line ~105) — also drops the only caller
  of `gh.DeleteRelease`.
- `cmdOwner` + `cmdOwnerList/Add/Remove` (dispatch `"owner"`, line ~113)
- `cmdToken` (dispatch `"token"`, line ~115)
- `cmdConfig` + `cmdConfigGitHubRepos*` (dispatch `"config"`, line ~117) — the only
  CLI caller of `registry.FetchConfig`, plus the `resolveGitHubRepo` fallback that
  also calls it.

Then remove the now-dead registry client surface in
[internal/registry/registry.go](../internal/registry/registry.go):
`LegacyBase`, `FetchConfig`, `PublisherVersionList`.

### A.4 Trim now-dead GitHub code

The new publish flow no longer uses GitHub Releases. In
[internal/github/github.go](../internal/github/github.go), remove the release-API
surface once the legacy commands are gone: `ReleaseTag`, `AssetName`,
`GetReleaseByTag`, `CreateRelease`, `UploadAsset`, `GetOrCreateRelease`,
`DeleteRelease`, `RepoFromEnv` (and the `setHeaders`/`checkResponse`/`isNotFound`
helpers if unused after).

**Keep** `VariantAssetName` (used by `publishPackage` in cli.go and by
[internal/cli/pack.go](../internal/cli/pack.go) to name the zip) and `IsGitHubURL`
(used by the installer's download-auth selection in
[internal/installer/installer.go](../internal/installer/installer.go)). Let the
compiler confirm exactly what remains reachable.

### A.5 Simplify the registry client

`defaultConsumerBase` and `defaultPublisherBase` are now identical
(`service.generointelligence.ai`). Collapse `consumerBase()`/`publisherBase()`
into a single base resolver and rewrite the stale package doc-comment at the top
of `registry.go` that still describes a two-backend (consumer vs publisher) split.

### A.6 Retire obsolete env vars

After the clean break these no longer do anything: `FGLPKG_PUBLISH_TOKEN`,
`FGLPKG_GITHUB_TOKEN`, `FGLPKG_GITHUB_REPO`, and `FGLPKG_PUBLISH_REGISTRY`
(publisher base == consumer base now — keep `FGLPKG_REGISTRY` only). Remove their
handling and their rows from the README env table.

### A.7 Docs & help cleanup

- [README.md](../README.md): delete the "Legacy registry server", "Registry API"
  table, "Registry Storage", and the `unpublish`/`owner`/`token`/`config` usage
  lines; prune the env-var table.
- `printUsage` in cli.go and the command list in
  [internal/cli/completion.go](../internal/cli/completion.go): remove the four
  commands.

### A.8 Acceptance criteria

- `go build ./...` and `go test ./...` green with the server and legacy commands
  gone.
- `grep -r "LegacyBase\|registry/server\|aws-sdk"` returns nothing in Go sources.
- `go mod tidy` leaves no AWS requires; dependency count and binary size drop.
- README/help no longer mention a legacy registry or the removed commands.

> **Effort:** ~0.5–1 day, mechanical and low-risk (removal + compiler-driven
> cleanup). Recommended as the **first** handoff PR — it shrinks and clarifies the
> repo for the incoming team.

---

## 3. Workstream B — Private packages (customer/tenant scoped)

**Requirement:** a package may be private, attached to a customer/tenant; only
that tenant's users can see/install it. Public packages install for anyone.

### B.1 Registry side — already DONE (no core build required)

Verified in `4js-genero-intelligence`:

- **Visibility column** with tenant default: `registry_packages.visibility TEXT
  NOT NULL DEFAULT 'private' CHECK (visibility IN ('public','private'))` —
  `migrations/0018_registry.sql:14`.
- **Tenant ownership**: `registry_packages.owner_partner_id` → `partners(id)`;
  every user carries a `partner_id`.
- **Read enforcement**: `canRead(pkg, user)` (`src/gateway/registry-routes.ts:52`)
  gates the package-detail and artifact-download routes; the browse/search SQL
  adds `(p.visibility = 'public' OR p.owner_partner_id = ?)`. Non-owners get 404
  on a private package; anonymous users cannot download it.
- **Publish**: `POST /registry/packages` requires `user.partner_id` and stores
  `visibility` + `owner_partner_id` together.
- **Tests** confirm tenant-A can read its private package while tenant-B and
  anonymous get 404.

**Conclusion:** tenant isolation for private packages is implemented and tested on
the registry. fglpkg does not need a registry build for this.

### B.2 fglpkg side — small work + verification

- **Manifest + publish already support it:** `manifest.Manifest.Visibility` exists
  and `publishPackage` sends it on package create.
- **Default stays `public` (confirmed decision).** Note the intentional mismatch:
  `publishPackage` sets `visibility = "public"` when the manifest omits it
  ([cli.go:762-764](../internal/cli/cli.go)), which **overrides** the registry's
  private-by-default. This is deliberate (npm-like, public-by-default); publishing
  private is explicit via `"visibility": "private"` in `fglpkg.json`. Document this
  clearly in the README so it is not surprising to customers.
- **Consuming private packages requires login.** The OAuth bearer carries the
  user→partner association, and `install`/`search`/`info` already send it, so a
  logged-in tenant user resolves private packages automatically. Remaining work:
  - A **clear CLI error** when a private package returns 404 to an
    unauthenticated or wrong-tenant user (today it surfaces as a generic
    "not found"). Distinguish "not found" from "exists but you lack access / are
    not logged in — run `fglpkg login`".
  - **Docs**: a "Private packages" README section — how to publish private, that
    consumers must be logged-in members of the owning tenant, and that public is
    the default.
- **Optional ergonomics (nice-to-have, not required):** `fglpkg publish
  --private`/`--public` flags, and have `init --template` leave `visibility`
  unset rather than implying a default.

### B.3 Optional registry hardening (GI repo, low priority — not fglpkg work)

- Re-check visibility on the signed-download endpoint (`GET /registry/dl/:token`)
  for defense-in-depth (today it trusts the issuer).
- Formalize `registry:read`/`registry:write` scope enforcement (currently
  partner_id is the gate; scopes are reported but not enforced).
- Embed `partner_id` in PATs so externally issued tokens (CI) carry tenant context.

### B.4 Acceptance / verification (end-to-end)

Mirror the metadata smoke test already used in this project:
1. Publish a package as tenant A with `"visibility": "private"`.
2. Tenant-A user: `fglpkg install` succeeds.
3. Tenant-B user and anonymous: install fails with a clear access/login error
   (registry returns 404).
4. A `public` package installs for anyone, logged in or not.

> **Effort:** ~1 day (CLI error message + README + the e2e verification). Registry:
> ~0 (done); optional hardening separately.

---

## 4. Workstream C — Remaining roadmap items

Status carried over from the codebase audit. "Blocker" flags cross-repo
dependencies on the GI registry.

| Item | Status | Blocker | Effort | Notes |
|---|---|---|---|---|
| `fglpkg deprecate <pkg>@<ver>` | Missing | **GI endpoint** | M | Needs `DELETE`/flag route + a `deprecated` field surfaced on reads |
| Org/team management commands | Missing (CLI) | **GI admin surface** | M–L | RBAC + partner model exist in GI; needs CLI commands + admin API |
| Package **signing** / `install --verify-signature` | Missing | none (CLI-led) | L | Largest security item; builds on existing SHA256 verification. Decide signing scheme (e.g. minisign/cosign-style detached sig + key distribution) |
| `fglpkg migrate <old> <new>` | Missing | none | S | Low value; rename/redirect helper |
| Opt-in telemetry | Missing | none | M | Partly redundant — GI already tracks downloads server-side; reconsider need |
| Self-hosted deployment kit / k8s (roadmap 2.3) | N/A | — | — | **Obsolete** — registry is Cloudflare-hosted in GI; drop from scope |
| Web registry UI / README rendering (roadmap 1.1–1.2) | Done elsewhere | — | — | GI portals render package detail + README/USERGUIDE |
| VS Code extension (roadmap 2.2) | Missing | — | L | Separate project, not this repo |

**Split for planning:**
- **CLI-only (no cross-repo dependency):** signing, migrate, telemetry.
- **Blocked on GI registry endpoints:** deprecate, org/team management. Coordinate
  with the GI team before starting these.

---

## 5. Cross-repo coordination & suggested sequencing

**Touches `4js-genero-intelligence`:** Workstream C `deprecate` and org/team
management (new endpoints); optional private-package hardening (B.3).
**fglpkg-only:** Workstream A (jettison), Workstream B CLI bits, Workstream C
signing/migrate/telemetry.

**Recommended order:**
1. **Workstream A (jettison)** — first; mechanical, shrinks the repo, gives the
   incoming team a clean CLI-only codebase and dependency tree.
2. **Workstream B (private packages)** — CLI error + docs + e2e verification;
   unlocks customer/tenant use, registry already supports it.
3. **Workstream C** by value — `signing` (security, customer-facing) is the
   highest-impact CLI-led item; `deprecate`/org-mgmt once GI endpoints land;
   `migrate`/telemetry as capacity allows.

---

## 6. Open decisions for the R&D team

- **Signing scheme** for Workstream C (key management, detached signatures,
  registry storage of signatures, trust roots).
- **Re-introduction of admin commands** (`unpublish`/`owner`/`token`) — these were
  removed in the clean break; decide whether they return as CLI commands against
  new GI endpoints or live only in the GI portals.
- **Telemetry** — whether client-side telemetry is worth building given the
  registry already records downloads.

---

## 7. Market-readiness coverage (vs. `market-readiness-gaps.md`)

Workstreams A–C above were scoped to the three asks plus the
[enhancement-roadmap](fglpkg-enhancement-roadmap.md). This section reconciles them
against the broader **33-item** parity catalogue in
[market-readiness-gaps.md](market-readiness-gaps.md) (npm/gem/maven parity;
"market-ready" = start of Phase 2). It is the authoritative coverage map.

Legend: ✅ **Done** (shipped) · 📋 **In plan** (covered as outstanding work in
§2–§4 above) · ⚠️ **Uncovered** (outstanding and *not* yet in this plan — new
backlog).

| # | Capability | Pri | Status | Where / note |
|---|---|---|---|---|
| 1 | Dependency scopes (dev + optional) | P0 | ✅ Done | manifest `DevDependencies`/`OptionalDependencies`, resolver scope promotion |
| 2 | Declarative lifecycle hooks | P0 | ✅ Done | [internal/hooks/](../internal/hooks/) |
| 3 | `version` bump | P0 | ✅ Done | [internal/cli/version.go](../internal/cli/version.go) |
| 4 | `outdated` | P0 | ✅ Done | [internal/cli/outdated.go](../internal/cli/outdated.go) |
| 5 | `audit` (CVE) | P0 | ✅ Done | OSV.dev — [internal/audit/](../internal/audit/) |
| 6 | Package signing / verification | P0 | 📋 In plan | Workstream C (signing) |
| 7 | Web registry UI | P0 | ✅/⚠️ Split | detail + README/USERGUIDE rendering done in GI portals; **self-service signup** (email verify, anti-abuse) ⚠️ uncovered (GI-side) |
| 8 | CI gate blocking merge | P0 | ⚠️ Partial | `ci.yml` exists; **branch-protection enforcement** not documented/owned |
| 8′ | `pack` | P1 | ✅ Done | [internal/cli/pack.go](../internal/cli/pack.go) |
| 9 | `publish --dry-run` | P1 | ✅ Done | [internal/cli/cli.go](../internal/cli/cli.go) |
| 10 | `info` / `view` | P1 | ✅ Done | [internal/cli/info.go](../internal/cli/info.go) |
| 11 | `deprecate` | P1 | 📋 In plan | Workstream C (GI-blocked) |
| 12 | `.fglpkgignore` | P1 | ✅ Done | [internal/cli/ignore.go](../internal/cli/ignore.go) |
| 13 | Dist-tags / release channels | P1 | ⚠️ Uncovered | `publish --tag beta`, `install pkg@beta`; CLI + registry |
| 14 | Organizations / scoped names | P1 | 📋/⚠️ Split | org/team **commands** in Workstream C; `@scope/name` **namespace** ⚠️ uncovered (registry schema) |
| 15 | 2FA for publish | P1 | ⚠️ Uncovered | TOTP/WebAuthn; registry + CLI |
| 16 | Prepublish validation | P1 | ✅ Done | [internal/cli/publish_validation.go](../internal/cli/publish_validation.go) |
| 17 | VS Code extension | P2 | 📋 In plan | Workstream C (separate project) |
| 18 | JSON schema for `fglpkg.json` | P2 | ✅ Done | [schema/fglpkg.schema.json](../schema/fglpkg.schema.json) |
| 19 | Genero Studio plugin | P2 | ⚠️ Uncovered | native-audience differentiator; not mentioned |
| 20 | Shell completions | P2 | ✅ Done | [internal/cli/completion.go](../internal/cli/completion.go) |
| 21 | GitHub Action (`setup-fglpkg`) | P2 | ⚠️ Uncovered | `--ci` exists, but the Action itself is not built |
| 22 | Self-hosted deploy kit (Docker/Helm) | P2 | 📋 In plan | Workstream C — marked **obsolete** (registry is GI/Cloudflare-hosted) |
| 23 | Telemetry (opt-in) | P2 | 📋 In plan | Workstream C |
| 24 | SBOM generation | P2 | ✅ Done | [internal/sbom/](../internal/sbom/) |
| 25 | Download statistics | P3 | ⚠️ Uncovered | GI counts downloads server-side; surfacing via `info`/web not built |
| 26 | Dependents graph | P3 | ⚠️ Uncovered | registry/portal side |
| 27 | `fglpkg link` (non-workspace) | P3 | ⚠️ Uncovered | partial via workspace members today |
| 28 | Offline install from cache | P3 | ⚠️ Uncovered | — |
| 29 | Parallel downloads | P3 | ✅ Done | [internal/installer/parallel.go](../internal/installer/parallel.go) |
| 30 | Progress bars / status UI | P3 | ⚠️ Uncovered | install UX polish |
| 31 | Package migration / rename | P3 | 📋 In plan | Workstream C (migrate) |
| 32 | LDAP / SAML / SSO | P3 | ⚠️ Uncovered | enterprise auth; GI-side direction |
| 33 | Audit log with retention | P3 | ⚠️ Uncovered | compliance; GI-side |

**Summary:** 14 Done · ~7 In plan · ~13 Uncovered. (Rows #7, #8, #14 are split and
count toward two columns; several uncovered items are GI-side.)

### 7.1 Newly surfaced backlog (the ⚠️ items to add to tracking)

Not yet reflected in Workstreams A–C; triage before Phase 2:

- **P0/P1, fglpkg-led:** CI **branch-protection** enforcement (#8); **dist-tags /
  release channels** (#13); **2FA for publish** (#15, registry + CLI).
- **P1/P2, registry-schema or cross-repo:** **scoped names** `@fourjs/poiapi`
  (#14); **self-service signup** for the web UI (#7, GI).
- **P2 ecosystem:** **GitHub Action** `setup-fglpkg` (#21); **Genero Studio
  plugin** (#19).
- **P3 / later:** download-stats surfacing (#25), dependents graph (#26), `link`
  (#27), offline cache (#28), progress bars (#30), LDAP/SSO (#32), audit log with
  retention (#33).

### 7.2 Registry infrastructure & domain governance (route to GI team)

`market-readiness-gaps.md` carries an open-questions section flagged
*"unresolved, blocking Phase 2."* It is **not** addressed by this plan and is now
largely a **`4js-genero-intelligence`** decision since the registry moved to
Cloudflare. Items needing an owner + decision:

- **Canonical production URL** and whether it is API-versioned (`/v1/...`).
- **fly.dev retirement** — directly intersects **Workstream A**: the legacy
  `fglpkg-registry.fly.dev` server is removed there, so confirm no consumers
  depend on it and decide front-vs-retire for any remaining traffic.
- **TLS/cert ownership**, **staging/prod (+ sandbox) split**, **data residency /
  retention policy**, **namespace URL scheme** (interacts with scoped names #14),
  and **self-hosted naming convention** for air-gapped customers.

These are governance/ops decisions, not fglpkg code; they belong in a short GI
decision record with named owners.
