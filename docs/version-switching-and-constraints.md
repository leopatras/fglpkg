# Runtime Version Constraints and Version Switching — fglpkg vs npm/Node

**Date:** 2026-07-12
**Audience:** fglpkg developers and users coming from the npm ecosystem
**Scope:** what happens to installed packages when the runtime version
changes underneath them — Genero for fglpkg, Node for npm — and how the
two toolchains enforce version constraints
**Related:** [global-vs-local-installs.md](global-vs-local-installs.md);
the runnable version of the fglpkg side lives in `samples/v5` and
`samples/v6` (see `samples/README.md`)

---

## The scenario

A package requires a newer runtime major (fglpkg: `"genero": ">=6.00"`;
npm: `"engines": { "node": ">=25" }`). You install it while running the
matching runtime, then switch the environment back to the older major.
Is the package still listed? Does anything warn you? Does it still run?

## fglpkg semantics (verified against both implementations, 2026-07-11)

- **Install under the wrong version: hard error.** The resolver checks
  the `genero` constraint of every candidate version
  (`filterByGenero`), and artifact selection is per Genero *major*
  (`genero5`/`genero6` variants). A fresh install of a `>=6.00` package
  under 5.00.x fails:
  `no version of "sample-v6" is compatible with Genero 5.00.05`.
- **`fglpkg list` after switching: still listed.** `list` is a pure
  filesystem scan of the `packages/` directory (name + manifest
  version); it never consults the detected Genero version. Installed is
  a disk fact.
- **Running after switching: eager load error.** BDL packages ship
  compiled `.42m` p-code for a specific Genero major; a genero6 module
  under a 5.x `fglrun` fails immediately with a bytecode-version error.
- Handy for experiments: `FGLPKG_GENERO_VERSION` overrides detection.

## npm/Node semantics

- **Install under the wrong version: warning only (by default).**
  `engines` is advisory: npm prints an `EBADENGINE` warning and
  installs anyway unless `engine-strict=true` is set in `.npmrc`.
  (Yarn classic enforces engines by default; pnpm is opt-in like npm.)
- **`npm ls` after switching: still listed (project-local).**
  `node_modules/` lives in the project and survives the switch; `npm
  ls` validates the dependency *tree*, not `engines`.
- **The global twist:** under nvm every Node version has its own global
  prefix, so `npm ls -g` under node 24 shows a *different* tree — a
  package globally installed under node 25 effectively disappears.
  fglpkg's global home is per-user, not per-runtime-version, so global
  packages stay visible across switches.
- **Running after switching: depends on the package kind.**
  - *Pure JS* using node-25-only APIs breaks **lazily** — at runtime
    when the missing API is hit (or at import time if used top-level);
    it may even mostly work. JavaScript ships as source; there is no
    compile gate.
  - *Native addons* are ABI-bound (`NODE_MODULE_VERSION`); `require()`
    fails **eagerly** with "was compiled against a different Node.js
    version … please reinstall or rebuild".

## Side by side

| | fglpkg | npm (default) |
|---|---|---|
| Install under wrong runtime version | hard error at resolve | `EBADENGINE` warning, installs anyway |
| Listed after switching (project-local) | yes | yes |
| Listed after switching (global) | yes (per-user home) | no under nvm (per-version prefix) |
| Running the wrong-version package | eager load error (compiled `.42m`) | lazy runtime break (pure JS) / eager ABI error (native addons) |

## Takeaways

- Every BDL package behaves like npm's **native addon** case — the
  strictest one — because packages ship compiled p-code, not source.
  That justifies fglpkg's stricter install-time stance (hard error
  where npm merely warns).
- The gap is identical in both ecosystems: `list`/`ls` won't tell you
  that what is installed can no longer run under the current runtime.
  If that ever becomes a pain point, `fglpkg.lock` already records
  `generoMajor` per package, so `fglpkg list` could cheaply annotate
  incompatible entries (e.g. `sample-v6 1.0.0 (genero6 — incompatible
  with current 5.00.x)`) — an improvement npm never implemented.
