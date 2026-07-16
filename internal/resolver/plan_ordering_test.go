package resolver_test

import (
	"fmt"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// chainName is the deterministic name of the i-th package in a linear
// dependency chain, zero-padded so lexical and numeric order coincide.
func chainName(i int) string { return fmt.Sprintf("a%05d", i) }

// linearChainDB builds a registry of n packages where a{i} depends on a{i+1},
// plus a root manifest depending on a{0}. The resolution order is therefore
// forced by the graph shape (a0, a1, …, a{n-1}) independent of any map
// iteration — a precise fixture for asserting install-plan ordering.
func linearChainDB(n int) (packageDB, *manifest.Manifest) {
	db := packageDB{}
	for i := 0; i < n; i++ {
		name := chainName(i)
		var deps map[string]string
		if i+1 < n {
			deps = map[string]string{chainName(i + 1): "^1.0.0"}
		}
		db[name] = map[string]dbEntry{"1.0.0": entry("", pkg(name, "1.0.0", deps))}
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency(chainName(0), "^1.0.0")
	return db, root
}

// TestBuildPlanPreservesDiscoveryOrder is the GIS-258 regression + determinism
// guard. buildPlan collects resolved packages by ranging s.resolved (Go
// randomizes map iteration) and then sorts them back into discovery order.
// Over a linear chain the discovery order is fixed (a00000, a00001, …), so the
// install plan must come out in exactly that order on every run. Looping
// defeats the map randomization that would otherwise let an ordering bug pass
// intermittently.
func TestBuildPlanPreservesDiscoveryOrder(t *testing.T) {
	const n = 30
	db, root := linearChainDB(n)

	want := make([]string, n)
	for i := range want {
		want[i] = chainName(i)
	}

	for run := 0; run < 20; run++ {
		plan, err := db.newResolver(genero401).Resolve(root)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", run, err)
		}
		if len(plan.Packages) != n {
			t.Fatalf("run %d: got %d packages, want %d", run, len(plan.Packages), n)
		}
		for i, p := range plan.Packages {
			if p.Name != want[i] {
				t.Fatalf("run %d: plan[%d] = %q, want %q — plan not in discovery order",
					run, i, p.Name, want[i])
			}
		}
	}
}

// BenchmarkBuildPlanLargeGraph guards against a regression to quadratic plan
// ordering: the former hand-written insertion sort over map-randomized input
// was O(N²) and dominated resolution above ~10k packages. Mirrors the
// synthetic traversal-only graph that surfaced the issue. Run with:
//
//	go test ./internal/resolver/ -bench BuildPlanLargeGraph -run '^$'
func BenchmarkBuildPlanLargeGraph(b *testing.B) {
	const n = 20000
	db, root := linearChainDB(n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.newResolver(genero401).Resolve(root); err != nil {
			b.Fatalf("resolve failed: %v", err)
		}
	}
}
