# Spec: Genero Intelligence registry changes for fglpkg Workstream C

**Status:** ✅ GI-side implemented — deprecate + package-signing endpoints live on the GI `package-management` branch
**Date:** 2026-07-02
**Author:** Mike Folcher
**Tracking:** Workstream C in [docs/outstanding-work.md](../docs/outstanding-work.md) §4.
**Companion spec:** [specs/package-signing.md](package-signing.md) (signing is specced in full there; this doc references its GI deliverables rather than re-specifying them).

> **Repo note.** Unless a path is a workspace link, all `src/...`, `migrations/...`
> and `file:line` citations in this document refer to the **`4js-genero-intelligence`**
> repo (branch `package-management`, at `~/4js-bitbucket/4js-genero-intelligence`),
> **not** to fglpkg. fglpkg files are given as workspace links.

---

## Summary

Several Workstream C items are blocked on registry-side work in Genero Intelligence
(GI). This spec collects **all** required GI changes, broken down by feature, and —
importantly — distinguishes what is genuinely new from what the GI registry
**already implements** (which turns out to be a lot). The headline correction from
grounding this against the current GI code:

| Feature (Workstream C) | GI dependency | Reality after code review |
|---|---|---|
| **Deprecate & relocate** (`deprecate --moved-to`) | New endpoint + schema | **Genuinely new.** The existing `status='deprecated'` is a *hide/withdraw* toggle, **not** npm-style deprecate. New advisory columns + read-model fields required. |
| **Org / team management** | — | **Deferred (2026-07-02).** Handled by the GI **web portal** — the partner portal already provides member management + PAT issuance. No CLI or registry work in Workstream C. See §2. |
| **Package signing** | GI + fglpkg | Fully specced in [package-signing.md](package-signing.md). GI deliverables summarized here for completeness. |
| **Telemetry / download stats** | "none" / optional | Downloads already counted server-side. Only *surfacing* stats (gap #25) needs new GI reads; client telemetry is optional and separate. |

The rest of this document specifies each feature's GI changes. An appendix lists the
**adjacent** GI-dependent backlog items (dist-tags, scoped names, 2FA, self-service
signup) that are *not* Workstream C but share the same registry and are worth the GI
team's awareness while this work is scheduled.

---

## 0. Current GI registry state (baseline)

Grounding facts, so every "new" below is unambiguous.

**Routes** (`src/gateway/registry-routes.ts`): discovery (`GET /registry/packages`,
`/tags`, `/whoami`, `/mine`, `/packages/:slug`, `.../versions/:version`), download
(`.../artifacts/:variant`, signed `GET /registry/dl/:token`), publish (`POST
/registry/packages`, `.../versions`, `PUT .../artifacts/:variant`,
`POST .../submit`), and lifecycle (`PATCH /packages/:slug`, `DELETE /packages/:slug`,
`PATCH .../versions/:version`, `DELETE .../versions/:version`).

**Lifecycle semantics that already exist** — do not confuse with deprecate:
- **Withdraw package** (`PATCH /packages/:slug {status}`, `registry-routes.ts:656`):
  toggles `registry_packages.status` between `active` and `deprecated`. A
  `deprecated` package is **hidden** — `canRead` (`registry-routes.ts:52`) returns
  the package to consumers only when `status === 'active'`, and the browse SQL
  filters `p.status = 'active'` (`registry-routes.ts:187`).
- **Yank version** (`PATCH .../versions/:version {withdrawn}`, `registry-routes.ts:710`):
  sets `registry_versions.withdrawn_at`; `recomputeLatest` repoints `latest_version_id`.
- **Soft-delete** package/version (`deleted_at`): hidden from everyone but admins.

**Schema** (`migrations/0018_registry.sql` + 0019–0024):
- `registry_packages(id, slug, name, description, visibility, owner_partner_id,
  status['active'|'deprecated'], latest_version_id, downloads, created_by,
  created_at, updated_at, deleted_at)`.
- `registry_versions(id, package_id, version, status['pending'|'approved'|'rejected'],
  submitted, changelog, tags_json, submitted_by/at, reviewed_by/at, review_comment,
  published_at, withdrawn_at, deleted_at, repository, author, license,
  genero_constraint, dependencies_json, readme, userguide)`.
- `registry_artifacts(id, version_id, variant, filename, r2_key, size_bytes, sha256,
  created_at)` — **no signature column yet.**
- `registry_version_tags(version_id, key, value)` — faceted **search tags**
  (`key=value`), **not** dist-tags/channels.

**Auth / RBAC**: 4 roles (`super_admin`, `admin`, `partner_admin`, `developer` —
`src/types.ts:142`, CHECK in `migrations/0001_initial_schema.sql:54`). Flat partner
model: one `users.partner_id` FK, **no teams/sub-groups**. PATs exist
(`migrations/0023_api_tokens.sql`, `src/lib/registry/tokens.ts`) with full CRUD at
`GET/POST/DELETE /partner/tokens` — but scope is **hardcoded `registry:read`**
(`src/gateway/partner-routes.ts:824`) and scopes are **not enforced** anywhere yet.

**Client contract** (fglpkg): responses are parsed by `apiVersionSummary` /
`apiPackageDetail` / `apiListedPackage` in
[internal/registry/registry.go](../internal/registry/registry.go) (snake_case JSON).
Any new read-model field must be added on both sides.

---

## 1. Feature — Deprecate & relocate (`deprecate --moved-to`)

**Decision recap (2026-07-02):** the standalone `migrate` command was dropped;
rename/redirect is folded into `deprecate --moved-to`, following the npm model
(rename = deprecate the old + point at the successor). See
[docs/outstanding-work.md](../docs/outstanding-work.md) §4.

### 1.1 Semantics — deprecate ≠ withdraw

This is the crux and the reason it's new work:

| Concept | Installable? | Listed/searchable? | Signal to consumer | Exists today? |
|---|---|---|---|---|
| **Withdraw** (`status='deprecated'`) | No (404 to consumers) | No | gone | ✅ yes |
| **Yank version** (`withdrawn_at`) | No | No | gone | ✅ yes |
| **Soft-delete** (`deleted_at`) | No | No | gone | ✅ yes |
| **Deprecate (npm-style)** | **Yes** | **Yes** | **advisory warning + optional successor** | ❌ **new** |

Deprecate must leave the package/version **fully installable and listed**, and attach
an advisory message (and optionally a successor package) that the CLI surfaces as a
non-fatal warning — exactly like `npm deprecate`, Maven relocation POMs, and
Composer's `abandoned`. Therefore it needs its **own columns**; it must **not** reuse
`status='deprecated'` (which hides).

> **Naming hazard for the GI team.** The existing `status='deprecated'` value is a
> misnomer — it means *withdrawn*. Overloading it for npm-deprecate would be a
> correctness bug (deprecated packages would vanish). Keep them separate. Optionally
> (low priority, deferred), a future migration could rename the status value to
> `withdrawn` to remove the ambiguity; not required for this feature.

### 1.2 Granularity

- **Version-level** (`deprecate pkg@1.2.3`): the primary case. Matches
  `deprecate old@ver --moved-to new`.
- **Package-level** (`deprecate pkg`, no version): whole-package relocation — every
  version is advisory-deprecated and a package-wide `moved_to` is set. This is the
  Maven-relocation / Composer-`abandoned` case.
- **Version ranges** (npm's `deprecate pkg@"<2.0"`): **out of scope v1.** Single
  version or whole package only.
- **Un-deprecate**: clearing the message (empty string) lifts the deprecation, npm-style.

### 1.3 GI schema changes

New migration `00NN_registry_deprecation.sql`:

```sql
-- Version-level npm-style deprecation (advisory; version stays installable & listed).
ALTER TABLE registry_versions ADD COLUMN deprecated_at        TEXT;          -- NULL = not deprecated
ALTER TABLE registry_versions ADD COLUMN deprecation_message  TEXT NOT NULL DEFAULT '';
ALTER TABLE registry_versions ADD COLUMN moved_to             TEXT NOT NULL DEFAULT '';  -- successor "slug" or "slug@version"

-- Package-level relocation (whole package renamed/moved).
ALTER TABLE registry_packages ADD COLUMN deprecated_at        TEXT;
ALTER TABLE registry_packages ADD COLUMN deprecation_message  TEXT NOT NULL DEFAULT '';
ALTER TABLE registry_packages ADD COLUMN moved_to             TEXT NOT NULL DEFAULT '';
```

All optional/defaulted, so existing rows and older clients are unaffected. `moved_to`
holds a package slug, optionally `slug@version`; the API validates the slug shape
(`isValidSlug`, `registry-routes.ts:46`) but does **not** require the successor to
exist yet (allows publishing the redirect before the new package, and avoids a hard
FK). Message cap: reuse the scalar-limit pattern (`SCALAR_LIMITS`,
`registry-routes.ts:39`) — e.g. 512 bytes; reject over-limit rather than truncate.

### 1.4 GI endpoint changes

Extend the two **existing owner-gated** PATCH routes (no new routes needed):

**`PATCH /registry/packages/:slug/versions/:version`** (`registry-routes.ts:710`) —
currently accepts `{withdrawn}`. Also accept:

```jsonc
{ "deprecated": true, "deprecationMessage": "use chart-3d-ng instead", "movedTo": "chart-3d-ng" }
// or to lift it:
{ "deprecated": false }
```

- Owner-only (`isOwner`), audit-logged (`writePartnerActionEvent`, action
  `registry_deprecate_version` / `registry_undeprecate_version`).
- `deprecated:true` → set `deprecated_at = datetime('now')`, store message + moved_to.
  `deprecated:false` → NULL out `deprecated_at`, clear message + moved_to.
- **Does not** touch `status`, `withdrawn_at`, or `latest_version_id` — the version
  stays live. (A deprecated version can still be the `latest`.)
- Applies regardless of `withdrawn_at` state, but a withdrawn version is already
  hidden, so deprecating it is a no-op in practice.

**`PATCH /registry/packages/:slug`** (`registry-routes.ts:656`) — currently accepts
`{status}`. Also accept `{deprecated, deprecationMessage, movedTo}` for **whole-package**
relocation. Same owner gate, audit action `registry_deprecate_package`.

### 1.5 Read-model changes (GI → client contract)

**`serializeVersion`** (`registry-routes.ts:125`) — add three fields:

```jsonc
{
  "version": "1.2.3",
  "deprecated": true,                       // bool: deprecated_at IS NOT NULL
  "deprecation_message": "use chart-3d-ng", // "" when not deprecated
  "moved_to": "chart-3d-ng"                 // "" when none
}
```

**Package detail** (`GET /packages/:slug`, `registry-routes.ts:348`) — add package-level
`deprecated`, `deprecation_message`, `moved_to`.

**Browse/search listing** (`apiListedPackage`, from `GET /packages`,
`registry-routes.ts:228`) — add a `deprecated` bool derived from the latest version (or
the package), so `search`/`outdated` can flag it inline without a detail fetch.

**fglpkg client** ([internal/registry/registry.go](../internal/registry/registry.go)):
add matching fields to `apiVersionSummary`, `apiPackageDetail`, `apiListedPackage`,
and surface them on `PackageInfo`.

### 1.6 CLI surface (fglpkg — summary; full CLI spec in [specs/deprecate-cli.md](deprecate-cli.md))

```
fglpkg deprecate <pkg>[@<version>] [--moved-to <newpkg>[@<version>]] [--message <text>]
fglpkg deprecate <pkg>[@<version>] --undo
```

- With `@<version>` → PATCH version; without → PATCH package (whole-package relocation).
- `--moved-to` implies a default message ("<pkg> has moved to <newpkg>") if
  `--message` is omitted.
- **Consumer surfacing** (the payoff), mirroring npm/Maven/Composer:
  - `install` / resolve: when a resolved version is deprecated, print a non-fatal
    `Warning: <pkg>@<ver> is deprecated: <message>` and, if `moved_to` is set,
    `→ moved to <newpkg>; consider: fglpkg install <newpkg>`. Never blocks the install.
  - `info`: show a `Deprecated:` block with the message and successor.
  - `outdated`: flag deprecated installed packages with the successor hint.
- Requires login (owner auth) for the deprecate action itself; consuming warnings
  needs no auth beyond normal read.

### 1.7 Acceptance

1. Publish `pkg@1.0.0` (approved). `fglpkg deprecate pkg@1.0.0 --moved-to pkg-ng`.
2. `fglpkg install pkg@1.0.0` **still succeeds** and prints the deprecation + moved-to warning.
3. `fglpkg info pkg` shows the `Deprecated:` block and successor.
4. The package is **still listed** in `search` (flagged deprecated), unlike a withdrawn one.
5. `fglpkg deprecate pkg@1.0.0 --undo` clears the warning.
6. Whole-package: `fglpkg deprecate pkg --moved-to pkg-ng` sets package-level relocation, surfaced on `info`/`search`.
7. Registry audit log records deprecate/undeprecate actions with actor + target.

---

## 2. Feature — Org / team management — DEFERRED (web portal)

> **Decision (2026-07-02): deferred out of Workstream C.** Organization and member
> management is done through the GI **web portal** (the partner portal), not the
> fglpkg CLI. There is **no CLI or registry work required** for this item in
> Workstream C. The analysis below is retained to justify the deferral and to record
> what a future CLI would map to, should it be revived.

### 2.1 Why deferring is safe: the portal already does all of it

The partner portal already exposes the full member-management surface (all
partner-scoped, `partner_admin`-gated). A CLI would have been a thin wrapper over the
same endpoints — so nothing is lost by leaving it to the portal:

| Intent | Existing GI endpoint (portal-backed) | Role | Source |
|---|---|---|---|
| Show my org | `GET /partner/info` | `partner_admin` | `partner-routes.ts:52` |
| List members (developers) | `GET /partner/developers` | `partner_admin` | `partner-routes.ts:78` |
| My profile | `GET /partner/me` | any member | `partner-routes.ts:20` |
| Invite a developer (email + invite_code) | `POST /partner/developers/invite` | `partner_admin` | `partner-routes.ts:257` |
| Add a peer partner-admin | `POST /partner/admins` | `partner_admin` | `partner-routes.ts:196` |
| Remove / suspend / reactivate a developer | `POST /partner/developers/:id/{remove,suspend,reactivate}` | `partner_admin` | `partner-routes.ts:92-193` |
| List / create / revoke PATs | `GET/POST/DELETE /partner/tokens` | any member | `partner-routes.ts:769-879` |

Model constraints that also argued against a bespoke CLI: the org model is **flat**
(a partner *is* the org; **no teams/sub-groups**), roles are only `developer` /
`partner_admin`, and **org creation is admin-only** (`POST /admin/partners`) — the CLI
couldn't have created an org anyway (self-service signup is the separate gap #7).

### 2.2 If revived later — the only two things that would need building

Recorded for future reference; **not scheduled**:
- A partner-level **role-change** endpoint (`PATCH /partner/developers/:id/role`) —
  none exists today (only platform `admin`↔`super_admin` via `admin-routes.ts:316`).
  The portal can add this UI-side without a CLI.
- CLI wrappers (`fglpkg org info|members|invite|...`) over the endpoints above.

### 2.3 The one item that is NOT org/team — tracked elsewhere

**PAT `registry:write` scopes** were previously lumped in here. They are **not** an
org/team-management concern and are **not** blocking anything today: `POST
/partner/tokens` hardcodes `registry:read` (`partner-routes.ts:824`) but scopes are
**not enforced**, so a PAT issued from the portal already publishes fine (this is how
`fglpkg publish --ci` works). Enforcing `registry:write` is an optional **security
hardening** item — it belongs with Workstream B.3 (scope enforcement) / the signing
work, and it does not gate deprecate, signing, or CI publish. Left out of this feature
entirely; noted so it isn't lost. See Appendix B.

---

## 3. Feature — Package signing (GI deliverables)

Signing is specified in full in [specs/package-signing.md](package-signing.md)
(design decisions locked: Ed25519, Cloudflare KMS root custody, Sigstore public-good
trust root, RFC 8785 canonical JSON). This section only enumerates the **GI-side**
deliverables so this coordination doc is complete; see that spec for wire formats,
error taxonomy, and rollout gating.

### 3.1 Layer 1 — registry-signed artifacts (on by default)

- **KMS**: root + working Ed25519 keys in Cloudflare Secrets Store; a small
  `internal/signing/` module in the Worker for sign/verify + key resolution.
- **New `GET /registry/.well-known/keys.json`**: signed keys manifest (working keys
  signed by the pinned root), cacheable.
- **Changed `PUT .../artifacts/:variant`**: after sha256/size, sign the canonical
  artifact payload and store the signature. Failure to sign is a 500 — no unsigned rows.
- **Read-model**: every artifact record gains a `signature {keyid, alg, sig}` block.
- **Backfill**: one-shot Worker signs all historical artifacts before the NOT NULL
  constraint is applied.
- **Schema**: `registry_artifacts.signature JSON NOT NULL` (after backfill);
  `registry_signing_keys(keyid PK, alg, pub_pem, valid_from, valid_to, retired_at)`;
  `registry_keys_manifests(issued_at PK, body, sig)`.

### 3.2 Layer 2 — Sigstore provenance (opt-in)

- **New `PUT /registry/packages/:slug/versions/:version/attestations/:variant`**:
  accept a Sigstore bundle; **verify server-side before storing** (Rekor inclusion,
  Fulcio chain against pinned trust root, subject digest == artifact sha256); store
  bundle in R2 + parsed identity row.
- **New `GET .../attestations/:variant`**: return the bundle (public, no auth).
- **Read-model**: artifact records gain an `attestation {present, issuer, subject,
  rekorEntryUUID}` block.
- **Per-package policy**: `registry_packages.require_provenance BOOLEAN DEFAULT FALSE`;
  `PATCH /packages/:slug {require_provenance}` (owner); enforced at `submit`.
- **Schema**: `registry_packages.require_provenance`;
  `registry_attestations(id PK, slug, version, variant, bundle_r2_key, issuer, subject,
  rekor_uuid, created_at)`.

*(Full CLI-side surface, canonical-payload spec, and milestones M1–M4 are in
[package-signing.md](package-signing.md).)*

---

## 4. Feature — Telemetry / download statistics (mostly optional GI)

**Downloads are already counted server-side**: `streamArtifact` increments
`registry_packages.downloads` on every artifact fetch (`registry-routes.ts:158`), and
the count is returned on browse/detail reads. So the roadmap's "telemetry" item is
**partly redundant** (as the outstanding-work doc notes).

Two separable pieces:

- **Client opt-in telemetry** (Workstream C "telemetry"): **no hard GI dependency.** If
  built, it would POST anonymized usage to a GI ingest endpoint — but given
  server-side counting already exists, reconsider whether it's worth building at all.
  Recommend **defer/drop** unless a concrete need appears. No GI work required to
  defer.
- **Download-stats surfacing** (gap #25, GI-side): the only real GI ask here. To power
  `fglpkg info`/a future `fglpkg stats`, expose more than the single package-level
  counter:
  - Optional new endpoint `GET /registry/packages/:slug/stats` → `{ total, weekly,
    per_version[] }`.
  - Requires **per-version** and **time-bucketed** counters, which don't exist today
    (only the package-level `downloads` integer). Minimal approach: a
    `registry_download_events(version_id, day, count)` rollup table incremented
    alongside the existing counter, plus the read endpoint.
  - **Priority: low (P3).** Specify only if surfacing stats is prioritized.

---

## 5. Consolidated GI work checklist (Workstream C)

New migrations (GI repo):

- [ ] `00NN_registry_deprecation.sql` — deprecation columns on versions + packages (§1.3).
- [ ] `00NN_registry_signing.sql` — `registry_artifacts.signature`, `registry_signing_keys`, `registry_keys_manifests` (§3.1).
- [ ] `00NN_registry_provenance.sql` — `registry_packages.require_provenance`, `registry_attestations` (§3.2).
- [ ] *(optional P3)* `00NN_registry_download_events.sql` — per-version/day rollup (§4).

Endpoints (GI repo, `src/gateway/`):

- [ ] Extend `PATCH /packages/:slug/versions/:version` + `PATCH /packages/:slug` for deprecate/relocate (§1.4).
- [ ] New `GET /registry/.well-known/keys.json`; sign-on-upload in `PUT .../artifacts` (§3.1).
- [ ] New attestation `PUT`/`GET`; `require_provenance` toggle + submit enforcement (§3.2).
- [ ] *(optional P3)* `GET /packages/:slug/stats` (§4).
- [ ] *(optional hardening, B.3 — not WS-C)* validated `scopes` on `POST /partner/tokens` + `registry:write` enforcement (§2.3).
- [ ] ~~Org/team endpoints~~ — **deferred to the web portal** (§2).

Read-model + client contract:

- [ ] Add deprecation fields to `serializeVersion`, package detail, browse listing (§1.5); mirror in fglpkg [registry.go](../internal/registry/registry.go).
- [ ] Add `signature` (and `attestation`) blocks to artifact reads (§3).

Ownership split: deprecate + signing are **GI-led**; the matching CLI commands are
**fglpkg-led** and depend on the endpoints landing first. Org/team management is
**out of scope** (web portal). Coordinate sequencing with the GI team (signing has the
longest lead time — KMS + backfill).

---

## Appendix A — Adjacent GI-dependent items (NOT Workstream C)

These share the registry and will need GI work too, but are outside Workstream C. Listed
so the GI team sees the full dependency surface while scheduling. See
[docs/outstanding-work.md](../docs/outstanding-work.md) §7 / §7.1 for tracking.

- **#13 Dist-tags / release channels** (`publish --tag beta`, `install pkg@beta`). Note:
  the existing `registry_version_tags` are **search facets**, not channel pointers. This
  needs a new `registry_dist_tags(package_id, tag, version_id)` table + resolve-by-tag
  logic. New GI work; CLI + registry.
- **#14 Scoped names** (`@fourjs/poiapi`). `isValidSlug` (`registry-routes.ts:46`)
  currently forbids `/` and `@`; scoped names need a namespace column/route scheme and a
  compatibility answer for existing unscoped slugs. Registry-schema change.
- **#15 2FA for publish** (TOTP/WebAuthn). Registry storage of factors + a step-up check
  on publish. GI + CLI.
- **#7 Self-service signup** (partner self-registration with email verify + anti-abuse).
  Blocks the CLI's inability to create an org (§2.1). GI/portal-side.
- **#26 Dependents graph** — registry/portal side; needs a reverse-dependency index.

## Appendix B — Open questions for the GI team

- **`moved_to` validation**: require the successor package to exist at deprecate time, or
  allow forward references (this spec assumes forward references allowed)?
- **Status-value rename**: is renaming `status='deprecated'` → `withdrawn` worth a
  migration to kill the naming ambiguity (§1.1), or leave as-is with documentation?
- **Scope enforcement rollout** (optional B.3 hardening, not WS-C): enforcing
  `registry:write` (§2.3) is a behavior change for any existing read-scoped PAT currently
  used to publish (works today because scopes aren't enforced). Confirm no external CI
  depends on that gap before flipping enforcement on.
