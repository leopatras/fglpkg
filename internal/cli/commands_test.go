package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// dispatchCaseRE pulls the quoted labels out of a `case "a", "b":` line.
var dispatchCaseRE = regexp.MustCompile(`"([^"]+)"`)

// dispatchedCommands scans the Execute dispatch switch in cli.go and returns
// the set of command labels it routes. It isolates the switch that follows
// startUpdateCheck(cmd) so flag-parsing switches elsewhere in the file are not
// picked up.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("cli.go")
	if err != nil {
		t.Fatalf("read cli.go: %v", err)
	}
	text := string(src)
	anchor := strings.Index(text, "startUpdateCheck(cmd)")
	if anchor < 0 {
		t.Fatal("could not locate the dispatch anchor startUpdateCheck(cmd)")
	}
	swi := strings.Index(text[anchor:], "switch cmd {")
	if swi < 0 {
		t.Fatal("could not locate the dispatch switch")
	}
	region := text[anchor+swi:]
	if end := strings.Index(region, "default:"); end >= 0 {
		region = region[:end]
	}
	out := map[string]bool{}
	for _, line := range strings.Split(region, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "case ") {
			continue
		}
		for _, m := range dispatchCaseRE.FindAllStringSubmatch(trimmed, -1) {
			out[m[1]] = true
		}
	}
	return out
}

// TestRegistryMatchesDispatch guards the invariant the commands.go doc comment
// promises: every dispatched command has a registry entry (so it appears in
// help/completion) and every registry entry is dispatched (so invoking it does
// not fall through to "unknown command"). The top-level help pseudo-commands
// are dispatched but intentionally not registered.
func TestRegistryMatchesDispatch(t *testing.T) {
	helpPseudo := map[string]bool{"help": true, "--help": true, "-h": true}

	dispatched := dispatchedCommands(t)
	registered := map[string]bool{}
	for _, c := range commands {
		for _, key := range append([]string{c.Name}, c.Aliases...) {
			registered[key] = true
		}
	}

	for name := range dispatched {
		if helpPseudo[name] {
			continue
		}
		if !registered[name] {
			t.Errorf("command %q is dispatched in cli.go but missing from the commands registry", name)
		}
	}
	for name := range registered {
		if !dispatched[name] {
			t.Errorf("command %q is in the commands registry but not dispatched in cli.go", name)
		}
	}
}

// TestRegistryEntriesWellFormed verifies every command carries the metadata
// the help renderer and top-level listing depend on.
func TestRegistryEntriesWellFormed(t *testing.T) {
	seen := map[string]string{} // name/alias -> owning command
	for _, c := range commands {
		if c.Name == "" {
			t.Errorf("command with empty Name: %+v", c)
		}
		if c.Summary == "" {
			t.Errorf("command %q has no Summary", c.Name)
		}
		if c.Usage == "" {
			t.Errorf("command %q has no Usage", c.Name)
		}
		for _, key := range append([]string{c.Name}, c.Aliases...) {
			if owner, dup := seen[key]; dup {
				t.Errorf("name/alias %q claimed by both %q and %q", key, owner, c.Name)
			}
			seen[key] = c.Name
		}
	}
}

// TestCommandIndexResolvesAliases confirms aliases point at the same entry as
// their canonical command.
func TestCommandIndexResolvesAliases(t *testing.T) {
	cases := map[string]string{"view": "info", "ws": "workspace"}
	for alias, canonical := range cases {
		c := commandIndex[alias]
		if c == nil {
			t.Fatalf("alias %q not in commandIndex", alias)
		}
		if c.Name != canonical {
			t.Errorf("alias %q resolves to %q, want %q", alias, c.Name, canonical)
		}
	}
}

// TestPrintCommandHelpContents checks the rendered help page includes the
// header and usage synopsis for a representative command.
func TestPrintCommandHelpContents(t *testing.T) {
	c := commandIndex["install"]
	out, err := captureStdout(t, func() error { printCommandHelp(c); return nil })
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	for _, want := range []string{"fglpkg install", "USAGE:", "--save-dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("install help missing %q\n---\n%s", want, out)
		}
	}
}

// TestHelpRequested covers the passthrough distinction: a normal command
// treats a help flag anywhere as a help request, while a passthrough command
// only honours it in the leading position (the rest goes to the child).
func TestHelpRequested(t *testing.T) {
	normal := commandIndex["install"]
	pass := commandIndex["run"]

	cases := []struct {
		name string
		cmd  *command
		args []string
		want bool
	}{
		{"normal leading", normal, []string{"--help"}, true},
		{"normal trailing", normal, []string{"pkg", "-h"}, true},
		{"normal none", normal, []string{"pkg"}, false},
		{"passthrough leading", pass, []string{"-h"}, true},
		{"passthrough trailing forwarded", pass, []string{"mytool", "--help"}, false},
		{"passthrough none", pass, []string{"mytool"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cmd.helpRequested(tc.args); got != tc.want {
				t.Errorf("helpRequested(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
