# Spec: Manifest ↔ registry dependency cross-check & fallback

**Status:** ✅ Implemented — shipped ([internal/installer/crosscheck.go](../internal/installer/crosscheck.go))
**Date:** 2026-07-09
**Author:** Mike Folcher
**Motivation:** the `poiapi@1.4.0` incident — a package installed with **zero of its 11
declared Java dependencies** and **no error or warning**, because the registry's stored
version metadata had an empty `dependencies.java` while the manifest *inside the published
zip* declared all 11 correctly.
**Related:** [specs/package-signing.md](package-signing.md) (the reviewed/authoritative
dependency surface this hardens toward), [security/threat-model.md](../security/threat-model.md)
(JAR scenarios), [specs/gi-registry-workstream-c.md](gi-registry-workstream-c.md).

---

## Summary

The installer resolves a package's Java (JAR) dependencies **exclusively** from the
registry's version metadata and never consults the `fglpkg.json` bundled inside the
downloaded package. When the two disagree — as they did for `poiapi@1.4.0` — the install
silently proceeds with the registry's (empty) list.

This spec adds two behaviours, both driven by reading the **bundled** manifest after a
package is downloaded:

1. **Cross-check (always on):** after extraction, diff each installed package's declared
   Java deps against the set the installer is actually going to install (from the resolve
   plan or the lock file). Emit an actionable warning on any divergence.
2. **Fallback (default on, disableable):** when the bundled manifest declares Java
   coordinates the install set is **missing**, install them anyway and record them in the
   lock file, so the package works and the resolution stays reproducible.

Neither behaviour blocks an install today. The cross-check is diagnostic; the fallback is
additive. The design leaves a clear seam to tighten `warn → refuse` once the registry
metadata becomes the reviewed, authoritative dependency surface (see [§9](#9-trust-model--security-posture)).

## 1. The incident (grounding)

`poiapi@1.4.0` was published **2026-06-03/04**. Two pieces of plumbing landed *after* that:

| Plumbing | Landed | Commit |
|---|---|---|
| Client sends `dependencies` in the publish payload | 2026-06-06 | `215fae7` (fglpkg) |
| Backend stores `dependencies_json` on create-version | 2026-07-01 | `a72955e` (genero-intelligence) |

So the registry row for `poiapi@1.4.0` has `dependencies.java: []`. The resolver reads that
empty list ([`internal/registry/registry.go:179`](../internal/registry/registry.go#L179) →
[`internal/resolver/resolver.go:319`](../internal/resolver/resolver.go#L319)), plans **0
JARs**, and `installFromPlan` installs none — while the zip's own manifest lists all 11
POI coordinates. Republishing fixed that one package, but any package published in the same
window has the same latent hole, and nothing surfaces it.

## 2. Root cause: two dependency sources, only one consulted

There are two records of a package's Java deps:

- **Registry metadata** — `GET /registry/packages/:slug` → `versions[].dependencies.java`.
  Fetched at **resolve time**, before any download. This is what builds `plan.JARs`.
- **Bundled manifest** — `fglpkg.json` inside the artifact zip. Available only **after**
  download+extract, at `.fglpkg/packages/<name>/fglpkg.json`
  ([`installer.go:343`](../internal/installer/installer.go#L343) already loads it, for
  `bin` permissions).

Resolution is entirely registry-driven and finishes before the first byte is downloaded, so
the bundled manifest is invisible to it. The cross-check therefore **cannot** live in the
resolver — it must run **post-extraction**, which is also where it naturally covers both the
resolve path (`installFromPlan`) and the lock path (`installFromLock`).

## 3. Design overview

```
resolve/lock ──► install packages (download + extract)
                        │
                        ▼
          ┌─── collect DECLARED java deps ─────────────┐
          │  root manifest.java  ∪  each installed     │
          │  package's bundled manifest.java           │
          └────────────────────────────────────────────┘
                        │  diff against INSTALL SET
                        │  (plan.JARs or lock.JARs, keyed by groupId:artifactId)
                        ▼
     ┌───────────┬──────────────────┬───────────┐
     │ missing   │ version-mismatch │  extra     │
     ▼           ▼                  ▼
  warn +      warn (install-set    warn (info)
  FALLBACK    version wins)
  install
     │
     ▼
  install JARs (plan/lock set  +  fallback set)  ──►  update lock
```

## 4. Where it hooks

- **Read the bundled deps:** primary approach is to read from disk after extraction —
  `manifest.Load(filepath.Join(i.packagesDir, name))` — since the manifest is guaranteed
  present for every BDL/mixed package (webcomponent-only packages carry no Java deps and do
  not extract their manifest, so they are simply skipped). This adds **no new parsing** and
  no change to the widely-called `Install`.
  - *Alternative:* generalise the existing zip-peek
    [`readWebcomponentsFromZip`](../internal/installer/installer.go#L565) into a
    `readManifestFromZip` that returns `{webcomponents, dependencies}` in one pass. Preferred
    only if we want to avoid a second file read; not required.
- **Run the check:** a new step in `InstallAllWithOptions`
  ([`installer.go:74`](../internal/installer/installer.go#L74)), after the package-install
  parallel pass and **before** the JAR-install pass, in both branches
  (`installFromPlan` / `installFromLock`). It iterates the packages in the install set —
  **including those skipped as "already installed"** (their manifest is on disk too) — loads
  each bundled manifest, and builds the declared set.
- **Comparison key:** `JavaDependency.Key()` = `groupId:artifactId`
  ([`manifest.go`](../internal/manifest/manifest.go)), matching the resolver's own dedup.
  Version is compared as a secondary field.

## 5. Divergence classification

Let **INSTALL** = the JAR set the installer will fetch (from `plan.JARs` or the lock), and
**DECLARED** = `root.dependencies.java` ∪ (each installed package's `dependencies.java`).
Including the root manifest's own Java deps in DECLARED is required so a consumer's directly
declared JARs are not mis-flagged as `extra`.

| Class | Condition | Meaning | Action |
|---|---|---|---|
| **missing** | key ∈ DECLARED, key ∉ INSTALL | registry/lock dropped a dep the package declares (the poiapi case) | **warn + fallback-install** |
| **version-mismatch** | key ∈ both, versions differ | package wants a different version than the install set pins | **warn**; the INSTALL version is authoritative and is kept |
| **extra** | key ∈ INSTALL, key ∉ DECLARED | install set has a JAR no manifest declares (e.g. hand-edited registry metadata) | **warn (informational)**; installed as-is |

Only **missing** triggers fallback. `version-mismatch` and `extra` are warn-only — the
resolved/locked set stays authoritative so we never silently change a version or drop a JAR
mid-install.

## 6. Behaviour: cross-check (always on)

For every divergence, emit one stderr warning naming the package, the coordinate, the class,
and the remediation. Example for the poiapi case:

```
warning: poiapi@1.4.0 declares 11 Java dependencies its registry record omits:
  org.apache.poi:poi@5.3.0, org.apache.poi:poi-ooxml@5.3.0, … (11 total)
  Installed from the package manifest as a fallback (--no-manifest-fallback to disable).
  The registry metadata is stale — ask the publisher to re-publish so the registry
  record is authoritative.
```

The warning is intentionally structured (package, class, coordinates) so it can later be
emitted as a machine-readable record for the security pipeline / telemetry
(cf. [gi-registry-workstream-c.md](gi-registry-workstream-c.md) §4).

## 7. Behaviour: fallback (default on)

For **missing** coordinates:

1. Build supplemental `[]manifest.JavaDependency` from the bundled manifests' missing entries
   (dedup by `Key()`; if two packages declare the same missing coordinate at different
   versions, the higher version wins — same rule as `resolver.state.addJARScoped`).
2. Append them to the JAR install list; they flow through the normal
   `InstallJar` path ([`installer.go:388`](../internal/installer/installer.go#L388)) — i.e.
   fetched from Maven Central by coordinate, exactly like registry-sourced JARs.
3. Record them in the lock file marked as manifest-sourced (see [§8](#8-data-model-changes)),
   so subsequent installs and `--production` installs (which read only the lock) are
   deterministic and don't re-diverge.

Fallback is scoped to **production** semantics: a downloaded package's Java deps are always
production for its consumer (matching the resolver's transitive-deps-are-prod rule), so
fallback JARs install under `--production` too.

**Disable:** `fglpkg install --no-manifest-fallback` (and the equivalent for `update`).
With fallback disabled, the cross-check still runs and still warns; the missing JARs are
simply not installed.

## 8. Data model changes

Add a provenance marker to `LockedJAR`
([`internal/lockfile/lockfile.go:138`](../internal/lockfile/lockfile.go#L138)):

```go
// Source records where this JAR entry came from: "" / "registry" (resolved
// from registry metadata) or "manifest" (recovered from a package's bundled
// manifest via the dependency cross-check fallback). Informational; lets a
// reader see which JARs bypassed the registry's declared dependency set.
Source string `json:"source,omitempty"`
```

`FromPlan` sets `"registry"` (or leaves it empty) for resolved JARs; the fallback step sets
`"manifest"`. No lockfile-version bump is needed — the field is additive and
`omitempty`, so old readers ignore it and old locks parse unchanged.

**Lock write ordering.** Today the lock is written from the plan *before* install
([`installer.go:129-137`](../internal/installer/installer.go#L129)) so it survives an
interrupted install. Keep that. In the fallback case only, after supplemental JARs are
discovered and installed, re-write the lock to include them. Common case (no divergence):
unchanged, single write.

## 9. Trust model & security posture

Fallback installs Maven coordinates from the **author-controlled** bundled manifest that were
**not** part of the registry's recorded dependency set. Two observations keep default-on
acceptable *today*:

- It matches the current trust model. JARs are already fetched from Maven Central by
  coordinate with no per-coordinate review; whether a coordinate came from the registry row
  or the bundled manifest, the fetch and trust are identical.
- The alternative — silently installing nothing — is strictly worse: it produces a broken
  install with no signal, which is exactly the failure we are fixing.

But this is a **transitional** stance. Once the registry's dependency metadata is the
reviewed, authoritative surface (the direction of [package-signing.md](package-signing.md)
and the [security/](../security/README.md) pipeline), a `missing` divergence should stop
being "silently recover" and become "the reviewed set and the shipped package disagree —
that is a finding." The rollout mirrors signing's `warn → require`:

| Phase | `missing` divergence behaviour |
|---|---|
| **0 (this spec)** | warn + fallback-install; `--no-manifest-fallback` opt-out |
| **1** | warn + fallback, and emit a structured record to the registry/telemetry |
| **2** | when the package is signed/provenanced, treat divergence as a hard mismatch: refuse the fallback and route to `needs_human` (consistent with [security/scoring-policy.md](../security/scoring-policy.md) §5) |

`LockedJAR.Source == "manifest"` is the audit trail that makes Phase 2 enforceable.

## 10. Scope decisions

- **Java deps: full cross-check + fallback.** This is the demonstrated failure mode and JARs
  are fetched by flat coordinate, so recovery is a simple additive fetch.
- **FGL deps: cross-check warn-only, no fallback.** A missing transitive **FGL** dependency
  cannot be recovered additively — it would require re-entering the resolver mid-install
  (fetch versions, apply Genero-compat + semver constraints, download, recurse), a far larger
  change and one that races the parallel install. The realistic failure (poiapi) is
  Java-specific. So: if a bundled manifest declares an `fgl` dependency that was **not**
  resolved, **warn** and advise republish; do not attempt to resolve it. (In practice the
  registry's `fgl` map and the manifest agree far more often than `java`, since `fgl` deps
  drive resolution and a missing one usually fails the build loudly downstream.)
- **Warn-only, never block.** No divergence class aborts an install in Phase 0.

## 11. Edge cases

- **Both empty** (package genuinely has no Java deps): no divergence, no-op.
- **Registry richer than manifest** (`extra`): informational warn; install per the resolved
  set. Common-benign when the root project declares its own JARs — handled by folding
  `root.dependencies.java` into DECLARED (§5).
- **Webcomponent-only package:** manifest not on disk, no Java deps — skipped.
- **Already-installed packages** (lock fast-path, [`installer.go:160`](../internal/installer/installer.go#L160)):
  their manifest is on disk; include them in the DECLARED scan so a stale lock is still
  cross-checked.
- **`url`/`jar` overrides** on a `JavaDependency`: preserved when a fallback JAR is created
  (copy the full struct, not just `groupId:artifactId:version`), so a coordinate with a
  custom download URL still resolves.
- **Concurrency:** the DECLARED-set collection runs after the parallel package pass completes
  (a barrier), so it reads manifests off disk single-threaded — no shared-state races with
  the installers.

## 12. Non-goals

- Verifying the bundled manifest's integrity/authenticity — that is signing
  ([package-signing.md](package-signing.md)).
- Resolving unlisted transitive **FGL** packages (§10).
- Changing how JARs are fetched or verified (still Maven Central by coordinate via
  `InstallJar`).
- Any registry-side change — this is entirely client-side. (A registry-side backfill of
  pre-2026-07-01 versions' `dependencies_json` is a separate, optional cleanup.)

## 13. Test plan

- **Unit — divergence classifier:** table-driven over (DECLARED, INSTALL) → {missing,
  version-mismatch, extra} sets, including the root-manifest-folding case and the
  higher-version-wins dedup.
- **Unit — fallback lock entries:** `Source == "manifest"`, versions preserved, `url`/`jar`
  overrides carried through.
- **Integration — the poiapi regression:** a fake registry returning `dependencies.java: []`
  plus a fixture zip whose manifest declares JARs → assert (a) a warning is emitted, (b) the
  JARs are installed, (c) the lock records them as `manifest`-sourced. With
  `--no-manifest-fallback`: warning emitted, JARs **not** installed.
- **Integration — no false positives:** registry and manifest agree → no warning, single lock
  write, byte-identical lock to today.
- **Integration — lock fast-path:** already-installed package with a stale lock still triggers
  the cross-check.

## 14. Decisions to confirm

1. **Fallback default-on** (recommended) vs. opt-in behind `--manifest-fallback`. Default-on
   restores "it just works" for every package published in the plumbing gap without a manual
   flag; the security posture is addressed by the Phase 1/2 rollout (§9). *Recommend default-on.*
2. **`LockedJAR.Source` field** (recommended) — small additive field, but it is the audit
   trail that makes the Phase 2 enforcement possible. *Recommend adding it.*
3. **FGL divergence: warn-only** (recommended) vs. attempt post-hoc FGL resolution. *Recommend
   warn-only for this spec; a follow-up can tackle FGL recovery if it ever bites.*
4. **Registry backfill** of stale pre-2026-07-01 `dependencies_json` — out of scope here, but
   worth a separate ticket so the fallback stays a safety net rather than load-bearing.
