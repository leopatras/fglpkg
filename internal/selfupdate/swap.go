package selfupdate

import (
	"os"
	"runtime"
)

// atomicReplace replaces the file at target with the file at staged (which must
// be on the same filesystem — the caller stages it in target's directory).
//
//   - Unix: a single os.Rename is atomic, and replacing a running binary's inode
//     is safe — the running process keeps its open file.
//   - Windows: a running .exe cannot be overwritten, so rename it out of the way
//     to <target>.old first, move the new file into place, and leave the .old for
//     best-effort cleanup on a later run. On failure the original is restored.
func atomicReplace(target, staged string) error {
	if runtime.GOOS == "windows" {
		old := target + ".old"
		_ = os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return err
		}
		if err := os.Rename(staged, target); err != nil {
			_ = os.Rename(old, target) // roll back
			return err
		}
		return nil
	}
	return os.Rename(staged, target)
}

// cleanupStaleWindowsBackup best-effort removes a leftover <exe>.old from a prior
// Windows self-update. A no-op elsewhere. Harmless if it fails.
func cleanupStaleWindowsBackup(exePath string) {
	if runtime.GOOS == "windows" {
		_ = os.Remove(exePath + ".old")
	}
}

// applyMode makes staged executable, preserving target's current permission bits
// on Unix (defaulting to 0755) and ensuring the execute bits are set. A no-op on
// Windows, where executability is by extension.
func applyMode(staged, target string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm() | 0o111
	}
	return os.Chmod(staged, mode)
}
