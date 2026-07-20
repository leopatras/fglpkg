package signing

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSigningConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, configFile), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceModeDefault(t *testing.T) {
	t.Setenv("FGLPKG_SIGNING", "")
	if got := EnforceMode(t.TempDir()); got != DefaultEnforce {
		t.Errorf("got %q, want default %q", got, DefaultEnforce)
	}
}

func TestEnforceModeEnvOverridesConfig(t *testing.T) {
	t.Setenv("FGLPKG_SIGNING", "require")
	home := t.TempDir()
	writeSigningConfig(t, home, `{"signing":{"enforce":"warn"}}`)
	if got := EnforceMode(home); got != EnforceRequire {
		t.Errorf("env should win: got %q, want %q", got, EnforceRequire)
	}
}

func TestEnforceModeFromConfig(t *testing.T) {
	t.Setenv("FGLPKG_SIGNING", "")
	home := t.TempDir()
	writeSigningConfig(t, home, `{"signing":{"enforce":"off"}}`)
	if got := EnforceMode(home); got != EnforceOff {
		t.Errorf("got %q, want %q", got, EnforceOff)
	}
}

func TestEnforceModeInvalidFallsThrough(t *testing.T) {
	t.Setenv("FGLPKG_SIGNING", "bogus") // invalid env, no config => default
	if got := EnforceMode(t.TempDir()); got != DefaultEnforce {
		t.Errorf("got %q, want default %q", got, DefaultEnforce)
	}
}
