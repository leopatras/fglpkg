package cli

// TestGeneroVersionSwitch_V5V6Consumers exercises a real Genero-major
// switch end to end: samples/v5 and samples/v6 (which require Genero
// >=5.00.03 and >=6.00 respectively) are published fresh to a throwaway
// mock registry, consumed by samples/consumers/v5-consumer and
// v6-consumer under a real Genero 6 SDK, then the active Genero
// environment is switched to a real Genero 5 SDK and `fglpkg update` is
// re-run in place: the v5 consumer must re-resolve to sample-v5's
// genero5 variant and still build+run, while the v6 consumer -- whose
// sample-v6 dependency has no genero5 variant at all -- must fail
// `fglpkg update` with a clear Genero-incompatibility error.
//
// This is a real integration test, not a unit test: it shells out to
// two actual local Genero SDK installations (fglcomp/fglrun), spins up
// g/fglpkg/test/mock_registry.py as a subprocess, and builds/runs the
// repo's own bin/fglpkg-go. The two SDK directories are HARD
// requirements -- the test fails outright (no t.Skip) if they are
// missing, so it only runs meaningfully on a machine that has them
// (override via FGLPKG_TEST_GENERO6_DIR / FGLPKG_TEST_GENERO5_DIR).
//
// It deliberately uses the Go fglpkg built fresh from this checkout
// (bin/fglpkg-go), not whatever "fglpkg" happens to be on PATH, so it
// always exercises the exact Go implementation in this repo.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func defaultGenero6Dir() string {
	if v := os.Getenv("FGLPKG_TEST_GENERO6_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "Downloads", "fgl")
}

func defaultGenero5Dir() string {
	if v := os.Getenv("FGLPKG_TEST_GENERO5_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "Downloads", "fgl5.01.04dev")
}

// requireGeneroSDK hard-fails (no skip) when dir doesn't look like a
// real Genero installation -- this test is meant to prove real
// cross-version behavior, so silently skipping would hide that.
func requireGeneroSDK(t *testing.T, dir string) {
	t.Helper()
	fglcomp := filepath.Join(dir, "bin", "fglcomp")
	if _, err := os.Stat(fglcomp); err != nil {
		t.Fatalf("required Genero SDK not found at %s (bin/fglcomp missing): %v\n"+
			"override with FGLPKG_TEST_GENERO6_DIR / FGLPKG_TEST_GENERO5_DIR if this machine's SDKs live elsewhere", dir, err)
	}
}

// generoEnv returns an environment (based on base) with sdkDir's own
// bin/ first on PATH and FGLDIR pointed at sdkDir, so fglcomp/fglrun
// resolve to that exact SDK regardless of what's already on PATH.
func generoEnv(base []string, sdkDir string) []string {
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, "FGLDIR=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "FGLDIR="+sdkDir)
	env = append(env, "PATH="+filepath.Join(sdkDir, "bin")+":"+os.Getenv("PATH"))
	return env
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// this file lives in internal/cli
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// runIn runs name(args...) in dir with env, failing the test with full
// output on error unless wantErr is true (in which case the caller
// checks the returned output/err itself).
func runIn(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRunIn(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, err := runIn(t, dir, env, name, args...)
	if err != nil {
		t.Fatalf("%s %s (in %s) failed: %v\n--- output ---\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return out
}

// buildFglpkgGo builds the repo's own Go fglpkg binary fresh (matching
// the samples Makefiles' own $(FGLPKG_GO) on-demand build rule) and
// returns its absolute path.
func buildFglpkgGo(t *testing.T, repoRoot string) string {
	t.Helper()
	bin := filepath.Join(repoRoot, "bin", "fglpkg-go")
	env := append(os.Environ(), "PATH="+filepath.Join(os.Getenv("HOME"), "sdk", "go", "bin")+":"+os.Getenv("PATH"))
	mustRunIn(t, repoRoot, env, "go", "build", "-o", bin, "./cmd/fglpkg")
	return bin
}

// freePort asks the OS for an unused TCP port by binding to :0 and
// immediately releasing it. Small TOCTOU race in exchange for not
// hardcoding a port that might collide with a manually-running demo
// (samples/Makefile uses 18930).
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not allocate a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startMockRegistry launches g/fglpkg/test/mock_registry.py on a fresh
// port with its own throwaway state dir, waits for it to accept
// connections, and returns its base URL plus a cleanup func.
func startMockRegistry(t *testing.T, repoRoot string) (string, func()) {
	t.Helper()
	port := freePort(t)
	stateDir := t.TempDir()
	script := filepath.Join(repoRoot, "g", "fglpkg", "test", "mock_registry.py")

	cmd := exec.Command("python3", script, fmt.Sprintf("%d", port), stateDir)
	logFile, err := os.Create(filepath.Join(stateDir, "mock_registry.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start mock registry: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		logFile.Close()
	}
	return url, cleanup
}

// cleanConsumerArtifacts removes the gitignored install/build state a
// consumer sample project accumulates, restoring it to its checked-in
// state regardless of whether the test passed.
func cleanConsumerArtifacts(dir string) {
	_ = os.RemoveAll(filepath.Join(dir, ".fglpkg"))
	_ = os.Remove(filepath.Join(dir, "fglpkg.lock"))
	_ = os.Remove(filepath.Join(dir, "consumer.42m"))
}

func TestGeneroVersionSwitch_V5V6Consumers(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is required to run the mock registry: %v", err)
	}

	genero6Dir := defaultGenero6Dir()
	genero5Dir := defaultGenero5Dir()
	requireGeneroSDK(t, genero6Dir)
	requireGeneroSDK(t, genero5Dir)

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

	v5Dir := filepath.Join(repoRoot, "samples", "v5")
	v6Dir := filepath.Join(repoRoot, "samples", "v6")
	v5ConsumerDir := filepath.Join(repoRoot, "samples", "consumers", "v5-consumer")
	v6ConsumerDir := filepath.Join(repoRoot, "samples", "consumers", "v6-consumer")

	defer func() {
		_ = os.Remove(filepath.Join(v5Dir, "v5.42m"))
		_ = os.Remove(filepath.Join(v6Dir, "v6.42m"))
		cleanConsumerArtifacts(v5ConsumerDir)
		cleanConsumerArtifacts(v6ConsumerDir)
	}()

	// ─── Phase 1: under the Genero 6 SDK, compile + publish both samples ──
	env6 := generoEnv(baseEnv, genero6Dir)

	if out := mustRunIn(t, v5Dir, env6, "fglcomp", "-M", "v5.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v5.4gl (genero6) reported an error:\n%s", out)
	}
	mustRunIn(t, v5Dir, env6, fglpkgBin, "publish", "--ci")

	if out := mustRunIn(t, v6Dir, env6, "fglcomp", "-M", "v6.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v6.4gl (genero6) reported an error:\n%s", out)
	}
	mustRunIn(t, v6Dir, env6, fglpkgBin, "publish", "--ci")

	// ─── Phase 2: under Genero 6, both consumers install, build, and run ──
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{v5ConsumerDir, "Hello package v5:"},
		{v6ConsumerDir, "Hello package v6:"},
	} {
		mustRunIn(t, tc.dir, env6, fglpkgBin, "install")
		buildEnv := resolvedEnv(t, tc.dir, env6, fglpkgBin)
		mustRunIn(t, tc.dir, buildEnv, "fglcomp", "-M", "consumer.4gl")
		runOut := mustRunIn(t, tc.dir, buildEnv, "fglrun", "consumer.42m")
		if !strings.Contains(runOut, tc.want) {
			t.Fatalf("under Genero 6, %s: expected output to contain %q, got:\n%s", tc.dir, tc.want, runOut)
		}
	}

	// ─── Phase 3: switch to the Genero 5 SDK, publish sample-v5's real ────
	// genero5 variant (recompiled with the actual v5 toolchain, not just
	// a relabeled genero6 artifact)
	env5 := generoEnv(baseEnv, genero5Dir)

	if out := mustRunIn(t, v5Dir, env5, "fglcomp", "-M", "v5.4gl"); strings.Contains(out, "ERROR") {
		t.Fatalf("fglcomp -M v5.4gl (genero5) reported an error:\n%s", out)
	}
	mustRunIn(t, v5Dir, env5, fglpkgBin, "publish", "--ci")

	// ─── Phase 4: v5-consumer must update + rebuild + run cleanly ─────────
	_ = os.Remove(filepath.Join(v5ConsumerDir, "consumer.42m"))
	mustRunIn(t, v5ConsumerDir, env5, fglpkgBin, "update")
	buildEnv5 := resolvedEnv(t, v5ConsumerDir, env5, fglpkgBin)
	mustRunIn(t, v5ConsumerDir, buildEnv5, "fglcomp", "-M", "consumer.4gl")
	runOut := mustRunIn(t, v5ConsumerDir, buildEnv5, "fglrun", "consumer.42m")
	if !strings.Contains(runOut, "Hello package v5:") {
		t.Fatalf("under Genero 5, v5-consumer: expected output to contain \"Hello package v5:\", got:\n%s", runOut)
	}

	// ─── Phase 5: v6-consumer's update must fail -- sample-v6 has no ──────
	// Genero-5-compatible variant
	out, err := runIn(t, v6ConsumerDir, env5, fglpkgBin, "update")
	if err == nil {
		t.Fatalf("expected `fglpkg update` to fail for v6-consumer under Genero 5, but it succeeded:\n%s", out)
	}
	if !strings.Contains(out, "sample-v6") || !strings.Contains(strings.ToLower(out), "compatible") {
		t.Fatalf("expected a Genero-incompatibility error mentioning sample-v6, got:\n%s", out)
	}
}

// resolvedEnv runs `eval "$(fglpkgBin env)"` in dir through bash --
// exactly the mechanism the real launchers use -- and returns the
// resulting environment as an exec.Cmd.Env-compatible slice. Letting
// bash itself evaluate the export lines (rather than hand-parsing them)
// correctly resolves the `"${FGLLDPATH:+:$FGLLDPATH}"` shell-parameter-
// expansion suffix env.go emits on every export line.
func resolvedEnv(t *testing.T, dir string, env []string, fglpkgBin string) []string {
	t.Helper()
	script := fmt.Sprintf(`eval "$(%s env)" && env -0`, fglpkgBin)
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolving `fglpkg env` in %s failed: %v", dir, err)
	}
	var result []string
	for _, kv := range strings.Split(string(out), "\x00") {
		if kv != "" {
			result = append(result, kv)
		}
	}
	return result
}
