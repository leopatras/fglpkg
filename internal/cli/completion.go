package cli

import (
	"fmt"
	"sort"
	"strings"
)

// completionCommands lists every top-level subcommand users can invoke.
// Keep this in lockstep with the switch in runCommand — the schema-like
// drift cost is low, and static lists produce simpler completion scripts
// than introspecting the CLI at runtime.
var completionCommands = []string{
	"init", "install", "remove", "update", "list", "env", "search",
	"publish", "pack", "unpublish", "login", "logout", "whoami",
	"owner", "token", "config", "workspace", "ws", "run", "bdl",
	"docs", "version", "info", "view", "outdated", "audit",
	"sbom", "completion", "help",
}

// completionFlags lists the long + short flags used across commands.
// We deliberately do NOT discriminate per-command: a single universal
// set is simpler in every shell and the cost of showing an inapplicable
// flag in a completion list is zero.
var completionFlags = []string{
	"--local", "-l",
	"--global", "-g",
	"--force", "-f",
	"--gst",
	"--dry-run", "-n",
	"--git",
	"--json",
	"--list",
	"--output", "-o",
	"--production",
	"--severity=",
	"--offline",
	"--pretty",
	"--format=",
	"--help", "-h",
}

// cmdCompletion emits a shell completion script for the selected shell.
//
//	fglpkg completion bash         → sourceable bash script
//	fglpkg completion zsh          → zsh #compdef script
//	fglpkg completion fish         → fish completion file
//	fglpkg completion powershell   → PowerShell Register-ArgumentCompleter block
func cmdCompletion(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: fglpkg completion <bash|zsh|fish|powershell>")
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	case "powershell", "pwsh":
		fmt.Print(powershellCompletion())
	default:
		return fmt.Errorf("unsupported shell %q: expected bash, zsh, fish, or powershell", args[0])
	}
	return nil
}

// sortedCommands returns the command list sorted for deterministic output.
func sortedCommands() []string {
	cs := make([]string, len(completionCommands))
	copy(cs, completionCommands)
	sort.Strings(cs)
	return cs
}

func sortedFlags() []string {
	fs := make([]string, len(completionFlags))
	copy(fs, completionFlags)
	sort.Strings(fs)
	return fs
}

func bashCompletion() string {
	cmds := strings.Join(sortedCommands(), " ")
	flags := strings.Join(sortedFlags(), " ")
	return fmt.Sprintf(`# fglpkg bash completion
# Install:   fglpkg completion bash > /etc/bash_completion.d/fglpkg
# Or source: source <(fglpkg completion bash)

_fglpkg_complete() {
    local cur prev cmds flags
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    cmds="%s"
    flags="%s"

    # First positional argument: suggest subcommands.
    if [ "$COMP_CWORD" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$cmds" -- "$cur") )
        return 0
    fi

    # Anything starting with a dash: suggest flags.
    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
        return 0
    fi

    # Fall back to filename completion (useful for pack -o, workspaces, etc).
    COMPREPLY=( $(compgen -f -- "$cur") )
}
complete -F _fglpkg_complete fglpkg
`, cmds, flags)
}

func zshCompletion() string {
	// zsh native completion. Source or drop into a directory in $fpath.
	cmdLines := make([]string, 0, len(completionCommands))
	for _, c := range sortedCommands() {
		cmdLines = append(cmdLines, fmt.Sprintf("    '%s'", c))
	}
	flagLines := make([]string, 0, len(completionFlags))
	for _, f := range sortedFlags() {
		flagLines = append(flagLines, fmt.Sprintf("    '%s'", f))
	}
	return fmt.Sprintf(`#compdef fglpkg
# fglpkg zsh completion
# Install: fglpkg completion zsh > "${fpath[1]}/_fglpkg"
# Or source: source <(fglpkg completion zsh)

_fglpkg() {
  local -a _cmds _flags
  _cmds=(
%s
  )
  _flags=(
%s
  )

  if (( CURRENT == 2 )); then
    _describe 'command' _cmds
    return
  fi

  if [[ "${words[CURRENT]}" == -* ]]; then
    _describe 'flag' _flags
    return
  fi

  _files
}
compdef _fglpkg fglpkg
`, strings.Join(cmdLines, "\n"), strings.Join(flagLines, "\n"))
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fglpkg fish completion\n")
	b.WriteString("# Install: fglpkg completion fish > ~/.config/fish/completions/fglpkg.fish\n\n")

	// Top-level subcommands — only offered when no subcommand has been typed yet.
	b.WriteString("# Subcommands (only when fglpkg has no subcommand yet)\n")
	for _, c := range sortedCommands() {
		fmt.Fprintf(&b, "complete -c fglpkg -n '__fish_use_subcommand' -a %q\n", c)
	}
	b.WriteString("\n# Flags (available on every subcommand)\n")
	for _, f := range sortedFlags() {
		// Fish wants long and short flags registered separately.
		if strings.HasPrefix(f, "--") {
			fmt.Fprintf(&b, "complete -c fglpkg -l %s\n", strings.TrimPrefix(f, "--"))
		} else {
			fmt.Fprintf(&b, "complete -c fglpkg -s %s\n", strings.TrimPrefix(f, "-"))
		}
	}
	return b.String()
}

func powershellCompletion() string {
	cmdList := make([]string, 0, len(completionCommands))
	for _, c := range sortedCommands() {
		cmdList = append(cmdList, fmt.Sprintf("    '%s'", c))
	}
	flagList := make([]string, 0, len(completionFlags))
	for _, f := range sortedFlags() {
		flagList = append(flagList, fmt.Sprintf("    '%s'", f))
	}
	return fmt.Sprintf(`# fglpkg PowerShell completion
# Install: Add this to your $PROFILE, or
#   fglpkg completion powershell | Out-String | Invoke-Expression

Register-ArgumentCompleter -Native -CommandName fglpkg -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $commands = @(
%s
    )
    $flags = @(
%s
    )

    # Count non-flag tokens before the current one to decide if we're
    # completing a subcommand (position 1) or general arguments.
    $tokens = $commandAst.CommandElements
    $positional = 0
    foreach ($t in $tokens) {
        $text = $t.Extent.Text
        if ($text -notlike '-*' -and $text -ne 'fglpkg') { $positional++ }
    }

    if ($positional -le 1 -and -not $wordToComplete.StartsWith('-')) {
        $commands | Where-Object { $_ -like "$wordToComplete*" } |
            ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
        return
    }

    if ($wordToComplete.StartsWith('-')) {
        $flags | Where-Object { $_ -like "$wordToComplete*" } |
            ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterName', $_) }
    }
}
`, strings.Join(cmdList, ",\n"), strings.Join(flagList, ",\n"))
}
