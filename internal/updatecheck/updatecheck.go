// Package updatecheck implements fglpkg's passive "a new version is available"
// notice (GIS-255). It piggybacks on ordinary command runs — no daemon — and is
// designed to never block the command, never change its exit code, and never
// surface an error to the user (network failures are swallowed).
//
// User settings (opt-out, interval) live in config.json and are read-only. The
// mutable cache — last check time and last seen version — lives here, in a
// tool-managed ~/.fglpkg/update-check.json (mode 0600, atomic writes), so the
// feature never rewrites the user's hand-edited registry config.
package updatecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/atomicfile"
)

// StateFilename is the tool-managed cache file under the fglpkg home.
const StateFilename = "update-check.json"

// State is the cache persisted in update-check.json.
type State struct {
	LastCheck   time.Time `json:"lastUpdateCheck"`
	LatestKnown string    `json:"latestKnownVersion"`
}

func statePath(home string) string { return filepath.Join(home, StateFilename) }

// LoadState reads update-check.json. A missing, blank, or corrupt file yields a
// zero State — the check is best-effort, so it never hard-fails.
func LoadState(home string) State {
	data, err := os.ReadFile(statePath(home))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return State{}
	}
	var s State
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// SaveState atomically writes update-check.json (mode 0600). The write goes to a
// sibling temp file and is renamed into place, so a process killed mid-write
// (e.g. a fast command exiting before the background fetch returns) leaves the
// existing cache intact rather than a truncated file.
func SaveState(home string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.WriteFile(statePath(home), data, 0o600)
}

// Env captures every input to the throttle decision, so ShouldCheck is a pure
// function — testable without a real environment, TTY, or clock.
type Env struct {
	Version     string        // cli.Version; "" or "dev" disables (source build)
	Command     string        // the invoked subcommand
	CI          bool          // $CI is set
	NoCheckEnv  bool          // $FGLPKG_NO_UPDATE_CHECK is set
	StdoutIsTTY bool          // don't pollute piped/scripted output
	Enabled     bool          // config.json updateCheck
	Interval    time.Duration // config.json updateCheckInterval
	Now         time.Time
	LastCheck   time.Time // from the cached State
}

// ShouldCheck reports whether the passive check should run this invocation.
func ShouldCheck(e Env) bool {
	if e.Version == "" || e.Version == "dev" {
		return false
	}
	if e.CI || e.NoCheckEnv || !e.Enabled {
		return false
	}
	if !e.StdoutIsTTY {
		return false
	}
	switch e.Command {
	case "self-update", "upgrade", "version", "--version", "-v", "-V":
		return false
	}
	if !e.LastCheck.IsZero() && e.Now.Sub(e.LastCheck) < e.Interval {
		return false
	}
	return true
}

// Pending is a handle to an in-flight background check.
type Pending struct {
	current string
	ch      chan string
}

// Start kicks off a background update check when ShouldCheck(e) is true. fetch
// returns the latest version string (registry.FetchLatestFGLPkg().Version in
// production); prevLatest is the previously cached version, preserved on a fetch
// failure. It never blocks. When the check should not run it returns nil, and
// Finish on a nil *Pending is a no-op.
//
// The cache is updated by the background goroutine as soon as the fetch returns
// (recording the attempt time even on failure, so a missing/flaky endpoint is
// not hammered), independently of whether Finish ends up printing a notice.
func Start(home string, e Env, prevLatest string, fetch func() (string, error)) *Pending {
	if !ShouldCheck(e) {
		return nil
	}
	p := &Pending{current: e.Version, ch: make(chan string, 1)}
	now := e.Now
	go func() {
		latest, err := fetch()
		st := State{LastCheck: now, LatestKnown: prevLatest}
		if err == nil && latest != "" {
			st.LatestKnown = latest
		}
		_ = SaveState(home, st)
		if err != nil {
			p.ch <- ""
			return
		}
		p.ch <- latest
	}()
	return p
}

// Finish prints a one-line notice to w iff the background check has ALREADY
// returned a newer version — newer(current, latest) decides. It never blocks: if
// the result is not yet in, it is skipped (the goroutine still updates the cache
// for next time). Safe to call on a nil *Pending.
func (p *Pending) Finish(w io.Writer, newer func(current, latest string) bool) {
	if p == nil {
		return
	}
	select {
	case latest := <-p.ch:
		if latest != "" && newer(p.current, latest) {
			printNotice(w, p.current, latest)
		}
	default:
		// Result not back yet — skip, per the non-blocking contract.
	}
}

func printNotice(w io.Writer, current, latest string) {
	bar := strings.Repeat("─", 45)
	fmt.Fprintf(w, "\n%s\n A new fglpkg is available: %s → %s\n Run 'fglpkg self-update' to upgrade.\n%s\n",
		bar, current, latest, bar)
}
