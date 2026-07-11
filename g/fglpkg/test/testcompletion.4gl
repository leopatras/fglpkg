#+ tests for completion.4gl (list derivation + script structure)
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.completion
&include "testassert.inc"

MAIN
  CALL testLists()
  CALL testScripts()
  TSUMMARY()
END MAIN

FUNCTION testLists()
  VAR cmds = fglpkgutils.joinArr(completion.sortedCommands(), " ")
  --aliases from the registry must be present
  TOK(fglpkgutils.contains(cmds, " view "))
  TOK(fglpkgutils.contains(cmds, " ws"))
  TOK(fglpkgutils.startsWith(cmds, "audit bdl completion docs env help"))
  --sorted flag list starts with long flags, ends with short ones
  VAR flags = fglpkgutils.joinArr(completion.sortedFlags(), " ")
  TOK(fglpkgutils.startsWith(flags, "--dry-run --force --format="))
  TOK(fglpkgutils.endsWith(flags, "-f -g -h -l -n -o"))
END FUNCTION

FUNCTION testScripts()
  VAR b = completion.bashCompletion()
  TOK(fglpkgutils.contains(b, "_fglpkg_complete()"))
  TOK(fglpkgutils.contains(b, "complete -F _fglpkg_complete fglpkg"))
  VAR z = completion.zshCompletion()
  TOK(fglpkgutils.startsWith(z, "#compdef fglpkg"))
  TOK(fglpkgutils.contains(z, "compdef _fglpkg fglpkg"))
  VAR f = completion.fishCompletion()
  TOK(fglpkgutils.contains(f, "complete -c fglpkg -n '__fish_use_subcommand' -a \"audit\""))
  TOK(fglpkgutils.contains(f, "complete -c fglpkg -l severity="))
  TOK(fglpkgutils.contains(f, "complete -c fglpkg -s f"))
  VAR p = completion.powershellCompletion()
  TOK(fglpkgutils.contains(p, "Register-ArgumentCompleter -Native -CommandName fglpkg"))
  TOK(fglpkgutils.contains(p, "'ParameterValue'"))
END FUNCTION
