package cli

import (
	"strings"
	"testing"
)

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
