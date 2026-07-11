package resolver_test

// Benchmark twin of g/fglpkg/test/benchresolver.4gl — identical synthetic
// graph generated on the fly in the fetchers, used for the runtime
// comparison documented in g/BENCHMARKS.md. Skipped unless BENCH_N is set:
//   BENCH_N=5000 BENCH_K=5 go test ./internal/resolver -run TestBenchHugeGraph -v

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func TestBenchHugeGraph(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("BENCH_N"))
	k, _ := strconv.Atoi(os.Getenv("BENCH_K"))
	if n == 0 {
		t.Skip("BENCH_N not set")
	}
	if k == 0 {
		k = 5
	}

	versions := func(name string) ([]resolver.CandidateVersion, error) {
		return []resolver.CandidateVersion{
			{Version: semver.MustParse("1.0.0")},
			{Version: semver.MustParse("1.1.0")},
			{Version: semver.MustParse("1.2.0")},
		}, nil
	}
	info := func(name, version, _ string) (*registry.PackageInfo, error) {
		idx, _ := strconv.Atoi(name[3:])
		deps := map[string]string{}
		for i := idx + 1; i <= idx+k && i <= n; i++ {
			deps[fmt.Sprintf("pkg%06d", i)] = "^1.0.0"
		}
		return &registry.PackageInfo{
			Name:        name,
			Version:     version,
			DownloadURL: fmt.Sprintf("https://example.com/%s-%s.zip", name, version),
			Checksum:    "deadbeef",
			Variant:     "genero4",
			FGLDeps:     deps,
		}, nil
	}

	r := resolver.NewWithFetchers(genero.MustParse("4.01.12"), versions, info)
	root := &manifest.Manifest{
		Name:    "bench",
		Version: "1.0.0",
		Dependencies: manifest.Dependencies{
			FGL: map[string]string{"pkg000001": "^1.0.0"},
		},
	}

	start := time.Now()
	plan, err := r.Resolve(root)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	fmt.Printf("go  N=%d K=%d resolved=%d elapsed=%s\n",
		n, k, len(plan.Packages), elapsed)
}
