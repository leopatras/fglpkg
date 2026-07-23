package cli

// TestGeneroVersionSwitch_V5V6BinaryOnlyConsumers is the binary-only
// counterpart to TestGeneroVersionSwitch_V5V6Consumers: samples/v5_42m
// and samples/v6_42m ship ONLY a compiled .42m (no .4gl source at all --
// see their fglpkg.json "files": ["*.42m"]), so unlike the source-only
// samples there is no on-demand recompile fallback. A real, separately
// published .42m per supported Genero major is the only thing standing
// between a consumer and running incompatible bytecode.
//
// Reuses every helper from genero_version_switch_test.go (same
// package): sdkBin, generoEnv, relicenseSDK, fglrunBuildNo,
// requireGeneroSDK, mustRunIn/runIn, buildFglpkgGo, startMockRegistry,
// resolvedEnv, cleanConsumerArtifacts.
import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneroVersionSwitch_V5V6BinaryOnlyConsumers(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is required to run the mock registry: %v", err)
	}

	genero6Dir := defaultGenero6Dir()
	genero5Dir := defaultGenero5Dir()
	requireGeneroSDK(t, genero6Dir)
	requireGeneroSDK(t, genero5Dir)
	relicenseSDK(t, genero6Dir)
	relicenseSDK(t, genero5Dir)

	repoRoot := repoRootFromTest(t)
	fglpkgBin := buildFglpkgGo(t, repoRoot)

	registryURL, cleanupRegistry := startMockRegistry(t, repoRoot)
	defer cleanupRegistry()

	fglpkgHome := t.TempDir()
	baseEnv := append(os.Environ(),
		"FGLPKG_REGISTRY="+registryURL,
		"FGLPKG_TOKEN=gpr_e2e_pat",
		"FGLPKG_HOME="+fglpkgHome,
		"FGLGUI=0",
		"TERM=xterm",
	)

	v5Dir := filepath.Join(repoRoot, "samples", "v5_42m")
	v6Dir := filepath.Join(repoRoot, "samples", "v6_42m")
	v5ConsumerDir := filepath.Join(repoRoot, "samples", "consumers", "v5-42m-consumer")
	v6ConsumerDir := filepath.Join(repoRoot, "samples", "consumers", "v6-42m-consumer")

	defer func() {
		_ = os.Remove(filepath.Join(v5Dir, "v5_42m.42m"))
		_ = os.Remove(filepath.Join(v6Dir, "v6_42m.42m"))
		cleanConsumerArtifacts(v5ConsumerDir)
		cleanConsumerArtifacts(v6ConsumerDir)
	}()

	// ─── Phase 1: under the Genero 6 SDK, compile + publish both .42m- ────
	// only samples
	env6 := generoEnv(baseEnv, genero6Dir)
	fglcomp6 := sdkBin(genero6Dir, "fglcomp")
	fglrun6 := sdkBin(genero6Dir, "fglrun")

	if out := mustRunIn(t, v5Dir, env6, fglcomp6, "-M", "v5_42m.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v5_42m.4gl (genero6) reported an error:\n%s", out)
	}
	if bn := fglrunBuildNo(t, env6, genero6Dir, filepath.Join(v5Dir, "v5_42m.42m")); !strings.HasPrefix(bn, "6.") {
		t.Fatalf("v5_42m.42m compiled under the Genero 6 SDK reports buildNo=%s, want a 6.x build", bn)
	}
	mustRunIn(t, v5Dir, env6, fglpkgBin, "publish", "--ci")

	if out := mustRunIn(t, v6Dir, env6, fglcomp6, "-M", "v6_42m.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v6_42m.4gl (genero6) reported an error:\n%s", out)
	}
	if bn := fglrunBuildNo(t, env6, genero6Dir, filepath.Join(v6Dir, "v6_42m.42m")); !strings.HasPrefix(bn, "6.") {
		t.Fatalf("v6_42m.42m compiled under the Genero 6 SDK reports buildNo=%s, want a 6.x build", bn)
	}
	mustRunIn(t, v6Dir, env6, fglpkgBin, "publish", "--ci")

	// ─── Phase 2: under Genero 6, both consumers install, build, and run, ─
	// with the freshly-installed dependency's .42m verified as genuinely
	// Genero-6 bytecode via fglrun -r
	for _, tc := range []struct {
		dir     string
		depFile string // installed dependency .42m, relative to .fglpkg/packages
		want    string
	}{
		{v5ConsumerDir, filepath.Join("sample-v5-42m", "v5_42m.42m"), "Hello package v5_42m:"},
		{v6ConsumerDir, filepath.Join("sample-v6-42m", "v6_42m.42m"), "Hello package v6_42m:"},
	} {
		mustRunIn(t, tc.dir, env6, fglpkgBin, "install")
		installedDep := filepath.Join(tc.dir, ".fglpkg", "packages", tc.depFile)
		if bn := fglrunBuildNo(t, env6, genero6Dir, installedDep); !strings.HasPrefix(bn, "6.") {
			t.Fatalf("under Genero 6, %s: installed %s reports buildNo=%s, want a 6.x build", tc.dir, tc.depFile, bn)
		}
		buildEnv := resolvedEnv(t, tc.dir, env6, fglpkgBin)
		mustRunIn(t, tc.dir, buildEnv, fglcomp6, "-M", "consumer.4gl")
		runOut := mustRunIn(t, tc.dir, buildEnv, fglrun6, "consumer.42m")
		if !strings.Contains(runOut, tc.want) {
			t.Fatalf("under Genero 6, %s: expected output to contain %q, got:\n%s", tc.dir, tc.want, runOut)
		}
	}

	// ─── Phase 3: switch to the Genero 5 SDK, publish sample-v5-42m's ─────
	// real genero5 variant -- a genuine separate compile, since there is
	// no source to fall back on at install time
	env5 := generoEnv(baseEnv, genero5Dir)
	fglcomp5 := sdkBin(genero5Dir, "fglcomp")
	fglrun5 := sdkBin(genero5Dir, "fglrun")

	if out := mustRunIn(t, v5Dir, env5, fglcomp5, "-M", "v5_42m.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v5_42m.4gl (genero5) reported an error:\n%s", out)
	}
	if bn := fglrunBuildNo(t, env5, genero5Dir, filepath.Join(v5Dir, "v5_42m.42m")); !strings.HasPrefix(bn, "5.") {
		t.Fatalf("v5_42m.42m compiled under the Genero 5 SDK reports buildNo=%s, want a 5.x build", bn)
	}
	mustRunIn(t, v5Dir, env5, fglpkgBin, "publish", "--ci")

	// ─── Phase 4: v5-42m-consumer must update + rebuild + run cleanly, ────
	// with the newly-installed dependency genuinely v5-compiled bytecode
	// -- the definitive check this scenario exists to prove
	_ = os.Remove(filepath.Join(v5ConsumerDir, "consumer.42m"))
	mustRunIn(t, v5ConsumerDir, env5, fglpkgBin, "update")
	installedV5Dep := filepath.Join(v5ConsumerDir, ".fglpkg", "packages", "sample-v5-42m", "v5_42m.42m")
	if bn := fglrunBuildNo(t, env5, genero5Dir, installedV5Dep); !strings.HasPrefix(bn, "5.") {
		t.Fatalf("after `fglpkg update` under Genero 5, installed sample-v5-42m/v5_42m.42m reports buildNo=%s, want a 5.x build", bn)
	}
	buildEnv5 := resolvedEnv(t, v5ConsumerDir, env5, fglpkgBin)
	mustRunIn(t, v5ConsumerDir, buildEnv5, fglcomp5, "-M", "consumer.4gl")
	runOut := mustRunIn(t, v5ConsumerDir, buildEnv5, fglrun5, "consumer.42m")
	if !strings.Contains(runOut, "Hello package v5_42m:") {
		t.Fatalf("under Genero 5, v5-42m-consumer: expected output to contain \"Hello package v5_42m:\", got:\n%s", runOut)
	}

	// ─── Phase 5: v6-42m-consumer's update must fail -- sample-v6-42m has ─
	// no Genero-5-compatible variant
	out, err := runIn(t, v6ConsumerDir, env5, fglpkgBin, "update")
	if err == nil {
		t.Fatalf("expected `fglpkg update` to fail for v6-42m-consumer under Genero 5, but it succeeded:\n%s", out)
	}
	if !strings.Contains(out, "sample-v6-42m") || !strings.Contains(strings.ToLower(out), "compatible") {
		t.Fatalf("expected a Genero-incompatibility error mentioning sample-v6-42m, got:\n%s", out)
	}
}
