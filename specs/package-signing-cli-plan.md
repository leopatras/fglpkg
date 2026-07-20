# Implementation plan: package signing (fglpkg CLI side)

**Status:** Draft plan — CLI implementation of the signing design.
**Date:** 2026-07-08
**Author:** Maximilian Harold
**Tracking:** Workstream C, item #6 (P0) in [docs/outstanding-work.md](../docs/outstanding-work.md); market-readiness gap #6.
**Design source (locked):** [specs/package-signing.md](package-signing.md) by Mike Folcher.

This is the *implementation plan for the CLI half*. The authoritative design — wire
formats, milestones, error taxonomy — lives in [package-signing.md](package-signing.md).
This document only sequences the fglpkg CLI work and grounds it in the real code.

---

## 0. Scope — what's already done, and what's mine

Per [package-signing.md](package-signing.md), **the entire GI registry side has already
shipped** — Layer 1 signing at publish, the keys manifest endpoint, Layer 2 attestation
endpoints, the backfill, the schema migrations. That is an external repo
(`4js-genero-intelligence`), not this one.

**The deliverable here is the fglpkg CLI side only** — the *consumer* (verify on install)
and *publisher* (attach provenance) halves. That maps to the design's milestones:

- **M2** — Layer 1 CLI (~1 week)
- **M2b** — GA flip (`enforce=warn` → `require`)
- **M4** — Layer 2 CLI (~3 weeks)

M1/M3 are the registry side — done.

> The earlier spike (`internal/cli/sign.go`, a `cosign` shell-out + self-hosted Sigstore
> stack) is **misaligned** with this locked design and will be removed. The real design uses
> the `sigstore-go` *library* (not a `cosign` subprocess) against **public** Sigstore, plus a
> registry-Ed25519 layer unrelated to cosign.

---

## 1. Recommended sequencing

**Build Layer 1 fully, ship it, then build Layer 2.** They are independent PRs by design,
and their dependency profiles are wildly different:

| | Layer 1 (registry-signed) | Layer 2 (Sigstore provenance) |
|---|---|---|
| Crypto | `crypto/ed25519` (**stdlib**) | `github.com/sigstore/sigstore-go` (**large dep tree**) |
| New deps | at most **one tiny** JCS lib | dozens transitively — first heavy deps in a zero-dep repo |
| Testable locally | yes (mock registry + fixtures) | needs **real CI + live Rekor** for E2E |
| Default | **on** (`warn`→`require`) | **opt-in** on both sides |
| Threat closed | transport / mirror / cache tampering | compromised publisher creds + rogue registry |

`go.mod` currently has **zero external dependencies**. Layer 1 stays essentially stdlib-only;
Layer 2 is the first heavy dependency commitment. Layer 1 delivers most of the value at a
fraction of the cost — build it first.

---

## 2. Phase 1 — Layer 1: registry-signed artifacts

### Step 1.1 — `internal/signing/payload.go` + golden vectors ⚠️ build this first
Load-bearing. The Go CLI and the TS Worker must produce **byte-identical** canonical JSON or
*every* signature fails.
- Payload struct `{artifact:{name,version,variant,sha256,size,uploaded_at,uploader}}` — all
  strings except integer `size`.
- Canonicalize with RFC 8785 (JCS). **Decision (see §5):** `github.com/gowebpki/jcs` per the
  spec, vs hand-roll for this constrained shape to keep the repo zero-dep.
- **Golden-vectors test** (`payload → canonical bytes → signature under a known test key`),
  incl. boundary `size` ints and key-order permutations. The identical fixture must also live
  in the GI repo — it is the contract between the two implementations.

### Step 1.2 — the rest of `internal/signing/`
- `keys.go` — Ed25519 verify wrapper (`crypto/ed25519.Verify`, stdlib).
- `root.go` — the pinned root **public** key as an embedded Go constant. Requires the real
  base64 root pubkey + keyid from Mike (blocker — §5).
- `errors.go` — `ErrSignatureMismatch`, `ErrKeyUnknown`, `ErrKeyExpired`,
  `ErrManifestUnverified` (mirror [checksum.ErrMismatch](../internal/checksum/checksum.go)).
- `manifest.go` — fetch `GET /registry/.well-known/keys.json`, verify `{keys,issuedAt}`
  against the pinned root, select the key whose `[validFrom,validTo]` covers the artifact's
  `uploaded_at`.

### Step 1.3 — keys cache
Cache raw signed manifest bytes at `~/.fglpkg/keys.json`. Refetch when older than
`Cache-Control` or missing; never persist a manifest that fails root verify.

### Step 1.4 — registry client fields
Extend [registry.PackageInfo](../internal/registry/registry.go) **and** `VariantInfo` with
`UploadedAt`, `Uploader`, `Signature{keyid,alg,sig}` (nullable). Add a `FetchKeysManifest()`
method. The signature fields must travel with the *selected variant* (that is where
`Checksum`/`DownloadURL` come from).

### Step 1.5 — plumb through the resolver
Thread the signature material from the registry record → `resolver.ResolvedPackage`/`Plan` →
installer, parallel to how `Checksum` already flows.

### Step 1.6 — installer verify
The design says "right after the checksum verify," but that verify lives *inside*
`downloadAndVerify` ([installer.go](../internal/installer/installer.go)), which only receives
`info.Checksum`. Cleaner integration: **verify the signature at the call site** in
`installPackage`, right after `downloadAndVerify` returns, where the full `PackageInfo` is in
scope. The signature covers the sha256, and the sha256 is already verified against the bytes,
so signature verification needs only the record — not the zip bytes. Honor `signing.enforce`:
`warn` logs + continues, `require` returns the error, `off` skips.

### Step 1.7 — lockfile
Add `Signature` + `SignatureKeyID` to `LockedPackage`
([lockfile.go](../internal/lockfile/lockfile.go)). On install-from-lock, re-verify against the
cached manifest — no network call except a keys refresh on TTL expiry.

### Step 1.8 — config + flags
- `signing.enforce` in `~/.fglpkg/config.json`: `require|warn|off`, **default `warn`** in v1.0.
- `FGLPKG_SIGNING=off` env override.
- `install --no-verify-signature` escape hatch.

### Step 1.9 — `fglpkg audit signatures`
Walk the lockfile, re-verify every entry, one line per package (`✓`/`✗ keyid=…`), exit
non-zero on any failure (for CI).

### Step 1.10 — wiring + docs
Add `audit signatures` handling and `--no-verify-signature` to
[completion.go](../internal/cli/completion.go); README "Signature verification" section.

**Ships as M2** with `enforce=warn`. **M2b** flips the default to `require` one release after
the backfill is confirmed complete.

---

## 3. Phase 2 — Layer 2: Sigstore provenance (opt-in)

Only after Layer 1 ships. Introduces `sigstore-go` — the first heavy dependency.

- **`internal/attest/`**: `ci.go` (detect GH Actions/GitLab/Buildkite/CircleCI/GCP + fetch
  OIDC token, audience `sigstore`), `fulcio.go`/`rekor.go`/`dsse.go`/`verify.go`/`errors.go` —
  thin wrappers over `sigstore-go`.
- **`publish --provenance` / `--no-provenance` / `--i-accept-risk`**: OIDC → Fulcio → sign
  DSSE (in-toto + SLSA v1 predicate) → Rekor → build bundle → `PUT …/attestations/:variant` →
  print Rekor URL.
- **`install --require-provenance` / `--verify-provenance=off|warn|require`** + consumer
  manifest `signing.requireProvenance: [...]`: fetch bundle → verify against the pinned public
  trust root → subject-digest match → identity policy (default: subject repo == manifest
  `repository`).
- **`info`**: print a Provenance block when present.
- **`docs/provenance.md`**: per-provider CI setup + troubleshooting.

**Ships as M4.** E2E requires a real GitHub Actions run — needs a staging package + CI workflow.

---

## 4. Testing strategy
- **Unit:** JCS golden vectors (shared with GI); manifest root-verify happy path + every
  failure mode; CI provider table tests; verify pipeline against fixture bundles.
- **Integration:** mock registry serving signed/unsigned/tampered fixtures → installer
  accepts/warns/aborts; a real Sigstore bundle in `testdata/` verifies, a mutated one fails.
- **E2E (pre-release, manual):** publish from staging GH Actions `--provenance`, verify from a
  local install, hand-edit the R2 bundle, observe failure.

---

## 5. Decisions / blockers to raise with Mike
1. **Root public key** — `root.go` pins it; need the real base64 root pubkey (and keyid)
   before Layer 1 verify is real. *(hard blocker for 1.2)*
2. **JCS dependency** — the spec says `gowebpki/jcs`, but this repo is zero-dep. For a
   strings+one-int payload, hand-rolling RFC 8785 is viable and keeps zero-dep.
   **Recommendation: follow the spec (add `gowebpki/jcs`)** — parity with the TS reference impl
   beats dependency purity, and the golden vectors are the real safety net. Confirm.
3. **Golden vectors ownership** — the fixture is a two-repo contract. Who commits it, and does
   GI's CI gate on the same vectors?
4. **Scope confirmation** — is the deliverable **Layer 1 (M2/M2b) only**, with Layer 2 as a
   next phase? Given `sigstore-go`'s weight and the CI-E2E needs, scoping the first deliverable
   to Layer 1 is the recommendation.
