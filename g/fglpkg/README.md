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
./fglpkg ...  # launcher script (exec fglrun main.42m)
```

## Scope

Phase 1 (consumer): `init` (incl. `--template library|app|webcomponent`),
`install`, `remove`, `update`, `list`, `env` (incl. `--gst`, `--gwa`),
`search`, `info`/`view`, `pack`, `version`, `help` + per-command `--help`.

Phase 2 (publisher + auth): `publish` (incl. `--dry-run`, `--ci`,
`--private`/`--public`), `login` (browser OAuth with PKCE, or
`--token <PAT>`), `logout`, `whoami`, `outdated`, silent OAuth token
refresh (incl. the registry 401-retry hook).

Not yet ported (the CLI reports this and defers to the Go binary):
`audit`, `sbom`, `completion`, `workspace`, `run`, `bdl`, `docs`.

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
| `fglpkgutils.4gl` | shared helpers (modeled on gwa's `gwautils.4gl`) |

## Deviations from the Go implementation

- Zip handling shells out to `unzip`/`zip` (Unix) or `tar` (Windows)
  instead of `archive/zip`; entries are pre-scanned for zip-slip.
- Downloads run sequentially (`FGLPKG_INSTALL_CONCURRENCY` is ignored).
- JSON whitespace of written files matches Go's 2-space
  `MarshalIndent` layout; key order and omission rules are identical.
- The OAuth loopback callback binds a scanned port range (9101-9300 on
  127.0.0.1) instead of Go's ephemeral port 0.
- `FGLPKG_BROWSER` overrides the browser-launch command (testing /
  headless environments) — a 4GL-port extension.
- Built zips differ byte-wise from the Go binary's (external Info-ZIP vs
  Go's archive/zip); contents and entry lists are identical.

## E2E smoke testing

`test/mock_registry.py <port> <statedir>` implements enough of the
registry + OAuth protocol to exercise publish/login/whoami/outdated
headlessly (`FGLPKG_REGISTRY=http://127.0.0.1:<port>`, and
`FGLPKG_BROWSER="curl -sL"` for the browser OAuth flow).
