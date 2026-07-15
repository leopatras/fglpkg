# fglpkg samples

Interdependent BDL packages, a pure webcomponent package, and two
consumer projects, wired up to demonstrate the full
publish → install → build → run cycle with both fglpkg implementations
(the Go binary and the Genero 4GL port).

| Directory | Package      | Module/Component | Depends on / requires       |
|-----------|--------------|-------------------|-----------------------------|
| `A/`      | `sample-a`   | `a.4gl`           | —                           |
| `B/`      | `sample-b`   | `b.4gl`           | sample-a, sample-c          |
| `C/`      | `sample-c`   | `c.4gl`           | sample-a, sample-b          |
| `v5/`     | `sample-v5`  | `v5.4gl`          | Genero `>=5.00.03` (`base.Channel.getExitStatus`) |
| `v6/`     | `sample-v6`  | `v6.4gl`          | Genero `>=6.00` (`prometheus` package) |
| `WC/`     | `sample-img` | webcomponent `img`| — (pure webcomponent, no BDL content) |
| `D/`      | `sample-d`   | `d.4gl`           | sample-a, -b, -c (app)      |
| `E/`      | `sample-e`   | `imgdemo.4gl`     | sample-img (app)            |

Each package module exposes `FUNCTION main()` displaying
`Hello package <X>`; B and C also call into their dependencies
(`CALL a.main()` etc.). D is an application project: its `MAIN` calls
all three packages after installing them with `fglpkg install`. E is a
second application project demonstrating a *webcomponent* dependency
instead of a BDL one: it installs `sample-img` and its own
`imgdemo.per` form references the installed component with
`WEBCOMPONENT ... COMPONENTTYPE = "img"`.

## Run it

```bash
make demo-4gl     # end-to-end with the Genero 4GL fglpkg (g/fglpkg)
make demo-go      # end-to-end with the Go fglpkg (bin/fglpkg-go, built on demand)
make clean
```

A demo target: precompiles the package modules, starts a private mock
registry (`g/fglpkg/test/mock_registry.py` on port 18930 — override
with `REGISTRY_PORT=...`), publishes A, C, B, v5, v6, WC with
`publish --ci`, then in `D/` runs `fglpkg install`, evaluates
`fglpkg env`, compiles `d.4gl` and runs it, and in `E/` runs
`fglpkg install`, evaluates `fglpkg env`, and compiles `imgdemo.per` +
`imgdemo.4gl` (not run — see below). Everything is sandboxed:
`FGLPKG_HOME` points into `samples/.fglpkg-home`, credentials come from
`FGLPKG_TOKEN` (the mock accepts `gpr_e2e_pat`), and the registry
process is stopped when the target finishes.

## Things this sample demonstrates deliberately

- **A dependency cycle**: sample-b and sample-c depend on each other.
  The registry accepts it, the resolver handles it (each package is
  resolved once; check the `required by:` lines of `fglpkg install`),
  and the `IMPORT FGL` cycle compiles. Two consequences worth noting:
  - `b.main()` and `c.main()` guard against re-entry — without the
    guard the mutual calls would recurse forever at runtime;
  - neither B nor C can be compiled first inside its own directory
    (`fglcomp` resolves `IMPORT FGL` from `FGLLDPATH` only as compiled
    `.42m`), so the `modules` target compiles them together in a flat
    staging directory and ships the prebuilt `.42m` in the package zip.
  fglpkg *workspaces* reject cycles by design (`workspace dependency
  cycle`) — this sample uses plain registry packages on purpose.
- **Naming rules**: registry package names must be 2–64 chars of
  lowercase letters, digits and hyphens — hence `sample-a` for the
  package in `A/`. Module files must be unique across `FGLLDPATH`
  (`IMPORT FGL a` finds `a.4gl`/`a.42m`), so each package names its
  module after itself instead of `main.4gl`.
- **Publish requirements**: `publish --ci` needs `FGLPKG_TOKEN` and a
  manifest with `repository` set; `--ci` prints the machine-readable
  `fglpkg-published ...` status line.
- **Genero version constraints and variants** (`v5/`, `v6/`):
  `sample-v5` requires Genero `>=5.00.03` (it calls
  `base.Channel.getExitStatus()`, introduced there), `sample-v6`
  requires `>=6.00` (`IMPORT prometheus`). Artifacts are published per
  Genero *major* — the demo publishes sample-v5 twice, once natively
  (genero6 variant) and once with `FGLPKG_GENERO_VERSION=5.00.05`
  (genero5 variant), so it is installable in both environments;
  sample-v6 exists only as genero6.

  The `FGLPKG_GENERO_VERSION` override makes version-switch experiments
  easy. Semantics worth knowing (verified against both implementations):
  - `fglpkg list` reports what is **on disk** — it scans the
    `packages/` directory and never consults the Genero version. A
    package installed under 6.00 stays listed after switching to a
    5.0x environment.
  - The Genero version matters at **resolve time**: a fresh
    `fglpkg install` under 5.00.05 fails with
    `no version of "sample-v6" is compatible with Genero 5.00.05`,
    while sample-v5 resolves to its genero5 variant.
- **Webcomponent packages** (`WC/`, `E/`): `sample-img` is a *pure*
  webcomponent package per `specs/webcomponent-packages.md` — its
  manifest declares `"webcomponents": ["img"]` and nothing BDL-side
  (no `programs`/`files`/`main`), so it publishes under the
  `webcomponent` variant (not `genero<N>`) and needs no Genero-version
  fan-out. `fglpkg install` in `E/` extracts it straight into
  `.fglpkg/webcomponents/img/`, and `fglpkg env` adds `.fglpkg/` to
  `FGLIMAGEPATH` so `E/imgdemo.per`'s
  `WEBCOMPONENT img=..., COMPONENTTYPE = "img"` resolves it. The demo
  only *compiles* `imgdemo.per`/`imgdemo.4gl` — actually rendering a
  webcomponent needs a browser-capable front end (GBC/GWC), which this
  headless `FGLGUI=0`/`TERM=xterm` demo doesn't have; compiling is
  enough to prove the install/discovery path is correct. `E/public/`
  holds three tiny generated placeholder images (plain shapes, not
  external assets) that `imgdemo.4gl`'s menu switches between via the
  installed `img` widget.
