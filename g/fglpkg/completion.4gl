#+ fglpkg completion — shell completion script generation
#+ port of internal/cli/completion.go; the command list is derived from
#+ the commands registry so it stays in lockstep automatically
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.commands
&include "myassert.inc"

#+the completion command; returns the process exit code
FUNCTION cmdCompletion(args fglpkgutils.TStringArr) RETURNS INT
  IF args.getLength() != 1 THEN
    CALL fglpkgutils.printStderr(
        "usage: fglpkg completion <bash|zsh|fish|powershell>")
    RETURN 1
  END IF
  CASE args[1]
    WHEN "bash"
      CALL fglpkgutils.printStdoutNoNL(bashCompletion())
    WHEN "zsh"
      CALL fglpkgutils.printStdoutNoNL(zshCompletion())
    WHEN "fish"
      CALL fglpkgutils.printStdoutNoNL(fishCompletion())
    WHEN "powershell"
      CALL fglpkgutils.printStdoutNoNL(powershellCompletion())
    WHEN "pwsh"
      CALL fglpkgutils.printStdoutNoNL(powershellCompletion())
    OTHERWISE
      CALL fglpkgutils.printStderr(
          SFMT('unsupported shell "%1": expected bash, zsh, fish, or powershell',
              args[1]))
      RETURN 1
  END CASE
  RETURN 0
END FUNCTION

#+every command name and alias from the registry, sorted
FUNCTION sortedCommands() RETURNS fglpkgutils.TStringArr
  DEFINE names fglpkgutils.TStringArr
  DEFINE i, j INT
  VAR cmds = commands.commands()
  FOR i = 1 TO cmds.getLength()
    LET names[names.getLength() + 1] = cmds[i].name
    FOR j = 1 TO cmds[i].aliases.getLength()
      LET names[names.getLength() + 1] = cmds[i].aliases[j]
    END FOR
  END FOR
  CALL glob.sortBytewise(names)
  RETURN names
END FUNCTION

#+the universal flag set (deliberately not per-command), sorted
FUNCTION sortedFlags() RETURNS fglpkgutils.TStringArr
  DEFINE flags fglpkgutils.TStringArr
  LET flags[1] = "--local"
  LET flags[2] = "-l"
  LET flags[3] = "--global"
  LET flags[4] = "-g"
  LET flags[5] = "--force"
  LET flags[6] = "-f"
  LET flags[7] = "--gst"
  LET flags[8] = "--dry-run"
  LET flags[9] = "-n"
  LET flags[10] = "--git"
  LET flags[11] = "--json"
  LET flags[12] = "--list"
  LET flags[13] = "--output"
  LET flags[14] = "-o"
  LET flags[15] = "--production"
  LET flags[16] = "--severity="
  LET flags[17] = "--offline"
  LET flags[18] = "--pretty"
  LET flags[19] = "--format="
  LET flags[20] = "--help"
  LET flags[21] = "-h"
  CALL glob.sortBytewise(flags)
  RETURN flags
END FUNCTION

FUNCTION bashCompletion() RETURNS STRING
  VAR cmds = fglpkgutils.joinArr(sortedCommands(), " ")
  VAR flags = fglpkgutils.joinArr(sortedFlags(), " ")
  VAR sb = base.StringBuffer.create()
  CALL sb.append("# fglpkg bash completion\n")
  CALL sb.append("# Install:   fglpkg completion bash > /etc/bash_completion.d/fglpkg\n")
  CALL sb.append("# Or source: source <(fglpkg completion bash)\n")
  CALL sb.append("\n")
  CALL sb.append("_fglpkg_complete() {\n")
  CALL sb.append("    local cur prev cmds flags\n")
  CALL sb.append("    COMPREPLY=()\n")
  CALL sb.append('    cur="${COMP_WORDS[COMP_CWORD]}"\n')
  CALL sb.append('    prev="${COMP_WORDS[COMP_CWORD-1]}"\n')
  CALL sb.append(SFMT('    cmds="%1"\n', cmds))
  CALL sb.append(SFMT('    flags="%1"\n', flags))
  CALL sb.append("\n")
  CALL sb.append("    # First positional argument: suggest subcommands.\n")
  CALL sb.append('    if [ "$COMP_CWORD" -eq 1 ]; then\n')
  CALL sb.append('        COMPREPLY=( $(compgen -W "$cmds" -- "$cur") )\n')
  CALL sb.append("        return 0\n")
  CALL sb.append("    fi\n")
  CALL sb.append("\n")
  CALL sb.append("    # Anything starting with a dash: suggest flags.\n")
  CALL sb.append('    if [[ "$cur" == -* ]]; then\n')
  CALL sb.append('        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )\n')
  CALL sb.append("        return 0\n")
  CALL sb.append("    fi\n")
  CALL sb.append("\n")
  CALL sb.append("    # Fall back to filename completion (useful for pack -o, workspaces, etc).\n")
  CALL sb.append('    COMPREPLY=( $(compgen -f -- "$cur") )\n')
  CALL sb.append("}\n")
  CALL sb.append("complete -F _fglpkg_complete fglpkg\n")
  RETURN sb.toString()
END FUNCTION

FUNCTION zshCompletion() RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  CALL sb.append("#compdef fglpkg\n")
  CALL sb.append("# fglpkg zsh completion\n")
  CALL sb.append('# Install: fglpkg completion zsh > "${fpath[1]}/_fglpkg"\n')
  CALL sb.append("# Or source: source <(fglpkg completion zsh)\n")
  CALL sb.append("\n")
  CALL sb.append("_fglpkg() {\n")
  CALL sb.append("  local -a _cmds _flags\n")
  CALL sb.append("  _cmds=(\n")
  VAR cmds = sortedCommands()
  FOR i = 1 TO cmds.getLength()
    CALL sb.append(SFMT("    '%1'\n", cmds[i]))
  END FOR
  CALL sb.append("  )\n")
  CALL sb.append("  _flags=(\n")
  VAR flags = sortedFlags()
  FOR i = 1 TO flags.getLength()
    CALL sb.append(SFMT("    '%1'\n", flags[i]))
  END FOR
  CALL sb.append("  )\n")
  CALL sb.append("\n")
  CALL sb.append("  if (( CURRENT == 2 )); then\n")
  CALL sb.append("    _describe 'command' _cmds\n")
  CALL sb.append("    return\n")
  CALL sb.append("  fi\n")
  CALL sb.append("\n")
  CALL sb.append('  if [[ "${words[CURRENT]}" == -* ]]; then\n')
  CALL sb.append("    _describe 'flag' _flags\n")
  CALL sb.append("    return\n")
  CALL sb.append("  fi\n")
  CALL sb.append("\n")
  CALL sb.append("  _files\n")
  CALL sb.append("}\n")
  CALL sb.append("compdef _fglpkg fglpkg\n")
  RETURN sb.toString()
END FUNCTION

FUNCTION fishCompletion() RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  CALL sb.append("# fglpkg fish completion\n")
  CALL sb.append("# Install: fglpkg completion fish > ~/.config/fish/completions/fglpkg.fish\n")
  CALL sb.append("\n")
  CALL sb.append("# Subcommands (only when fglpkg has no subcommand yet)\n")
  VAR cmds = sortedCommands()
  FOR i = 1 TO cmds.getLength()
    CALL sb.append(SFMT('complete -c fglpkg -n \'__fish_use_subcommand\' -a "%1"\n',
        cmds[i]))
  END FOR
  CALL sb.append("\n# Flags (available on every subcommand)\n")
  VAR flags = sortedFlags()
  FOR i = 1 TO flags.getLength()
    IF fglpkgutils.startsWith(flags[i], "--") THEN
      CALL sb.append(SFMT("complete -c fglpkg -l %1\n",
          flags[i].subString(3, flags[i].getLength())))
    ELSE
      CALL sb.append(SFMT("complete -c fglpkg -s %1\n",
          flags[i].subString(2, flags[i].getLength())))
    END IF
  END FOR
  RETURN sb.toString()
END FUNCTION

FUNCTION powershellCompletion() RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  CALL sb.append("# fglpkg PowerShell completion\n")
  CALL sb.append("# Install: Add this to your $PROFILE, or\n")
  CALL sb.append("#   fglpkg completion powershell | Out-String | Invoke-Expression\n")
  CALL sb.append("\n")
  CALL sb.append("Register-ArgumentCompleter -Native -CommandName fglpkg -ScriptBlock {\n")
  CALL sb.append("    param($wordToComplete, $commandAst, $cursorPosition)\n")
  CALL sb.append("\n")
  CALL sb.append("    $commands = @(\n")
  VAR cmds = sortedCommands()
  FOR i = 1 TO cmds.getLength()
    CALL sb.append(SFMT("    '%1'%2\n",
        cmds[i], IIF(i < cmds.getLength(), ",", "")))
  END FOR
  CALL sb.append("    )\n")
  CALL sb.append("    $flags = @(\n")
  VAR flags = sortedFlags()
  FOR i = 1 TO flags.getLength()
    CALL sb.append(SFMT("    '%1'%2\n",
        flags[i], IIF(i < flags.getLength(), ",", "")))
  END FOR
  CALL sb.append("    )\n")
  CALL sb.append("\n")
  CALL sb.append("    # Count non-flag tokens before the current one to decide if we're\n")
  CALL sb.append("    # completing a subcommand (position 1) or general arguments.\n")
  CALL sb.append("    $tokens = $commandAst.CommandElements\n")
  CALL sb.append("    $positional = 0\n")
  CALL sb.append("    foreach ($t in $tokens) {\n")
  CALL sb.append("        $text = $t.Extent.Text\n")
  CALL sb.append("        if ($text -notlike '-*' -and $text -ne 'fglpkg') { $positional++ }\n")
  CALL sb.append("    }\n")
  CALL sb.append("\n")
  CALL sb.append("    if ($positional -le 1 -and -not $wordToComplete.StartsWith('-')) {\n")
  CALL sb.append('        $commands | Where-Object { $_ -like "$wordToComplete*" } |\n')
  CALL sb.append("            ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }\n")
  CALL sb.append("        return\n")
  CALL sb.append("    }\n")
  CALL sb.append("\n")
  CALL sb.append("    if ($wordToComplete.StartsWith('-')) {\n")
  CALL sb.append('        $flags | Where-Object { $_ -like "$wordToComplete*" } |\n')
  CALL sb.append("            ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterName', $_) }\n")
  CALL sb.append("    }\n")
  CALL sb.append("}\n")
  RETURN sb.toString()
END FUNCTION
