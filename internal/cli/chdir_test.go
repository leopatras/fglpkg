package cli

import (
	"os"
	"testing"
)

// chdirTest changes the working directory to dir for the duration of the
// test and restores the original directory on cleanup. It exists because
// t.Chdir was only added in Go 1.24 and this module targets Go 1.20.
func chdirTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}
