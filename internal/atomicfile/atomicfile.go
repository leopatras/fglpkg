// Package atomicfile writes a file via a sibling temp file + rename, so a
// process interrupted mid-write can never leave a truncated or partially
// written destination. Used for the small tool-managed JSON files under the
// fglpkg home (credentials, global config, update-check cache).
package atomicfile

import (
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically. It writes to a temp file in the
// same directory (keeping the final rename on one filesystem) and renames it
// over path. On success path holds either the previous content or the complete
// new content — never a partial write. os.Rename replaces an existing file on
// both Unix and Windows. The caller must ensure the parent directory exists.
//
// perm is applied best-effort: the temp file is created 0600 by os.CreateTemp,
// then widened to perm via Chmod. A Chmod failure (e.g. on Windows, where only
// the read-only bit is meaningful) is ignored, and the result is never wider
// than perm requests.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we return before the rename succeeds. After a
	// successful rename tmpName no longer exists and this Remove is a harmless
	// no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Chmod(perm)
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
