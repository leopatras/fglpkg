# Spec: `fglpkg self-update` — self-updating binary + periodic update notices

**Status:** 📋 Not started — GIS-255 (spec ready)
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** fglpkg ships as a standalone binary users copy into `PATH` by hand
([README install section](../README.md)). There is no upgrade path: to move from v3.3.0 to v3.4.0
a user must re-download the right asset for their OS/arch, clear the macOS quarantine flag, and copy
it over the old one. Most never do, so field installs drift far behind. fglpkg should be able to
update *itself*, and — like `npm`, `gh`, and `brew` — passively let users know when a newer version
is available.
**Related:** [macos-release-signing.md](macos-release-signing.md) (a self-updated macOS binary must
be signed/notarized or Gatekeeper blocks it — see [§ Platform notes](#platform-notes));
[gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md) (the service-side spec for the
registry endpoint this consumes).

---

## Summary

Two related capabilities:

1. **`fglpkg self-update`** — a new command that downloads the latest stable release binary for the
   current OS/arch, verifies its SHA-256 checksum, and atomically replaces the running executable.
2. **A passive update check** — piggybacked on ordinary command runs (no daemon). At most once per
   check interval, fglpkg asks the registry for the latest version in the background and, if a newer
   one exists, prints a one-line notice **after** the command's own output. On by default; disabled
   in CI, for `dev` builds, and whenever the user opts out.

Both learn the latest version from the **Genero Intelligence registry** (a new
`GET /registry/fglpkg/latest` endpoint), not from GitHub directly, keeping all client network
traffic on the registry the user already trusts and authenticates against. The registry response
carries the download URL and checksum per platform asset, so the client stays agnostic about where
binaries are actually hosted.

Scope is deliberately narrow: **latest stable only** — no version pinning, no pre-releases, no
downgrade.

## Background — how it works today

### The binary knows its own version but can do nothing with it

`cli.Version` / `cli.Build` are stamped at build time via `-ldflags`
([cmd/build.sh:5](../cmd/build.sh#L5)); a plain `go build` leaves the sentinel values:

```go
// internal/cli/cli.go:58-61
Version = "dev"
Build   = "unknown"
```

[`cmdVersion`](../internal/cli/version.go#L26) prints them and nothing else. There is no notion of a
"latest available" version anywhere in the client.

### Releases are GitHub assets with a checksum manifest

[release.yml](../.github/workflows/release.yml) fires on a `v*` tag, runs `cmd/build.sh` to produce
six binaries named `fglpkg-<os>-<arch>[.exe]`, generates `checksums.txt`
(`sha256sum * > checksums.txt`), and uploads all of them to a GitHub Release. So each release already
has both the binaries and a `checksums.txt` at predictable URLs; the registry endpoint
([§ Registry contract](#registry-contract-new)) surfaces it to clients.

### There is a proven pattern for JSON state under `~/.fglpkg`

[credentials.go](../internal/credentials/credentials.go) stores a forward-compatible JSON file
(`credentials.json`, mode 0600) in the fglpkg home resolved by
[`fglpkgHome()`](../internal/cli/cli.go#L2589) (honors `FGLPKG_HOME`, else `~/.fglpkg`). Update-check
state reuses this exact pattern via a new `config.json` ([§ Config & state](#config--state-configjson)).

### The registry client already centralizes base URL + auth

[`registryBase()`](../internal/registry/registry.go#L533) resolves `FGLPKG_REGISTRY` (default
`https://service.generointelligence.ai`) and [`httpGetAuthed`](../internal/registry/registry.go#L543)
performs authenticated GETs. The update check is one more call through this same client.

## Registry contract (new)

A new endpoint the client depends on. **Additive and unauthenticated-friendly** — the latest fglpkg
version is public information; auth, if present, is only used to lift rate limits. The service side
of this endpoint is specified in full in
[gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md); this section is the client's
view of the same contract.

```
GET /registry/fglpkg/latest
200 →
{
  "version": "3.4.0",
  "notes": "https://github.com/4js-mikefolcher/fglpkg/releases/tag/v3.4.0",
  "checksumsUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/download/v3.4.0/checksums.txt",
  "assets": [
    { "os": "darwin", "arch": "arm64", "url": "https://github.com/.../v3.4.0/fglpkg-darwin-arm64" },
    { "os": "linux",  "arch": "amd64", "url": "https://github.com/.../v3.4.0/fglpkg-linux-amd64" },
    { "os": "windows","arch": "amd64", "url": "https://github.com/.../v3.4.0/fglpkg-windows-amd64.exe" }
    // … all six
  ]
}
```

- `version` is the latest **stable** release (no pre-release/`-rc` tags).
- `os`/`arch` use Go's `runtime.GOOS`/`runtime.GOARCH` spellings, so the client matches directly.
- The `url`s point at **GitHub Releases** — GI stores only the version and derives URLs from it; it
  does not host or proxy binaries.
- **Checksums come via `checksumsUrl`, not inline.** GI returns the URL of the release's
  `checksums.txt`; the client fetches and parses it to get the expected SHA-256 for its asset (the
  file is `sha256sum` output: `<hex>  <filename>` lines). This keeps GI a pure URL provider while
  preserving the integrity gate below. If `checksumsUrl` is absent or the fetch fails, self-update
  **aborts** rather than installing an unverified binary.
- A registry that predates this endpoint returns `404`; the client treats that as "no update info"
  (silent no-op for the passive check; a clear message for explicit `self-update`).

A new `registry.FetchLatestFGLPkg() (*LatestRelease, error)` wraps this call in
[internal/registry/registry.go](../internal/registry/registry.go), returning a typed struct.

## `fglpkg self-update`

New command wired into the dispatch switch ([cli.go:140+](../internal/cli/cli.go#L140)) as
`self-update` (alias `upgrade`), backed by a new `internal/selfupdate` package.

### Flags

| Flag | Effect |
|---|---|
| `--check` | Report whether an update exists and exit 0 (newer available) / 0 (up to date). Never writes. |
| `--yes`, `-y` | Skip the confirmation prompt (for scripts). |
| `--force` | Re-install even if already on the latest version (repair a corrupt/quarantined binary). |

No `--version` / `--pre` / downgrade — latest stable only, per scope.

### Flow

1. **Guard managed installs.** If `Version == "dev"`, refuse: this is a source build with no release
   to update to. If the running executable lives under a package-manager prefix (e.g. a Homebrew
   Cellar path, or a path not writable by the user), refuse with a hint to use that manager instead.
   Detection is best-effort and conservative — when unsure, proceed and let the atomic-write step
   fail cleanly.
2. **Resolve latest** via `registry.FetchLatestFGLPkg()`. Compare to `cli.Version` using
   [`internal/semver`](../internal/semver). If not newer and not `--force`, print
   `fglpkg is up to date (vX.Y.Z)` and exit.
3. **Select the asset** matching `runtime.GOOS`/`runtime.GOARCH`. If none, error with the manual
   download URL.
4. **Fetch expected checksum.** GET `checksumsUrl`, parse the `sha256sum`-format lines
   (`<hex>  <filename>`), and look up the entry for the selected asset's filename. If `checksumsUrl`
   is missing or the entry can't be found, **abort** — self-update never installs an unverified
   binary.
5. **Confirm** (unless `--yes`): `Update fglpkg vCUR → vNEW? [Y/n]` via the existing
   [`promptYesNo`](../internal/cli/cli.go#L740).
6. **Download** the asset to a temp file **in the same directory as the target executable** (so the
   final rename is same-filesystem and atomic — a cross-device `os.Rename` fails). Stream to disk.
7. **Verify** the computed SHA-256 against the expected value from step 4 using the existing
   [checksum](../internal/checksum) streaming verifier. Mismatch → delete temp, abort, exit non-zero.
   This is the integrity gate — never install an unverified binary.
8. **Swap atomically** (see below), preserving the original file mode; `chmod +x` on Unix.
9. Print `Updated fglpkg vCUR → vNEW`. Refresh `config.json`'s cached latest so the passive check
   goes quiet immediately.

### Atomic swap

- **Unix:** write temp in the target dir, `chmod`, then `os.Rename(temp, exe)` — atomic on the same
  filesystem, and replacing a running binary's inode is safe (the running process keeps the old
  open file).
- **Windows:** a running `.exe` cannot be overwritten. Rename the running exe to `fglpkg.old` (in
  place), `os.Rename` the new binary into the real path, and best-effort delete `fglpkg.old` on the
  next run. Document that a leftover `fglpkg.old` is harmless.
- `os.Executable()` gives the path; resolve symlinks with `filepath.EvalSymlinks` so we replace the
  real file, not a symlink into it.

## Passive update check

### Model

Piggybacked on command runs — **no daemon, no cron**. In `Execute`, after the invoked command
returns, a check runs subject to throttling. To avoid ever slowing a command down, the network call
runs in a goroutine kicked off at command start; the notice is printed at the end **only if the
result is already back** — otherwise it is skipped and the freshly-fetched result is cached for next
time. The check never blocks, never changes exit codes, and never emits errors to the user (network
failures are swallowed silently).

### When it does *not* run

- `Version == "dev"` (source build).
- Any of these is set: `CI`, `FGLPKG_NO_UPDATE_CHECK=1`, or `updateCheck: false` in `config.json`.
- Less than `updateCheckInterval` (default **24h**) since `lastUpdateCheck` in `config.json`.
- The command is itself `self-update` or `version` (they surface version info directly).
- stdout is not a TTY (don't pollute piped/scripted output) — the notice is advisory UX, not data.

### The notice

Printed to **stderr** (keeps stdout clean for piping), one block, after the command's output:

```
─────────────────────────────────────────────
 A new fglpkg is available: 3.3.0 → 3.4.0
 Run 'fglpkg self-update' to upgrade.
─────────────────────────────────────────────
```

## Config & state (`config.json`)

New file in the fglpkg home (`~/.fglpkg/config.json`), same load/save discipline as
[credentials.go](../internal/credentials/credentials.go) — mode 0600, unknown fields preserved,
absent file treated as defaults. A new `internal/config` package owns it.

```json
{
  "updateCheck": true,
  "updateCheckInterval": "24h",
  "lastUpdateCheck": "2026-07-14T09:00:00Z",
  "latestKnownVersion": "3.4.0"
}
```

- `updateCheck` (default `true`) — master opt-out; `FGLPKG_NO_UPDATE_CHECK=1` overrides to off.
- `updateCheckInterval` (default `24h`) — Go duration string; throttles the passive check.
- `lastUpdateCheck` / `latestKnownVersion` — throttle bookkeeping + last result, so a notice can be
  shown from cache without a network call when within the interval.

A `fglpkg config` surface (get/set) is **out of scope**; for now the file is edited by hand or the
env var, and self-update maintains the cache fields. (A future `config` command can wrap it.)

## Platform notes

- **macOS Gatekeeper.** A binary written by fglpkg's own HTTP download does **not** get the
  `com.apple.quarantine` attribute (that flag is applied by browsers, not by API/curl-style
  fetches), so a self-updated binary avoids the quarantine prompt the README documents for browser
  downloads. It must still be **signed and notarized** or Gatekeeper rejects it on some
  configurations — this depends on [macos-release-signing.md](macos-release-signing.md) landing. Note
  the ordering dependency; do not ship self-update on macOS ahead of signed releases.
- **Windows** — the running-exe swap trick above; leftover `fglpkg.old` cleanup is best-effort.
- **Permissions** — if the executable sits in a root-owned dir (`/usr/local/bin`), the rename fails
  with EACCES; catch it and print a clear "re-run with sufficient privileges, or update manually:
  <url>" message rather than a raw error.

## Non-goals

- No version pinning, pre-release channel, or downgrade (`--version`/`--pre` explicitly excluded).
- No background daemon / scheduled task — checks only piggyback on invocations.
- No `fglpkg config` command in this spec (state file is edited by hand / env for now).
- No auto-apply — the passive check only *notifies*; it never updates without the user running
  `self-update`.
- Registry-side implementation of `/registry/fglpkg/latest` is specified separately in
  [gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md).

## Testing

- **semver comparison**: current vs latest → newer / equal / older, including `dev`.
- **`registry.FetchLatestFGLPkg`**: maps the JSON contract; asset selection by GOOS/GOARCH; missing
  asset → clear error. (Table-driven, mocked HTTP as in
  [registry_test.go](../internal/registry/registry_test.go).)
- **checksum gate**: a tampered download (wrong sha256) aborts and leaves the original binary
  untouched — the critical safety test.
- **atomic swap**: temp written in target dir; original mode preserved; Windows `.old` path
  exercised behind a GOOS guard.
- **throttle logic**: check skipped when `<interval`, when `CI`/env/`updateCheck:false`, when `dev`,
  and when stdout is not a TTY; runs when stale. Pure function over (now, config, env) — no network.
- **config.json**: defaults when absent; unknown fields round-trip; mode 0600.

## Rollout

1. Land `internal/config` + the passive check (safe no-op until the registry endpoint exists — a 404
   is swallowed, so nothing regresses).
2. Land `internal/selfupdate` + the `self-update` command.
3. Gate the macOS path on signed/notarized releases ([macos-release-signing.md](macos-release-signing.md)).
4. Document `self-update`, the notice, `FGLPKG_NO_UPDATE_CHECK`, and `config.json` in
   [README.md](../README.md) and [docs/user-guide.md](../docs/user-guide.md).
