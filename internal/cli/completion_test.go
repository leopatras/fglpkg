package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCompletionShellContents checks each shell's output contains the
// load-bearing markers (the command list, the dispatcher function, and
// the completion registration).
func TestCompletionShellContents(t *testing.T) {
	cases := []struct {
		shell    string
		produce  func() string
		mustHave []string
	}{
		{
			shell:   "bash",
			produce: bashCompletion,
			mustHave: []string{
				"_fglpkg_complete()",
				"complete -F _fglpkg_complete fglpkg",
				"install", "publish", "outdated",
				"--json", "--dry-run",
			},
		},
		{
			shell:   "zsh",
			produce: zshCompletion,
			mustHave: []string{
				"#compdef fglpkg",
				"_fglpkg()",
				"compdef _fglpkg fglpkg",
				"install", "publish", "outdated",
				"--json",
			},
		},
		{
			shell:   "fish",
			produce: fishCompletion,
			mustHave: []string{
				"complete -c fglpkg",
				"__fish_use_subcommand",
				"install", "publish", "outdated",
				"-l json", "-l dry-run", "-s n",
			},
		},
		{
			shell:   "powershell",
			produce: powershellCompletion,
			mustHave: []string{
				"Register-ArgumentCompleter",
				"-CommandName fglpkg",
				"install", "publish", "outdated",
				"--json", "--dry-run",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			out := tc.produce()
			if len(out) < 100 {
				t.Fatalf("%s completion suspiciously short (%d bytes)", tc.shell, len(out))
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(out, want) {
					t.Errorf("%s completion missing %q", tc.shell, want)
				}
			}
		})
	}
}

// TestBashCompletionSyntaxValid runs the generated bash completion
// through `bash -n` to check for syntax errors. Skipped if bash is not
// available in PATH (e.g., minimal container images).
func TestBashCompletionSyntaxValid(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH")
	}
	// On Windows, "bash" often resolves to the WSL launcher
	// (C:\Windows\System32\bash.exe), which stays on PATH even with no WSL
	// distro installed and then fails at exec time ("execvpe(/bin/bash)
	// failed"). LookPath succeeding doesn't mean bash actually runs, so probe
	// with a trivial script and skip if bash is present but non-functional.
	if err := exec.Command("bash", "-n", "-c", "").Run(); err != nil {
		t.Skipf("bash present but not functional: %v", err)
	}
	cmd := exec.Command("bash", "-n", "-c", bashCompletion())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected generated script: %v\n%s", err, out)
	}
}

// TestCmdCompletionDispatch confirms the CLI dispatcher accepts each
// supported shell name and rejects unknown ones.
func TestCmdCompletionDispatch(t *testing.T) {
	supported := []string{"bash", "zsh", "fish", "powershell", "pwsh"}
	for _, s := range supported {
		t.Run(s, func(t *testing.T) {
			out, err := captureStdout(t, func() error { return cmdCompletion([]string{s}) })
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(out) == 0 {
				t.Errorf("no output produced for %s", s)
			}
		})
	}

	if err := cmdCompletion([]string{"csh"}); err == nil {
		t.Error("expected error for unknown shell, got nil")
	}
	if err := cmdCompletion([]string{}); err == nil {
		t.Error("expected error for no-args, got nil")
	}
}
