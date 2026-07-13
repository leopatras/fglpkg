# fglpkg — Genero 4GL implementation

A port of the Go fglpkg package manager to Genero BDL, written in the
language it serves. All modules live in `PACKAGE fglpkg` (flat, gwa-style:
one `.4gl` module per Go package), imported as `IMPORT FGL fglpkg.<module>`
with `FGLLDPATH=<repo>/g`.

## Build & test

```bash
cd g/fglpkg
make          # compile all modules (fglcomp -M -Wall)
make test     # build + run the test programs in test/
./fglpkg ...  # launcher script (exec fglrun main.42m; fglpkg.bat on Windows)
```

The launcher sets `FGL_LENGTH_SEMANTICS=CHAR` — required for correct
Unicode handling (see `g/BENCHMARKS.md`, "cmpBytes silently corrupted
multi-byte characters"). Invoking `fglrun main.42m` directly without
the launcher runs under the interpreter's BYTE-semantics default and
is not supported.

## Scope

Phase 1 (consumer): `init` (incl. `--template library|app|webcomponent`),
`install`, `remove`, `update`, `list`, `env` (incl. `--gst`, `--gwa`),
`search`, `info`/`view`, `pack`, `version`, `help` + per-command `--help`.

Phase 2 (publisher + auth): `publish` (incl. `--dry-run`, `--ci`,
`--private`/`--public`), `login` (browser OAuth with PKCE, or
`--token <PAT>`), `logout`, `whoami`, `outdated`, silent OAuth token
refresh (incl. the registry 401-retry hook).

Phase 3 (full parity): `workspace`/`ws` (monorepo members, topo-sorted
resolver/env integration), `audit` (OSV.dev, exit codes 0/1/2), `sbom`
(CycloneDX 1.5), `completion` (bash/zsh/fish/powershell), `bdl` (exact
child exit-code propagation), `run`, `docs`.

Every command of the Go binary is now ported.

## Module map (4GL ← Go)

| Module            | Mirrors                                        |
|-------------------|------------------------------------------------|
| `main.4gl`        | `cmd/fglpkg/main.go`                           |
| `cli.4gl`         | `internal/cli/{cli,info,version,pack}.go`      |
| `commands.4gl`    | `internal/cli/commands.go`                     |
| `templates.4gl`   | `internal/cli/templates.go`                    |
| `manifest.4gl`    | `internal/manifest`                            |
| `semver.4gl`      | `internal/semver`                              |
| `genero.4gl`      | `internal/genero`                              |
| `resolver.4gl`    | `internal/resolver`                            |
| `installer.4gl`   | `internal/installer`                           |
| `lockfile.4gl`    | `internal/lockfile`                            |
| `checksum.4gl`    | `internal/checksum`                            |
| `credentials.4gl` | `internal/credentials` (PAT/`FGLPKG_TOKEN`)    |
| `registry.4gl`    | `internal/registry`                            |
| `env.4gl`         | `internal/env`                                 |
| `hooks.4gl`       | `internal/hooks`                               |
| `glob.4gl`        | glob logic from `internal/cli` + `internal/github` |
| `ignore.4gl`      | `internal/cli/ignore.go`                       |
| `pack.4gl`        | zip building from `internal/cli`               |
| `publish.4gl`     | `cmdPublish` + `publish_validation.go` + `readme.go` |
| `oauth.4gl`       | `internal/oauth` (PKCE, loopback callback, browser) |
| `outdated.4gl`    | `internal/cli/outdated.go`                     |
| `completion.4gl`  | `internal/cli/completion.go`                   |
| `runner.4gl`      | `cmdBdl`/`cmdRun`/`cmdDocs` from `internal/cli/cli.go` |
| `workspace.4gl`   | `internal/workspace`                           |
| `sbom.4gl`        | `internal/cli/sbom.go`                         |
| `audit.4gl`       | `internal/audit` + `internal/cli/audit.go`     |
| `fglpkgutils.4gl` | shared helpers (modeled on gwa's `gwautils.4gl`) |

## Deviations from the Go implementation

- Zip handling shells out to `unzip`/`zip` (Unix) or `tar` (Windows)
  instead of `archive/zip`; entries are pre-scanned for zip-slip.
- Downloads for one phase (BDL packages, webcomponents, or JARs) run
  concurrently, bounded by `FGLPKG_INSTALL_CONCURRENCY`, by shelling
  out to a single `curl --parallel` invocation — falls back to the
  original one-request-at-a-time `com.HttpRequest` path when `curl`
  isn't on PATH. See [BENCHMARKS.md](../BENCHMARKS.md#parallel-packagejar-downloads--4gl-port-vs-go-implementation).
- JSON whitespace of written files matches Go's 2-space
  `MarshalIndent` layout; key order and omission rules are identical.
- The OAuth loopback callback binds a scanned port range (9101-9300 on
  127.0.0.1) instead of Go's ephemeral port 0.
- `FGLPKG_BROWSER` overrides the browser-launch command (testing /
  headless environments) — a 4GL-port extension.
- Built zips differ byte-wise from the Go binary's (external Info-ZIP vs
  Go's archive/zip); contents and entry lists are identical.
- The sbom `serialNumber` uses `security.RandomGenerator.CreateUUIDString()`
  (lowercased) instead of Go's hand-rolled UUIDv4 — still a valid urn:uuid.
- `fglpkg audit` reproduces the Go binary's pluralization bug
  ("2 vulnerabilitie found") for byte-parity; fix both sides together.

## E2E smoke testing

`test/mock_registry.py <port> <statedir>` implements enough of the
registry + OAuth protocol to exercise publish/login/whoami/outdated
headlessly (`FGLPKG_REGISTRY=http://127.0.0.1:<port>`, and
`FGLPKG_BROWSER="curl -sL"` for the browser OAuth flow). It also serves
`POST /v1/query` as an OSV.dev stand-in for `fglpkg audit`
(`FGLPKG_AUDIT_URL=http://127.0.0.1:<port>/v1/query`), with canned
responses read from `<statedir>/osv.json` (`{purl: {vulns: [...]}}`).
