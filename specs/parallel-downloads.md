# Spec: Parallel package + JAR downloads (v1)

**Status:** ✅ Implemented — shipped ([internal/installer/parallel.go](../internal/installer/parallel.go))
**Date:** 2026-05-18
**Author:** Mike Folcher
**Tracking:** P3 #29 in [docs/market-readiness-gaps.md](../docs/market-readiness-gaps.md)

---

## Summary

Make `fglpkg install` (and `update`) download BDL packages and Java JARs **in parallel** instead of one at a time. Bounded concurrency (default 4) keeps the implementation simple, predictable, and easy on the registry / GitHub. No CLI surface change.

## Motivation

Today's installer is strictly sequential. For a typical project with one BDL package and ~10 JARs, latency is dominated by serialized HTTP round-trips. Measured against [poiapi](https://github.com/4js-mikefolcher/poiapi) (11 JARs):

- Sequential: ~150ms × 11 ≈ 1.6 s of pure download time.
- Parallel (4 workers): ≈ 0.4 s.

The gain is bigger for projects with more JARs or higher network latency (CI runners on cloud regions far from Maven Central). And the change pairs naturally with the new `fglpkg audit` and `fglpkg sbom` flows that come **after** install — making the whole pipeline visibly snappier.

## Goals

- BDL packages install concurrently, up to a configurable cap.
- JARs install concurrently, up to a configurable cap.
- Default concurrency 4. Override with `FGLPKG_INSTALL_CONCURRENCY=<n>`.
- No new CLI flags. No new go.mod dependencies.
- Optional-scope failure semantics preserved: warnings stay warnings; hard-scope failures still abort the install.
- Progress output remains readable — concurrent goroutines do not interleave mid-line.

## Non-goals (v1)

- Cancelling in-flight downloads on a sibling failure. Workers run to completion; failure is reported once everything settles. (Future: pass a `context.Context` through `downloadAndVerify` and cancel on first error.)
- Reordering BDL packages vs JARs. Today's installer does packages then JARs in two phases; v1 keeps that ordering, just makes each phase parallel.
- Adaptive concurrency. Static cap, no auto-tune.
- Per-file progress bars. The output is still line-by-line "✓ name" markers; a Progress-bar UI is a separate P3 ticket.

## Behavior

### Concurrency cap

A small pool of workers handles the install function calls. The cap is read once at the top of each install phase:

```go
cap := installConcurrency()  // env override, else 4
```

`installConcurrency()`:

- If `FGLPKG_INSTALL_CONCURRENCY` is set and parses as an int ≥ 1, use it.
- If it is set but ≤ 0 or non-numeric, fall back to 4 (don't error — env-tuning shouldn't break installs).
- Otherwise return the default of 4.

A cap of 1 effectively restores sequential behavior. Useful for debugging.

### Failure semantics

Inside each worker, `Install` / `InstallJar` runs unchanged:

- On error, if the item is **optional-scope**, the worker prints `warning: skipping optional ...` and returns nil (same as today).
- On error, if the item is **hard-scope**, the worker returns the error.

The pool collects the first non-nil error and returns it once all workers have finished. Other workers run to completion — we don't abort half-written downloads. This matches today's "partial install survives" behavior with the new sequential-then-fail flow.

### Output

Each worker prints **a single line on completion**:

```
  ✓ poiapi@1.0.0
  ✓ com.example:commons-text 1.10.0
  ...
```

The leading `→ name` "starting" lines from the current installer are dropped — with concurrent starts they aren't aligned with the completion order anyway, and removing them produces a cleaner stream that reads like yarn/pnpm.

A `sync.Mutex` around `fmt.Printf` keeps every line atomic. No tearing, no garbled output. Required-by hints (`required by: A, B, ...`) currently emitted in `installFromPlan` are preserved on the completion line.

### Existing dependency-graph guarantees

Packages and JARs are installed into **disjoint directories**:

- `~/.fglpkg/packages/<name>/` per package
- `~/.fglpkg/jars/<groupId>-<artifactId>-<version>.jar` per JAR

No two workers ever touch the same file. The `ensureDirs()` call (top-level `mkdir -p`) is idempotent and safe under concurrent invocation. `http.DefaultClient` is concurrency-safe by design.

The only shared state is stdout, which the print mutex protects.

## API additions

Internal only — no public CLI or Go API surface changes.

```go
// runParallel calls fn(item) for each item, bounded by `cap` workers.
// Returns the first non-nil error fn returned; all goroutines complete
// regardless of where the error originated. cap <= 0 is treated as 1.
func runParallel[T any](items []T, cap int, fn func(T) error) error
```

Lives next to the installer (`internal/installer/parallel.go`) — kept private so it can evolve without callers worrying.

## Algorithm

For each of the two phases (`installFromLock` and `installFromPlan`):

```
1. Build a slice of work items (skip already-installed in the lock path).
2. Pass the slice + concurrency cap to runParallel.
3. The worker function calls Install or InstallJar.
4. On error, the worker either:
     - prints a warning and returns nil  (optional scope)
     - returns the error                 (hard scope)
5. Once all workers complete, return the first error (or nil).
```

JARs are processed in their own `runParallel` call after the BDL package phase finishes — the two phases stay separate so JAR installs don't compete with package extraction for I/O.

## Testing

Existing `installer_test.go` covers `makeBinScriptsExecutable` only — no network-touching tests. Adding tests for the pool primitive is straightforward:

- `internal/installer/parallel_test.go`
  - `TestRunParallelEmpty` — empty input → nil, no work.
  - `TestRunParallelSingle` — one item → fn called once, returned err propagated.
  - `TestRunParallelHonoursCap` — N=20 items, cap=4 → max-in-flight observed never exceeds 4 (use atomic counter + sync.Mutex around max).
  - `TestRunParallelAllRun` — every item's fn is called even when one fails.
  - `TestRunParallelFirstErrorReturned` — multiple failures → exactly one error returned; underlying message is one of them.

`installFromLock` / `installFromPlan` paths are not unit-tested today and v1 doesn't add coverage there — too much network/zip/filesystem scaffolding for the value gained. Manual smoke test against `poiapi` is the merge gate.

## Acceptance criteria

1. `fglpkg install` against a project with N>1 JARs downloads them concurrently. ✅
2. Concurrency cap respected — workers in flight ≤ `FGLPKG_INSTALL_CONCURRENCY` (default 4). ✅
3. Optional-scope failures still print a warning and do not abort the install. ✅
4. Hard-scope failures still abort the install (after siblings settle). ✅
5. Stdout lines are atomic — no torn output under contention. ✅
6. Smoke test against `poiapi` is meaningfully faster than the v2.0.1 sequential baseline. ✅ (qualitative — note in the merge PR)
7. `go build ./...` clean; `go test ./...` passes; new pool tests included.
8. No new go.mod dependencies.

## Open questions

- **Should `update` use a different cap than `install`?** No good reason to differentiate today. One env var, one default, both commands honour it.
- **What about the `fglpkg audit` HTTP fan-out?** Audit already does one-per-JAR sequential queries against OSV.dev. Parallelizing it is a separate (smaller) follow-up; not in scope here.

## Future work (explicitly deferred)

- `context.Context` plumbing through `downloadAndVerify` for proper in-flight cancellation on sibling failure.
- Per-file progress bars (P3 #30 — separate ticket).
- Adaptive concurrency / backoff based on observed errors.
- Reusable pool primitive shared with `audit` for parallel OSV.dev queries.
