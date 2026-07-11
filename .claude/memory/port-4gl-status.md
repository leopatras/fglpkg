---
name: port-4gl-status
description: Status of the Genero 4GL port in g/fglpkg — full parity reached, branch/commit state, confirmed layout decisions
type: project
last-updated: 2026-07-11
---

# 4GL port status

**Full command parity with the Go binary reached 2026-07-11.** All work
lives on branch `4gl-port`: Phases 1+2 (consumer + publisher/auth) in
commit `06daeb1`, Phase 3 (workspace, audit, sbom, completion,
bdl/run/docs) in `7e928bf`. Branch not merged to main, not pushed.

Verified parity (byte-identical diff vs `bin/fglpkg-go`):

- all 24 per-command `--help` outputs and the global help
- init manifests, pack lists, env, outdated (table + JSON), whoami,
  login --token, publish --dry-run
- all four completion scripts (bash/zsh/fish/powershell)
- sbom (modulo serialNumber uuid, timestamp, tool version string)
- audit: 11 E2E scenarios incl. exit codes 0/1/2, zero-JAR paths,
  flag errors, missing lockfile, HTTP errors
- `bdl` propagates the child's exact exit code (`childExitCode()` decodes
  the Unix wait status)

`make test` in g/fglpkg: 18 test programs, 1093 checks.

**Decisions confirmed by Leo** (do not re-litigate): flat gwa-style
layout, `PACKAGE fglpkg`, Genero 4.01 compat target, shell-out zip
handling, PAT-then-OAuth auth order, key-logic tests + mock-registry
E2E depth, Windows browser open via gwabrowser's `start` + winQuoteUrl
(NOT rundll32).

**How to apply:** before extending the port, read `g/PORTING.md`
(philosophy, traps, sync checklist) and `g/fglpkg/README.md` (module
map, deviations). Go reference build: `go build -o bin/fglpkg-go
./cmd/fglpkg` (Go 1.26.5 in `~/sdk/go`, add `$HOME/sdk/go/bin` to PATH).
