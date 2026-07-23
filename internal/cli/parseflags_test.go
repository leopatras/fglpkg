package cli

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseFlagsRejectsUnknownFlag is the regression for issue #24 M4: a
// flag-looking token that isn't recognized must error rather than being routed
// into the positional args (so `remove --registry x` cannot delete a package
// literally named "x").
func TestParseFlagsRejectsUnknownFlag(t *testing.T) {
	_, _, _, _, err := parseFlags([]string{"--registry", "acme"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	if !strings.Contains(err.Error(), "--registry") {
		t.Errorf("error should name the offending flag, got: %v", err)
	}
}

func TestParseFlagsKnownAndPositional(t *testing.T) {
	rem, local, global, force, err := parseFlags([]string{"pkg-a", "--local", "--force", "pkg-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !local || global || !force {
		t.Errorf("flags = local:%v global:%v force:%v, want local:true global:false force:true", local, global, force)
	}
	if !reflect.DeepEqual(rem, []string{"pkg-a", "pkg-b"}) {
		t.Errorf("positional = %v, want [pkg-a pkg-b]", rem)
	}
}

func TestParseFlagsExtraAllowed(t *testing.T) {
	// env passes its own --gst/--gwa flags through extraAllowed; they must not
	// be rejected as unknown, and must not leak into the positional args.
	rem, _, _, _, err := parseFlags([]string{"--gst", "--gwa"}, "--gst", "--gwa")
	if err != nil {
		t.Fatalf("extraAllowed flags should be accepted, got: %v", err)
	}
	if len(rem) != 0 {
		t.Errorf("extraAllowed flags should not appear as positional args, got: %v", rem)
	}
}

// TestCmdRemoveRejectsUnknownFlag confirms the fix surfaces through the command
// entry point before any manifest/filesystem work happens.
func TestCmdRemoveRejectsUnknownFlag(t *testing.T) {
	err := cmdRemove([]string{"--registry", "acme"})
	if err == nil {
		t.Fatal("expected cmdRemove to reject --registry, got nil")
	}
	if !strings.Contains(err.Error(), "--registry") {
		t.Errorf("error should name the offending flag, got: %v", err)
	}
}
