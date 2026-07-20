package updatecheck

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func baseEnv() Env {
	return Env{
		Version:     "3.3.0",
		Command:     "install",
		StdoutIsTTY: true,
		Enabled:     true,
		Interval:    24 * time.Hour,
		Now:         time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
	}
}

func TestShouldCheck(t *testing.T) {
	if !ShouldCheck(baseEnv()) {
		t.Fatal("base env should check")
	}
	cases := map[string]func(*Env){
		"dev version":     func(e *Env) { e.Version = "dev" },
		"empty version":   func(e *Env) { e.Version = "" },
		"CI":              func(e *Env) { e.CI = true },
		"env opt-out":     func(e *Env) { e.NoCheckEnv = true },
		"config disabled": func(e *Env) { e.Enabled = false },
		"not a TTY":       func(e *Env) { e.StdoutIsTTY = false },
		"self-update cmd": func(e *Env) { e.Command = "self-update" },
		"upgrade cmd":     func(e *Env) { e.Command = "upgrade" },
		"version cmd":     func(e *Env) { e.Command = "version" },
		"within interval": func(e *Env) { e.LastCheck = e.Now.Add(-time.Hour) },
	}
	for name, mut := range cases {
		e := baseEnv()
		mut(&e)
		if ShouldCheck(e) {
			t.Errorf("%s: should NOT check", name)
		}
	}
	// Past the interval, it checks again.
	e := baseEnv()
	e.LastCheck = e.Now.Add(-48 * time.Hour)
	if !ShouldCheck(e) {
		t.Error("past interval: should check")
	}
}

func TestStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	if got := LoadState(home); !got.LastCheck.IsZero() || got.LatestKnown != "" {
		t.Errorf("missing file should be zero State, got %+v", got)
	}
	want := State{LastCheck: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC), LatestKnown: "3.4.0"}
	if err := SaveState(home, want); err != nil {
		t.Fatal(err)
	}
	got := LoadState(home)
	if !got.LastCheck.Equal(want.LastCheck) || got.LatestKnown != want.LatestKnown {
		t.Errorf("round-trip: got %+v, want %+v", got, want)
	}
}

func newerByString(cur, lat string) bool { return cur != lat }

func TestFinishPrintsWhenReady(t *testing.T) {
	p := &Pending{current: "3.3.0", ch: make(chan string, 1)}
	p.ch <- "3.4.0"
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if !strings.Contains(buf.String(), "3.3.0 → 3.4.0") {
		t.Errorf("expected upgrade notice, got %q", buf.String())
	}
}

func TestFinishSkipsWhenNotReady(t *testing.T) {
	p := &Pending{current: "3.3.0", ch: make(chan string, 1)} // empty channel
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if buf.Len() != 0 {
		t.Errorf("expected no output when result not ready, got %q", buf.String())
	}
}

func TestFinishNilNoop(t *testing.T) {
	var p *Pending
	var buf bytes.Buffer
	p.Finish(&buf, newerByString) // must not panic
	if buf.Len() != 0 {
		t.Errorf("nil Finish should print nothing, got %q", buf.String())
	}
}

func TestStartRunsAndCaches(t *testing.T) {
	home := t.TempDir()
	p := Start(home, baseEnv(), "", func() (string, error) { return "3.4.0", nil })
	if p == nil {
		t.Fatal("Start returned nil for a should-check env")
	}
	// Reading the channel waits for the goroutine, which writes the cache before
	// sending — so this is deterministic, not a sleep.
	if got := <-p.ch; got != "3.4.0" {
		t.Errorf("channel got %q, want 3.4.0", got)
	}
	if st := LoadState(home); st.LatestKnown != "3.4.0" || st.LastCheck.IsZero() {
		t.Errorf("cache not updated: %+v", st)
	}
}

func TestStartSkipReturnsNil(t *testing.T) {
	e := baseEnv()
	e.Enabled = false
	if p := Start(t.TempDir(), e, "", func() (string, error) { return "3.4.0", nil }); p != nil {
		t.Error("Start should return nil when the check is disabled")
	}
}

func TestStartPreservesPrevOnFetchError(t *testing.T) {
	home := t.TempDir()
	p := Start(home, baseEnv(), "3.2.0", func() (string, error) { return "", errors.New("boom") })
	if p == nil {
		t.Fatal("Start returned nil")
	}
	if got := <-p.ch; got != "" {
		t.Errorf("channel got %q, want empty on fetch error", got)
	}
	st := LoadState(home)
	if st.LatestKnown != "3.2.0" {
		t.Errorf("prev version not preserved on error: %+v", st)
	}
	if st.LastCheck.IsZero() {
		t.Error("attempt time should be recorded even on error (backoff)")
	}
}
