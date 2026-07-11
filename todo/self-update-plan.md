# Self-Update Plan

How `fglpkg` updates *itself* — today there is no mechanism in either
implementation.

## Current state (verified 2026-07-11)

- No `self-update`/`upgrade` command in the registry of 24 commands.
  `update` and `outdated` operate on the project's *dependencies* only.
- `fglpkg version` prints the baked-in `Version`/`Build` and performs no
  "newer tool available" check; nothing phones home.
- `internal/github` is about distributing *package* zips via GitHub
  Releases, not the tool binary.
- README installation/update story is fully manual: download the latest
  binary from GitHub Releases into `PATH`.

## Vision (Genero implementation): fglpkg is itself a package

The 4GL implementation is a set of `.42m` modules run by `fglrun` — not
a single binary — so it can eat its own dog food: **publish fglpkg as a
registry package, marked with a special flag, and let self-update reuse
the normal package pipeline** (resolve → download → checksum-verify →
extract → swap). Updating fglpkg then works "mostly like any other
package".

Sketch:

- **Packaging**: publish the compiled `g/fglpkg` modules (+ launcher
  script) as package `fglpkg` with a special manifest flag (e.g.
  `"tool": true`). The flag tells clients this is not a library:
  - it never participates in project dependency resolution,
  - it installs into the tool location (e.g.
    `$FGLPKG_HOME/tool/<version>/`), not into
    `packages/`,
  - the registry can gate who may publish it.
- **Command**: `fglpkg self-update` — resolve `fglpkg@latest` (honoring
  the local Genero major for variant selection like any package),
  short-circuit if already current, otherwise download + verify +
  extract to a fresh versioned directory and atomically repoint the
  launcher (symlink/`current` marker) at it.
- **Windows / running-tool problem**: the update runs *inside* the tool
  being replaced, and Windows will not let open files be deleted or
  overwritten. Strategy: **move the old package directory into a temp
  location while fglpkg is running** (renames of open files work where
  deletes do not), extract the new version into the original path, and
  **delete the temp copy after the update is done** — on the next run,
  or best-effort at the end of the current one. Keeping the moved-aside
  copy until the new version has executed once also gives a natural
  rollback point.
- **Safety**:
  - checksum verification is already part of the installer pipeline;
  - keep the previous version's directory (or the temp move-aside) for
    rollback: `fglpkg self-update --rollback`;
  - a post-update smoke check (`fglrun … version`) before deleting the
    old version;
  - version-skew guard: refuse to load if the launcher and module
    versions disagree.

Prerequisite thinking: bootstrap installation (the very first install
cannot use fglpkg) and how the launcher script finds the current tool
directory — both are small once the tool-package layout is fixed.

## Go implementation (secondary)

The Go binary cannot reuse the package pipeline as directly (it is one
native executable, not a package of runnable modules), so the
conventional design applies: `fglpkg self-update` queries the GitHub
Releases API for the latest tag, compares with the built-in `Version`,
downloads the platform asset, verifies its checksum, and atomically
replaces the executable (Unix: rename over; Windows: rename the running
exe aside, write the new one, delete the old on next start). Parity
note: if both implementations grow `self-update`, the CLI surface
(flags, output, exit codes) should match even though the transport
differs (GitHub Releases vs registry package).

## Open questions

- Registry side: does the special tool flag need server support
  (publish gating, hiding from search), or is a client-side convention
  enough for v1?
- One tool package for all platforms vs per-platform variants (the
  launcher differs: shell script vs `.bat`)?
- Should `fglpkg outdated` also report an available tool update as an
  informational line?
