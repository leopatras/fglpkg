// Package selfupdate implements `fglpkg self-update` (GIS-255): download the
// latest stable release binary for this OS/arch, verify its authenticity (an
// Ed25519 signature chained to the pinned release root) AND its integrity
// (SHA-256), then atomically replace the running executable.
//
// Authenticity is gated before anything is installed: a bad or missing
// signature aborts BEFORE the binary is downloaded, and a checksum mismatch
// aborts before the swap. Every abort returns an error whose message carries the
// GI-served manual-download recovery path; the caller prints it and exits
// non-zero. Scope is latest-stable only — no pinning, pre-release, or downgrade.
package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
	"github.com/4js-mikefolcher/fglpkg/internal/updatecheck"
)

// Options configures a self-update run.
type Options struct {
	Current      string                   // the running version (cli.Version)
	Check        bool                     // report availability and exit; never writes
	Yes          bool                     // skip the confirmation prompt
	Force        bool                     // re-install even if already latest
	Stdout       io.Writer                // progress/success output
	Confirm      func(prompt string) bool // interactive confirm; required unless Yes/Check
	HomeForCache string                   // fglpkg home; refreshes the update-check cache on success

	// Test seams — unexported, so the production API stays clean and callers
	// always get the live behavior. Internal tests set these.
	exePath string
	roots   []signing.Root
	now     time.Time
}

// Run performs the self-update flow. On success it prints to opts.Stdout and
// returns nil. On any failure it returns an error whose message is the
// user-facing guidance (including the recovery path for download/verify aborts);
// the caller prints that verbatim and exits non-zero.
func Run(opts Options) error {
	// 1. Guard: only released builds installed as a plain writable binary.
	if opts.Current == "" || opts.Current == "dev" {
		return errors.New("self-update is only available for released builds (this is a 'dev' build); install a tagged release binary")
	}
	exePath := opts.exePath
	if exePath == "" {
		p, err := resolveExe()
		if err != nil {
			return fmt.Errorf("cannot locate the running executable: %w", err)
		}
		exePath = p
	}
	cleanupStaleWindowsBackup(exePath)
	if mgr := managedBy(exePath); mgr != "" {
		return fmt.Errorf("fglpkg looks installed via %s (%s); update it with that tool instead of self-update", mgr, exePath)
	}

	// 2. Resolve the latest release.
	lr, err := registry.FetchLatestFGLPkg()
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return errors.New("this registry does not provide fglpkg release information yet")
		}
		return fmt.Errorf("could not check for updates: %w", err)
	}
	isNewer := versionNewer(opts.Current, lr.Version)
	if opts.Check {
		if isNewer {
			fmt.Fprintf(opts.Stdout, "A new fglpkg is available: %s → %s\n", opts.Current, lr.Version)
		} else {
			fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		}
		return nil
	}
	if !isNewer && !opts.Force {
		fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		return nil
	}

	// 3. Select the asset for this platform.
	asset := lr.AssetFor(runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		return recoveryErr(lr, fmt.Sprintf("no fglpkg %s binary is published for %s/%s", lr.Version, runtime.GOOS, runtime.GOARCH))
	}

	// 4. Fetch and AUTHENTICATE checksums before downloading the binary.
	if lr.ChecksumsURL == "" || lr.ChecksumsSigURL == "" {
		return recoveryErr(lr, "the release does not publish a signed checksums file")
	}
	checksums, err := fetchURL(lr.ChecksumsURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch checksums.txt: %v", err))
	}
	sig, err := fetchURL(lr.ChecksumsSigURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch the release signature: %v", err))
	}
	keysURL := lr.KeysManifestURL()
	if keysURL == "" {
		return recoveryErr(lr, "the release does not publish a signing-key manifest")
	}
	keysJSON, err := fetchURL(keysURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch the signing-key manifest: %v", err))
	}
	roots := opts.roots
	if roots == nil {
		roots = signing.PinnedRoots()
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	vc, err := authenticateChecksums(checksums, sig, keysJSON, roots, now)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("release authenticity check failed: %v", err))
	}
	assetName := filepath.Base(asset.URL)
	expectedSHA, ok := vc.sha(assetName)
	if !ok {
		return recoveryErr(lr, fmt.Sprintf("the signed checksums have no entry for %s", assetName))
	}

	// 5. Confirm.
	if !opts.Yes {
		prompt := fmt.Sprintf("Update fglpkg v%s → v%s?", opts.Current, lr.Version)
		if opts.Confirm == nil || !opts.Confirm(prompt) {
			fmt.Fprintln(opts.Stdout, "Update cancelled.")
			return nil
		}
	}

	// 6. Download to a temp file in the target directory (same filesystem, so
	//    the final rename is atomic).
	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".fglpkg-update-*")
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("cannot write to %s (insufficient permissions?): %v", dir, err))
	}
	staged := tmp.Name()
	defer os.Remove(staged) // no-op once renamed into place
	fmt.Fprintf(opts.Stdout, "Downloading fglpkg v%s for %s/%s…\n", lr.Version, runtime.GOOS, runtime.GOARCH)
	if err := downloadTo(tmp, asset.URL); err != nil {
		tmp.Close()
		return recoveryErr(lr, fmt.Sprintf("download failed: %v", err))
	}
	tmp.Close()

	// 7. Integrity gate: verify the download against the authenticated SHA-256.
	if err := checksum.VerifyFile(staged, expectedSHA); err != nil {
		return recoveryErr(lr, fmt.Sprintf("downloaded binary failed checksum verification: %v", err))
	}

	// 8. Swap atomically, preserving the executable bit.
	if err := applyMode(staged, exePath); err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not set permissions on the new binary: %v", err))
	}
	if err := atomicReplace(exePath, staged); err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not replace %s (insufficient permissions?): %v", exePath, err))
	}

	// 9. Done. Refresh the update-check cache so the passive notice goes quiet.
	fmt.Fprintf(opts.Stdout, "Updated fglpkg v%s → v%s\n", opts.Current, lr.Version)
	if opts.HomeForCache != "" {
		_ = updatecheck.SaveState(opts.HomeForCache, updatecheck.State{LastCheck: now, LatestKnown: lr.Version})
	}
	return nil
}

// resolveExe returns the absolute path of the running executable with symlinks
// resolved, so we replace the real file rather than a symlink into it.
func resolveExe() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return p, nil
}

// managedBy returns a best-effort package-manager name if exePath looks owned by
// one (Homebrew, Linuxbrew), else "". Conservative: when unsure it returns ""
// and lets the atomic-write step fail cleanly instead.
func managedBy(exePath string) string {
	lower := strings.ToLower(filepath.ToSlash(exePath))
	switch {
	case strings.Contains(lower, "/cellar/"), strings.Contains(lower, "/homebrew/"), strings.Contains(lower, "/.linuxbrew/"):
		return "Homebrew"
	}
	return ""
}

// versionNewer reports whether latest is a newer release than current.
// Unparseable versions are treated as not newer (fail safe).
func versionNewer(current, latest string) bool {
	c, err1 := semver.Parse(current)
	l, err2 := semver.Parse(latest)
	if err1 != nil || err2 != nil {
		return false
	}
	return l.GreaterThan(c)
}

// recoveryErr builds an error whose message carries the GI-served manual-download
// recovery path verbatim, appended to msg. The caller prints it and exits.
func recoveryErr(lr *registry.LatestRelease, msg string) error {
	var b strings.Builder
	b.WriteString(msg)
	if lr != nil && lr.ManualURL != "" {
		fmt.Fprintf(&b, "\nDownload manually: %s", lr.ManualURL)
	}
	if lr != nil && lr.Instructions != "" {
		fmt.Fprintf(&b, "\n%s", lr.Instructions)
	}
	return errors.New(b.String())
}

// fetchURL GETs url and returns the body, erroring on a non-2xx status. Used for
// release assets on GitHub (not registry endpoints), so it is unauthenticated
// and follows redirects (http.DefaultClient default).
func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// downloadTo streams url into w.
func downloadTo(w io.Writer, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
