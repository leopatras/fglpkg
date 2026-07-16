# fglpkg project instructions for Claude

**Read `.claude/MEMORY.md` first, before anything else in this repository.** It indexes the project's accumulated knowledge base under `.claude/memory/` — 4GL port status, BDL porting traps, E2E test recipes, live-registry publishing workflows, and CLI/resolver internals (variant selection, `bin`/`run` dispatch, known gaps) — and links to the detail files. Check it before investigating something that may already be documented there.

When you learn something during a session that's worth remembering for future work on this repository (a root cause, a non-obvious fix, a workaround, a build quirk), add or update a file under `.claude/memory/` and link it from `.claude/MEMORY.md`, rather than only keeping it in your own home-directory memory (`<HOME>/.claude`) — knowledge kept there isn't shared with the rest of the team or with a fresh checkout.

## Repository layout

- Go implementation (the reference): `cmd/fglpkg/` + `internal/`.
- Genero 4GL reimplementation: `g/fglpkg/` (all `PACKAGE fglpkg`, `FGLLDPATH=<repo>/g`). Build/tests: `make` / `make test` in `g/fglpkg/`.
- Porting notes (parity philosophy, style rules, BDL trap catalogue, sync checklist): `g/PORTING.md`. Module map and deviations: `g/fglpkg/README.md`.

## Rules

- The 4GL port tracks the Go binary byte-for-byte: any change to user-visible Go output (strings, tables, JSON, exit codes) must be mirrored in the owning 4GL module, verified by diffing against a fresh `go build -o bin/fglpkg-go ./cmd/fglpkg` (see `g/PORTING.md` §Keeping the port in sync).
- Before writing Genero BDL code, consult the genero-intelligence MCP skills — do not rely on training data for Genero APIs.
