# Global vs Local Installs — fglpkg's Model Compared to npm's `-g`

**Date:** 2026-07-11
**Audience:** fglpkg developers and users coming from the npm ecosystem
**Scope:** what "global" actually means in npm, and the deliberate choices
in fglpkg's `--global` / `--local` model
**Related:** [version-switching-and-constraints.md](version-switching-and-constraints.md)
— what happens to installed packages when the runtime version switches

---

## What `npm install -g` really does

A common assumption is that `npm install -g` installs a package either
"for all users of the machine" or "for all projects of a user". Strictly
it does neither: it installs into **one shared location determined by
npm's `prefix`**, and whether that location is machine-wide or per-user
depends on how Node/npm was installed:

| Setup | Global prefix | Effective scope |
|---|---|---|
| Classic system-wide Node on Linux/macOS | `/usr/local` (or `/usr`) | **Machine-wide** — `/usr/local/lib/node_modules`, bins in `/usr/local/bin`; needs `sudo` |
| nvm (the de-facto standard on dev machines) | `~/.nvm/versions/node/<v>` | **Per-user, per Node version** |
| User-configured prefix (`npm config set prefix ~/.npm-global`) | user's home | **Per-user** |
| Windows (even with machine-wide Node) | `%APPDATA%\npm` | **Per-user** by default |

Two nuances that surprise people:

1. **"Global" only means "not project-local".** It is the CLI-tools
   location, nothing more. A project's `require()`/`import` does **not**
   resolve globally installed packages — only the package binaries end
   up on `PATH`. `-g` is therefore *not* "install a library once, use it
   in all my projects"; npm's own guidance keeps library dependencies
   per-project in `node_modules`, always.
2. Historically npm documented `-g` as installing "for all users", but
   on modern setups (nvm everywhere, `%APPDATA%` on Windows) the global
   dir is per-user far more often than machine-wide.

## fglpkg's model

fglpkg keeps the two tiers, but with explicit, predictable semantics:

- `--global` / `-g` — **per-user**, always: `~/.fglpkg` (override with
  `FGLPKG_HOME`). No elevation is ever needed; two users on one machine
  each have their own `~/.fglpkg`.
- `--local` / `-l` — **per-project**: `.fglpkg/` next to `fglpkg.json`
  (add it to `.gitignore`). Auto-detected when the current directory is
  a project.

Deliberate differences from npm:

- **Global packages are usable from projects.** Unlike npm's `-g`,
  fglpkg's global store is a first-class library location:
  `fglpkg env --global` puts the installed packages on
  `FGLLDPATH`/`CLASSPATH`, so shell profiles can make them available
  regardless of the current directory. The npm caveat "global is only
  for CLI tools" does not apply.
- **No machine-wide tier, no sudo.** fglpkg never writes outside the
  user's home (or the project). This sidesteps the classic
  `/usr/local` permission problems that plague system-wide npm setups.
- **Same environment mechanism for both tiers** — `env` emits the
  export lines either way; only the directories differ.

## Machine-wide sharing, if ever needed

There is intentionally no third tier. Where a machine-wide shared store
is genuinely wanted (e.g. a build server with many CI users), point
`FGLPKG_HOME` at a shared directory. The usual multi-writer caveats
apply (file ownership/permissions across users, concurrent installs) —
the same problems `/usr/local` npm setups have; keeping the shared store
written by a single service account is the sane configuration.
