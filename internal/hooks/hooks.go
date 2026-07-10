// Package hooks executes the declarative lifecycle steps declared in a
// manifest's "hooks" field. The vocabulary is intentionally small —
// arbitrary shell commands are not supported, since shell-based hooks are
// the dominant supply-chain attack vector in mainstream package managers.
package hooks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// Run executes every operation declared for event under the given hooks
// map, with cwd as the working directory. All file paths are resolved
// relative to cwd; absolute paths and ".." traversal are rejected so a
// hook cannot reach outside the package being installed (or the project
// being published).
//
// A failure on any operation aborts the run and returns the error; later
// operations for the same event are skipped.
func Run(hooks manifest.Hooks, event manifest.HookEvent, cwd string) error {
	ops := hooks[event]
	if len(ops) == 0 {
		return nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("hook %s: cannot resolve cwd: %w", event, err)
	}
	for i, op := range ops {
		if err := runOp(op, abs); err != nil {
			return fmt.Errorf("hook %s op[%d] %s: %w", event, i, op.Op, err)
		}
	}
	return nil
}

func runOp(op manifest.HookOperation, root string) error {
	switch op.Op {
	case manifest.HookOpCopyFiles:
		return runCopyFiles(op.From, op.To, root)
	case manifest.HookOpMkdir:
		return runMkdir(op.Path, root)
	default:
		return fmt.Errorf("unknown op %q", op.Op)
	}
}

// runMkdir creates the target directory (and any missing parents). If the
// path already exists and is a directory, this is a no-op; if it exists
// and is a file, the operation fails.
func runMkdir(path, root string) error {
	target, err := resolveInside(root, path)
	if err != nil {
		return err
	}
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("mkdir target %q exists and is not a directory", path)
		}
		return nil
	}
	return os.MkdirAll(target, 0o755)
}

// runCopyFiles copies one file, every file under a directory, or every
// file matching a glob from the source into the destination. The dest
// directory is created if missing.
func runCopyFiles(from, to, root string) error {
	srcAbs, err := resolveInside(root, from)
	if err != nil {
		return err
	}
	dstAbs, err := resolveInside(root, to)
	if err != nil {
		return err
	}

	// Direct path (no glob characters): copy file or recurse directory.
	if !strings.ContainsAny(from, "*?[") {
		info, err := os.Stat(srcAbs)
		if err != nil {
			return fmt.Errorf("source %q: %w", from, err)
		}
		if info.IsDir() {
			return copyTree(srcAbs, dstAbs)
		}
		return copyFileToDestOrPath(srcAbs, dstAbs, info)
	}

	// Glob source: dest must be (or become) a directory; copy each match
	// preserving its basename.
	matches, err := filepath.Glob(srcAbs)
	if err != nil {
		return fmt.Errorf("invalid glob %q: %w", from, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("source glob %q matched no files", from)
	}
	if err := os.MkdirAll(dstAbs, 0o755); err != nil {
		return err
	}
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstAbs, filepath.Base(m))
		if info.IsDir() {
			if err := copyTree(m, dst); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(m, dst, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// copyFileToDestOrPath copies src into dst. If dst already exists and is
// a directory, the file is placed inside it under its source basename;
// otherwise dst is treated as the target file path.
func copyFileToDestOrPath(src, dst string, srcInfo os.FileInfo) error {
	if info, err := os.Stat(dst); err == nil && info.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	} else if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, srcInfo.Mode())
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// resolveInside joins root with rel and verifies the result does not
// escape root. The manifest validator already enforces the same rule, but
// we re-check here so the executor is safe even if invoked with a
// programmatically-constructed manifest that bypassed validation.
func resolveInside(root, rel string) (string, error) {
	// filepath.IsAbs treats a leading "/" as absolute only on Unix; on
	// Windows an absolute path needs a drive letter, so check the "/" prefix
	// explicitly to reject "/etc/evil"-style paths on every OS.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("path %q must be relative", rel)
	}
	joined := filepath.Join(root, rel)
	cleaned := filepath.Clean(joined)
	rootClean := filepath.Clean(root)
	if cleaned != rootClean && !strings.HasPrefix(cleaned, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the package root", rel)
	}
	return cleaned, nil
}
