# Spec: Fix O(N²) install-plan ordering in the resolver

**Status:** 📋 Not started — spec ready (no ticket yet)
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** `resolver.buildPlan` orders the final install plan with a hand-written insertion
sort over packages collected in **randomized map-iteration order**. Insertion sort is O(N²) on
randomly-ordered input, so resolution time grows quadratically with the number of resolved
packages. On small graphs this is invisible; on large dependency graphs it dominates wall-clock
and — above ~10k packages — makes the native Go resolver *slower* than an interpreted 4GL port.
**Credit:** surfaced by Leo Schubert's dependency-graph traversal benchmark while evaluating a
Genero re-implementation (`g/BENCHMARKS.md` in [leopatras/fglpkg](https://github.com/leopatras/fglpkg)).
The fix belongs in the shipping Go implementation regardless of that evaluation's outcome.
**Related:** [genero-aware-search.md](genero-aware-search.md), [import-root.md](import-root.md) (house style).

---

## Summary

Replace the insertion sort in [`buildPlan`](../internal/resolver/resolver.go#L545) with a standard
`sort.Slice` keyed on each entry's resolution `order`. This changes the plan-ordering step from
O(N²) to O(N log N) while producing **byte-identical output** — the same deterministic
discovery-order plan — for every input. It is a pure internal performance fix: no CLI, manifest,
lockfile, or registry surface changes.

## Background — how it works today

### Each resolved package records its discovery order

As the BFS walk resolves a package, [`markResolved`](../internal/resolver/resolver.go#L521) stamps
it with a monotonically increasing sequence number and stores it in the `s.resolved` map:

```go
func (s *state) markResolved(name string, v semver.Version, info *registry.PackageInfo, scope manifest.Scope) {
    s.resolved[name] = &resolvedEntry{version: v, info: info, order: s.orderSeq, scope: scope}
    s.orderSeq++
}
```

`order` is unique per entry (it only ever increments, and only resolved packages get one). The
install plan is meant to preserve this order so packages install in the sequence they were
discovered.

### `buildPlan` collects from a map, then insertion-sorts

[`buildPlan`](../internal/resolver/resolver.go#L545) first appends every resolved entry into a
slice by ranging over the map — which Go deliberately iterates in **randomized order** — and then
restores `order` with a nested-loop insertion sort:

```go
pkgs := make([]ResolvedPackage, 0, len(s.resolved))
for name, entry := range s.resolved {      // randomized iteration order
    ...
    pkgs = append(pkgs, ResolvedPackage{ ... })
}
for i := 1; i < len(pkgs); i++ {            // insertion sort — O(N²) on random input
    for j := i; j > 0 && s.resolved[pkgs[j].Name].order < s.resolved[pkgs[j-1].Name].order; j-- {
        pkgs[j], pkgs[j-1] = pkgs[j-1], pkgs[j]
    }
}
```

Insertion sort is O(N) only when the input is already nearly sorted. Because the input is
map-randomized, the average and worst case are both **O(N²)** — every element can shift past most
of the ones before it.

### Measured impact

Leo's benchmark isolates exactly this traversal-plus-ordering path (synthetic graph generated in the
fetchers; no network, no disk — only the resolution work is timed):

| Packages | Fan-out | Go (current, insertion sort) | Go (with this fix) |
|---:|---:|---:|---:|
| 1,000 | 5 | 8.6 ms | 3.9 ms |
| 5,000 | 5 | 171 ms | 19 ms |
| 20,000 | 5 | 2.7 s | 79 ms |
| 50,000 | 5 | 18.5 s | 211 ms |

The super-linear blow-up (5k→50k is 10× the packages but ~108× the time) is the O(N²) signature.
With the fix, the same 50k graph resolves in ~0.2 s — an ~88× improvement — and scaling is
log-linear.

## Design

### The fix

Sort `pkgs` with `sort.Slice`, comparing on the recorded `order`:

```go
sort.Slice(pkgs, func(i, j int) bool {
    return s.resolved[pkgs[i].Name].order < s.resolved[pkgs[j].Name].order
})
```

Delete the nested insertion-sort loop and add `"sort"` to the file's imports.

**Determinism is preserved (and does not depend on sort stability).** Every entry's `order` is
unique, so the comparator defines a total order; the resulting slice is identical regardless of the
map's randomized starting order or the sort algorithm's internal pivoting. `sort.Slice` (not
`sort.SliceStable`) is therefore sufficient and correct. The output plan is the same discovery-order
sequence the insertion sort produced — this is a drop-in replacement, verifiable by a byte-for-byte
diff of the plan on any fixed input.

### Optional micro-optimization (not required)

The comparator does two map lookups per comparison (O(N log N) lookups total). If profiling ever
flags it, capture `order` into a parallel `[]int` (or add an unexported `order` field to the local
build slice) so the comparator reads a slice index instead of hashing a name. This is not needed to
hit O(N log N) and is called out only so a reviewer doesn't mistake the map lookups for the hot spot.

## Secondary finding — nondeterministic ordering of JARs and local members (out of scope)

The same [`buildPlan`](../internal/resolver/resolver.go#L568) builds `jars` and `locals` by ranging
over `s.jars` / `s.localMembers` with **no** subsequent sort, so those slices come out in randomized
map order on every run. This is a *determinism* issue (e.g. it can churn lockfile / plan-print
ordering run-to-run), not a performance one, and it is **out of scope** for this spec. Noting it here
so it isn't lost; a follow-up can sort `jars` by `dep.Key()` and `locals` by `Name`. This spec
touches only the BDL-package ordering path.

## Non-goals

- **No algorithm change to resolution itself.** The BFS walk, constraint intersection, version
  selection, and conflict detection are untouched. Only the final plan-ordering step changes.
- **No change to the resolved plan's contents or order.** Same packages, same versions, same
  sequence — only the cost of producing that sequence changes.
- **No CLI / manifest / lockfile / registry changes.**
- **JAR and local-member ordering** (see secondary finding) is deferred.

## Testing

- **Ordering unchanged (regression):** extend the resolver tests
  ([resolver_test.go](../internal/resolver/resolver_test.go)) with a multi-package graph and assert
  the resulting `Plan.Packages` names appear in resolution order — the assertion must hold across
  repeated runs (map randomization would expose any accidental dependence on iteration order).
- **Determinism:** resolve the same fixture N times and assert identical `Plan.Packages` ordering
  each time.
- **Scale/perf guard (optional):** a benchmark (`BenchmarkBuildPlanLargeGraph`) over a synthetic
  graph of e.g. 20k entries, so a future regression back to a quadratic sort is visible in
  `go test -bench`. Mirror Leo's synthetic-graph setup for comparability.
- Full `go test ./...` to confirm no downstream consumer relied on the (identical) prior ordering.

## Rollout

Pure internal fix; ships in the next patch release. No migration, no flag, no documentation change
beyond a CHANGELOG entry noting the large-graph resolution speedup and crediting the benchmark.
