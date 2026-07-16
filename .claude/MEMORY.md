# fglpkg project memory

Project-local knowledge base for fglpkg (Go package manager for Genero BDL + its 4GL reimplementation). Detail files live in `.claude/memory/`; the human-facing porting documentation is `g/PORTING.md`.

## 4GL port (g/fglpkg)

- [Port status](memory/port-4gl-status.md) — full parity reached 2026-07-11; branch `4gl-port` state, verified parity list, Leo's confirmed layout/style decisions
- [BDL porting traps](memory/bdl-porting-traps.md) — compressed checklist of the Genero language/runtime traps hit during the port (full catalogue: `g/PORTING.md`)
- [E2E testing how-to](memory/e2e-testing-howto.md) — headless fglrun invocation, byte-diff vs `bin/fglpkg-go`, mock_registry.py (registry + OAuth + mock OSV `/v1/query`), env vars
- [Upstream Go quirks](memory/upstream-go-quirks.md) — bugs replicated on purpose for byte-parity (pluralY "vulnerabilitie", run vs bdl exit codes); fix both sides together
- Resolver benchmarks: see `g/BENCHMARKS.md` — Go's buildPlan is accidentally O(N²) (insertion sort over randomized map order), 4GL is linear (insertion-ordered getKeys); fair constant factor ≈ 20× in Go's favor; irrelevant below ~1000 packages

## Registry, publishing, and CLI (applies to either implementation)

- [Registry publishing and variant resolution](memory/registry-publishing-and-variants.md) — live test registry usage, `unpublish` documented-but-not-implemented, `pickArtifact`'s exact-match/`default`/`arts[0]`-fallback algorithm (no version-aware fallback exists), the `bin`/`fglpkg run` Windows one-path-per-command limitation, and the `fglwebrun` example package (source-only self-compiling design)
