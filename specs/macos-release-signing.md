# Spec: Sign & notarize the macOS release binaries

**Status:** Draft
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** [Issue #11](https://github.com/4js-mikefolcher/fglpkg/issues/11) — the macOS release
binaries (`fglpkg-darwin-arm64`, `fglpkg-darwin-amd64`) trigger a Gatekeeper block —
*"fglpkg cannot be opened because the developer cannot be verified"* — when a user downloads them
through a **browser**. They are unsigned and un-notarized, so Gatekeeper refuses to run the
quarantined file on first launch.
**Related:** [Issue #11](https://github.com/4js-mikefolcher/fglpkg/issues/11);
[specs/package-signing.md](package-signing.md) (a *different* signing concern — registry **artifact
provenance**, not CLI-binary distribution trust; see [§9](#9-non-goals));
the shipped stopgap in [README.md](../README.md) ("macOS Gatekeeper warning").

---

## Summary

Sign and notarize the two macOS release binaries in the release pipeline, using **4Js's existing
Apple Developer ID** (`Developer ID Application: Four Js Development Tools`, Team `QESJC2GV7Y` — the
same identity that signs Genero Studio). This makes Gatekeeper accept a browser-downloaded binary
outright, closing Issue #11 for good.

The mechanism:

1. Keep the existing single Linux release runner. Add [`rcodesign`](https://github.com/indygreg/apple-platform-rs)
   (the open-source `apple-codesign` project) steps that **sign** each darwin binary with the
   Developer ID cert + hardened runtime, then **notarize** them via an App Store Connect API key —
   all from Linux, no macOS runner required.
2. Fix the checksum ordering: checksums must be generated **after** signing (signing rewrites the
   Mach-O, changing its hash).
3. The already-shipped README workaround stays as a fallback for older releases and offline users.

**No product-code change** — this is entirely a release-pipeline (`.github/workflows/release.yml`)
and credentials change.

## 1. The problem, precisely

- A browser (Safari, Chrome, …) tags every download with the `com.apple.quarantine` extended
  attribute. `cp` preserves it, so it rides along into `/usr/local/bin/fglpkg`.
- On first execution of a **quarantined** file, Gatekeeper evaluates it. An unsigned /
  un-notarized binary is **refused**.
- Downloading via `curl` avoids the block only because curl doesn't set the quarantine xattr —
  incidental, not a fix. Most users download via a browser.

The binary is fine; this is purely a distribution-trust gap. The fix is to make the binary carry a
Developer-ID signature **and** an Apple notarization ticket.

## 2. Background — the current release pipeline

[.github/workflows/release.yml](../.github/workflows/release.yml) on a `v*` tag push:

```
runs-on: ubuntu-latest
  → ./cmd/build.sh            # cross-compiles 6 BARE binaries into ./bin/
  → sha256sum * > checksums.txt
  → gh release create ${TAG} ./bin/* --generate-notes
```

[cmd/build.sh](../cmd/build.sh) produces bare executables (`-s -w` stripped) for linux/darwin/windows
× arm64/amd64. The two darwin assets are `bin/fglpkg-darwin-arm64` and `bin/fglpkg-darwin-amd64`,
uploaded as-is. Two consequences for this work:

- **Checksums are computed pre-sign today** → they would not match the signed binaries. Signing
  must happen *before* `sha256sum`.
- `--generate-notes` builds notes from commits/PRs, so it needs no change; the user-facing
  Gatekeeper guidance lives in the README (persistent), which is already done.

## 3. Prerequisites — credentials

4Js already owns the required certificate (verified: Genero Studio is signed by
`Developer ID Application: Four Js Development Tools (QESJC2GV7Y)`). The Genero Studio macOS release
owner can produce everything below; **none of it is pasted into chat or committed** — it goes into
GitHub Actions secrets.

### 3.1 Signing certificate

- The **"Developer ID Application"** certificate **with its private key**, exported from Keychain
  Access as a **`.p12`** (PKCS#12), plus the export **password**.
- Must be the *Developer ID Application* type (for direct distribution) — not an "Apple
  Distribution" / Mac App Store cert.

### 3.2 Notarization credential — App Store Connect API key (recommended)

- App Store Connect → **Users and Access → Integrations → Keys** → create a key with the
  **Developer** role. Yields: the **`.p8`** private key (downloadable once), the **Key ID**, and the
  **Issuer ID** (a UUID).
- Prefer the API key over an Apple-ID + app-specific-password: it isn't tied to a person, doesn't
  expire like a password, and is the modern notary path.
- *Prerequisite gotcha:* the Apple account must have **accepted the current developer license
  agreements**, or notary submissions fail with an authorization error.

### 3.3 GitHub Actions secrets

`rcodesign` bundles the three notary values into one JSON via `encode-app-store-connect-api-key`,
so the notary credential collapses to a single secret. Run **once, locally**:

```bash
rcodesign encode-app-store-connect-api-key <ISSUER_ID> <KEY_ID> AuthKey_<KEY_ID>.p8 \
  > notary-key.json
```

Then set three repository secrets (base64, since Actions secrets are text):

| Secret | Contents |
|---|---|
| `MACOS_CERT_P12` | `base64 -i DeveloperIDApplication.p12` |
| `MACOS_CERT_PASSWORD` | the `.p12` export password |
| `MACOS_NOTARY_API_KEY_JSON` | `base64 -i notary-key.json` (bundles Issuer ID + Key ID + `.p8`) |

*(This refines the earlier 5-secret sketch — folding Issuer ID + Key ID + `.p8` into the one
`encode-app-store-connect-api-key` JSON is cleaner and is what `rcodesign notary-submit` consumes
directly.)*

## 4. Approach — `rcodesign` on the Linux runner

`rcodesign` signs and notarizes Apple binaries from any OS, so the existing `ubuntu-latest` runner
is kept. New steps, inserted **after** the build and **before** checksums:

```yaml
      - name: Install rcodesign
        run: |
          curl -L <apple-codesign-release>/rcodesign-<ver>-x86_64-unknown-linux-musl.tar.gz \
            | tar xz && sudo mv rcodesign /usr/local/bin/     # or `cargo install apple-codesign`; pin the version

      - name: Decode signing secrets
        run: |
          echo "$MACOS_CERT_P12"          | base64 -d > cert.p12
          printf '%s' "$MACOS_CERT_PASSWORD" > cert.pw
          echo "$MACOS_NOTARY_API_KEY_JSON" | base64 -d > notary-key.json
        env:
          MACOS_CERT_P12:          ${{ secrets.MACOS_CERT_P12 }}
          MACOS_CERT_PASSWORD:     ${{ secrets.MACOS_CERT_PASSWORD }}
          MACOS_NOTARY_API_KEY_JSON: ${{ secrets.MACOS_NOTARY_API_KEY_JSON }}

      - name: Sign macOS binaries (Developer ID + hardened runtime)
        run: |
          for b in bin/fglpkg-darwin-arm64 bin/fglpkg-darwin-amd64; do
            rcodesign sign \
              --p12-file cert.p12 --p12-password-file cert.pw \
              --for-notarization \
              "$b"
          done

      - name: Notarize
        run: |
          # one submission for both binaries (Apple notarizes each contained cdhash)
          zip -j fglpkg-macos-notarize.zip bin/fglpkg-darwin-arm64 bin/fglpkg-darwin-amd64
          rcodesign notary-submit --api-key-file notary-key.json --wait fglpkg-macos-notarize.zip

      - name: Cleanup secrets
        if: always()
        run: rm -f cert.p12 cert.pw notary-key.json fglpkg-macos-notarize.zip
```

Notes:
- **`--for-notarization`** sets all the notarization prerequisites in one flag — hardened runtime,
  a Developer ID certificate, and a secure timestamp; `rcodesign` applies an Apple timestamp by
  default. Verify against the pinned `rcodesign` version.
- `--p12-password-file` (not `--p12-password`) keeps the password out of the process argument list.
- `--wait` blocks until Apple returns *accepted*/*rejected*; a rejection must **fail the job** so a
  bad release never publishes. On rejection, `rcodesign notary-log` prints Apple's reasons.
- Batching both binaries into one notarization zip is an optimization — Apple notarizes by
  **cdhash**, so the extracted bare binaries are covered even though we ship them un-zipped
  ([§5](#5-the-stapling-caveat)).

**Alternative considered — Apple's native tooling on a `macos-latest` runner** (`codesign` +
`xcrun notarytool`). More "official" and what the Studio team likely uses, but it adds a second
runner/job and splits the build. Recommend `rcodesign` for a single-runner pipeline; revisit if the
org standardizes on Apple-native tooling. A middle path is the maintained
[`indygreg/apple-code-sign-action`](https://github.com/indygreg/apple-code-sign-action), which wraps
`rcodesign` as a GitHub Action (see [§10](#10-decisions-to-confirm)).

## 5. The stapling caveat

Apple's notarization ticket can be **stapled** (embedded, for offline verification) only to
containers with a defined ticket location — `.app` bundles, `.dmg`, `.pkg`. A **standalone Mach-O
executable has no such location, so it cannot be stapled.**

Consequence: our bare binaries are **notarized but not stapled**. On first run of a quarantined
copy, Gatekeeper does an **online** check of the binary's cdhash against Apple's service and
allows it. This covers essentially all users (they're online when installing a package manager).

If offline-first-run or a cleaner install UX is later required, wrap the darwin binaries in a
**notarized, stapled `.pkg`** (needs the separate *Developer ID Installer* cert) or `.dmg`. That
changes the download from "bare binary you `cp`" to "installer you run" — deliberately **out of
scope** here ([§9](#9-non-goals)); the online-checked bare binary resolves Issue #11.

## 6. Pipeline change summary

New order in [release.yml](../.github/workflows/release.yml):

```
build (unchanged) → install rcodesign → decode secrets → SIGN darwin ×2
  → NOTARIZE (--wait, fail on reject) → sha256sum (now over signed binaries) → gh release
```

Only the two darwin assets change; linux/windows binaries are untouched. The released darwin files
are the signed ones, and `checksums.txt` reflects their post-sign hashes.

## 7. Secrets & security

- Secrets are **base64** in Actions, decoded to short-lived files, and removed in an
  `if: always()` cleanup step. They are never `echo`-ed.
- The API key is scoped to the **Developer** role (least privilege for notarization) and is
  revocable/rotatable independently of anyone's Apple ID.
- Forked-PR builds do not receive secrets; signing runs only on the `v*` tag push in the trusted
  repo context. (If untrusted contributors ever tag, gate the signing job on the actor/repo.)
- Rotation owner = the Genero Studio macOS release owner (they already manage this cert's lifecycle
  for Studio). Note the cert's own expiry — a Developer ID Application cert is typically valid 5
  years; an expired cert fails signing, so track its renewal alongside Studio's.

## 8. Verification / test plan

- **In-pipeline:** `rcodesign notary-submit --wait` gates the release — a non-accepted result fails
  the job. Optionally add `rcodesign print-signature-info bin/fglpkg-darwin-arm64` to log the
  Developer-ID authority for the build record.
- **The real test — run a quarantined copy.** For a **bare CLI binary** the definitive check is
  whether a quarantined copy *runs*: a signed-but-not-notarized quarantined Mach-O is killed by
  Gatekeeper on execution, while a notarized one passes Gatekeeper's online cdhash check and runs.
  Download the release asset **via a browser** (which sets `com.apple.quarantine`) and run it — it
  must print its version with **no Gatekeeper prompt**:
  ```bash
  xattr -p com.apple.quarantine fglpkg-darwin-arm64   # confirm it IS quarantined (browser download)
  ./fglpkg-darwin-arm64 version                        # must run; pre-fix a quarantined copy is blocked
  ```
- **⚠️ `spctl` is not a valid check for bare binaries.** `spctl -a -t exec -vv fglpkg-darwin-arm64`
  reports `rejected (the code is valid but does not seem to be an app)` **whether or not** the binary
  is notarized — its `exec` assessment only understands `.app` bundles, not standalone executables.
  Do **not** read that "rejected" as a signing/notarization failure; use the run-while-quarantined
  test above. `codesign -dvv --verbose=4 fglpkg-darwin-arm64` is still useful for the **signature**
  (`Authority = Developer ID Application: …`, `flags=…(runtime)`, a `Timestamp`) — just not notarization.
- **Clean-room reassessment** (rules out a cached "Open Anyway"): copy to a new path, apply a fresh
  quarantine, and run — a notarized binary still runs:
  ```bash
  cp fglpkg-darwin-arm64 /tmp/gk && xattr -c /tmp/gk
  xattr -w com.apple.quarantine "0001;$(printf '%x' "$(date +%s)");Safari;$(uuidgen)" /tmp/gk
  /tmp/gk version   # must run
  ```
- **Both architectures** (`arm64`, `amd64`) validated. amd64 can be exercised on Apple Silicon via
  Rosetta or on an Intel Mac.
- **Regression guard:** confirm `checksums.txt` entries match the *released* (signed) assets.

## 9. Non-goals

- **Windows Authenticode signing** — the analogous SmartScreen problem for `fglpkg-windows-*.exe`
  is real but a separate effort (different cert/CA, `osslsigncode`/`signtool`). Not in this spec.
- **`.pkg` / `.dmg` installer** and stapling for offline first-run ([§5](#5-the-stapling-caveat)).
- **Universal (`lipo`) binaries** — keep the two thin darwin assets and their current names.
- **Registry artifact signing** — [package-signing.md](package-signing.md) signs the fglpkg
  *packages* served by the registry for provenance; this spec signs the *CLI binary* for macOS
  distribution trust. Different signatures, different purpose; no overlap.
- **Any product-code change** — release pipeline + credentials only.

## 10. Decisions to confirm

1. **Signing tool — `rcodesign` on the existing Linux runner** (recommended) vs. Apple-native
   `codesign`/`notarytool` on a `macos-latest` runner vs. the
   [`apple-code-sign-action`](https://github.com/indygreg/apple-code-sign-action) wrapper.
   *Recommend `rcodesign`* to keep one runner; the Action wrapper is a fine substitute if we prefer
   a maintained step over raw CLI.
2. **Distribution format — notarized bare binaries (online Gatekeeper check)** (recommended) vs. a
   stapled `.pkg`/`.dmg` for offline first-run. *Recommend bare binaries*; it fixes Issue #11 with
   no UX change. Revisit `.pkg` only if an offline/air-gapped need surfaces.
3. **Secret shape — one `MACOS_NOTARY_API_KEY_JSON`** (recommended, via
   `encode-app-store-connect-api-key`) vs. three separate notary secrets encoded in CI. *Recommend
   the single JSON secret.*
4. **README workaround — keep it** (recommended) after notarized binaries ship, as a fallback for
   older releases and offline users, vs. remove it once signing lands. *Recommend keep*, perhaps
   softened to note it's only needed for pre-signing releases.
5. **Cert/secret ownership & rotation** — confirm the Genero Studio release owner will hold and
   rotate `MACOS_CERT_P12` / notary key, and that fglpkg may reuse Team `QESJC2GV7Y` for this
   purpose.
