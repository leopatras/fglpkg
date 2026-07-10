#+ the CLI command registry: single source of truth for help/usage text
#+ port of internal/cli/commands.go — the registry describes commands,
#+ dispatch stays a CASE in cli.4gl
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT FGL fglpkg.fglpkgutils
&include "myassert.inc"

PUBLIC CONSTANT TOOL_VERSION = "3.1.0"
PUBLIC CONSTANT TOOL_BUILD = "4gl"

PUBLIC TYPE TCommand RECORD
  name STRING, --canonical name (matches the dispatch CASE)
  aliases fglpkgutils.TStringArr,
  summary STRING, --one-line description
  listDetail STRING, --extra text in the top-level list ONLY
  args STRING, --compact positional hint, e.g. "[pkg...]"
  usage STRING, --synopsis line(s), newline separated
  long STRING, --detailed body shown by --help
  passthrough BOOLEAN --run/bdl: only a LEADING -h/--help is ours
END RECORD

PUBLIC TYPE TCommands DYNAMIC ARRAY OF TCommand

DEFINE _commands TCommands

#+the ordered command registry (order controls the printUsage listing)
FUNCTION commands() RETURNS TCommands
  IF _commands.getLength() == 0 THEN
    CALL initCommands()
  END IF
  RETURN _commands
END FUNCTION

#+index of the command matching name (canonical or alias), 0 if unknown
FUNCTION findCommand(name STRING) RETURNS INT
  DEFINE i, j INT
  VAR cmds = commands()
  FOR i = 1 TO cmds.getLength()
    IF cmds[i].name == name THEN
      RETURN i
    END IF
    FOR j = 1 TO cmds[i].aliases.getLength()
      IF cmds[i].aliases[j] == name THEN
        RETURN i
      END IF
    END FOR
  END FOR
  RETURN 0
END FUNCTION

FUNCTION isHelpFlag(a STRING) RETURNS BOOLEAN
  RETURN a == "-h" OR a == "--help"
END FUNCTION

#+whether args ask for this command's help; for passthrough commands only
#+the first argument counts, so help flags for the child are forwarded
FUNCTION helpRequested(idx INT, args fglpkgutils.TStringArr) RETURNS BOOLEAN
  DEFINE i INT
  VAR cmds = commands()
  IF cmds[idx].passthrough THEN
    RETURN args.getLength() > 0 AND isHelpFlag(args[1])
  END IF
  FOR i = 1 TO args.getLength()
    IF isHelpFlag(args[i]) THEN
      RETURN TRUE
    END IF
  END FOR
  RETURN FALSE
END FUNCTION

#+renders one command's help page: header, aliases, usage, detailed body
FUNCTION printCommandHelp(idx INT)
  DEFINE i INT
  VAR cmds = commands()
  DISPLAY SFMT("fglpkg %1 - %2\n", cmds[idx].name, cmds[idx].summary)
  IF cmds[idx].aliases.getLength() > 0 THEN
    DISPLAY SFMT("ALIASES:\n  %1\n",
        fglpkgutils.joinArr(cmds[idx].aliases, ", "))
  END IF
  DISPLAY "USAGE:"
  VAR usageLines = fglpkgutils.splitOnChar(cmds[idx].usage, "\n")
  FOR i = 1 TO usageLines.getLength()
    IF usageLines[i].getLength() > 0 THEN
      DISPLAY SFMT("  %1", usageLines[i])
    END IF
  END FOR
  IF cmds[idx].long.getLength() > 0 THEN
    DISPLAY ""
    CALL fglpkgutils.printStdoutNoNL(cmds[idx].long)
  END IF
END FUNCTION

#+the top-level usage/help listing
FUNCTION printUsage()
  DEFINE i, j INT
  DISPLAY "fglpkg - Genero BDL Package Manager\n"
  DISPLAY "USAGE:"
  DISPLAY "  fglpkg <command> [arguments]\n"
  DISPLAY "COMMANDS:"
  VAR cmds = commands()
  FOR i = 1 TO cmds.getLength()
    VAR name = cmds[i].name
    IF cmds[i].args.getLength() > 0 THEN
      LET name = SFMT("%1 %2", name, cmds[i].args)
    END IF
    VAR listing = fglpkgutils.concat(cmds[i].summary, cmds[i].listDetail)
    VAR lines = fglpkgutils.splitOnChar(listing, "\n")
    DISPLAY SFMT("  %1%2", fglpkgutils.padRight(name, 18), lines[1])
    FOR j = 2 TO lines.getLength()
      DISPLAY SFMT("  %1%2", fglpkgutils.padRight("", 18), lines[j])
    END FOR
  END FOR
  DISPLAY "\nRun 'fglpkg <command> --help' for command-specific options.\n"
  DISPLAY "ENVIRONMENT:"
  DISPLAY "  FGLPKG_HOME              Override ~/.fglpkg"
  DISPLAY "  FGLPKG_REGISTRY          Registry URL for install/search/audit/whoami/publish."
  DISPLAY "                           Default: https://service.generointelligence.ai"
  DISPLAY "  FGLPKG_TOKEN             Bearer token for the registry (overrides stored OAuth)"
  DISPLAY "  FGLPKG_GENERO_VERSION    Override Genero version detection"
  DISPLAY "  FGLPKG_INSTALL_CONCURRENCY  Cap parallel downloads during install (default 4)"
  DISPLAY ""
  IF fglpkgutils.isWin() THEN
    DISPLAY "SETUP:"
    DISPLAY "  PowerShell:    fglpkg env --global | Invoke-Expression"
    DISPLAY '  Command Prompt: run "fglpkg env --global" and set the displayed variables'
  ELSE
    DISPLAY "SETUP:"
    DISPLAY '  Add to ~/.bashrc:  eval "$(fglpkg env --global)"'
  END IF
  DISPLAY ""
END FUNCTION

--─── registry data ──────────────────────────────────────────────────────────

PRIVATE FUNCTION addCommand(
    name STRING, aliasesCSV STRING, summary STRING, listDetail STRING,
    args STRING, usage STRING, long STRING, passthrough BOOLEAN)
  DEFINE c TCommand
  LET c.name = name
  IF aliasesCSV.getLength() > 0 THEN
    LET c.aliases = fglpkgutils.splitOnChar(aliasesCSV, ",")
  END IF
  LET c.summary = summary
  LET c.listDetail = listDetail
  LET c.args = args
  LET c.usage = usage
  LET c.long = long
  LET c.passthrough = passthrough
  LET _commands[_commands.getLength() + 1] = c
END FUNCTION

PRIVATE FUNCTION initCommands()
  CALL addCommand("init", "",
      "Create a new fglpkg.json",
      " (--template <library|app> to scaffold)",
      "",
      "fglpkg init [--template <library|app>]",
      "FLAGS:\n"
      || "  --template, -t <name>    Scaffold from a built-in template (library|app)\n"
      || "\n"
      || "Prompts for name, version, description, and author, then writes fglpkg.json.\n",
      FALSE)
  CALL addCommand("install", "",
      "Install all dependencies (or add specific packages)", "",
      "[pkg...]",
      "fglpkg install [package[@version]...] [flags]",
      "FLAGS:\n"
      || "  --local, -l              Force local project directory (.fglpkg/)\n"
      || "  --global, -g             Force global home directory (~/.fglpkg/)\n"
      || "  --force, -f              Delete fglpkg.lock and .fglpkg/ first, then\n"
      || "                           re-download every package (local installs only)\n"
      || '  --save-dev, -D           Record added packages under "devDependencies"\n'
      || '  --save-optional, -O      Record added packages under "optionalDependencies"\n'
      || '  --save-prod, -P          Record added packages under "dependencies" (default)\n'
      || "  --production, --prod     Skip devDependencies when installing\n"
      || "\n"
      || "With no package arguments, installs everything declared in fglpkg.json.\n"
      || "With one or more <package>[@<version>] arguments, resolves and adds them.\n"
      || "Without --local/--global, the target is auto-detected: local when a\n"
      || ".fglpkg/ directory or fglpkg.json exists in the current directory.\n",
      FALSE)
  CALL addCommand("remove", "",
      "Remove a package", "",
      "<pkg>",
      "fglpkg remove <package>... [--local|--global]",
      "FLAGS:\n"
      || "  --local, -l              Force local project directory (.fglpkg/)\n"
      || "  --global, -g             Force global home directory (~/.fglpkg/)\n",
      FALSE)
  CALL addCommand("update", "",
      "Re-resolve and update all dependencies", "",
      "",
      "fglpkg update [--local|--global]",
      "FLAGS:\n"
      || "  --local, -l              Force local project directory (.fglpkg/)\n"
      || "  --global, -g             Force global home directory (~/.fglpkg/)\n"
      || "\n"
      || "Ignores fglpkg.lock and re-resolves every dependency to the newest version\n"
      || "allowed by the manifest constraints.\n",
      FALSE)
  CALL addCommand("list", "",
      "List installed packages", "",
      "",
      "fglpkg list [--local|--global]",
      "FLAGS:\n"
      || "  --local, -l              Force local project directory (.fglpkg/)\n"
      || "  --global, -g             Force global home directory (~/.fglpkg/)\n",
      FALSE)
  CALL addCommand("env", "",
      "Print environment variable exports", "",
      "",
      "fglpkg env [--local|--global|--gst|--gwa]",
      "FLAGS:\n"
      || "  --local, -l              Force local project directory (.fglpkg/)\n"
      || "  --global, -g             Force global home directory (~/.fglpkg/)\n"
      || "  --gst                    Output in Genero Studio format (implies --local)\n"
      || "  --gwa                    Emit --webcomponent flags for gwabuildtool, one\n"
      || "                           per installed COMPONENTTYPE\n"
      || "\n"
      || "Prints shell export lines. Evaluate the output to load them, e.g.\n"
      || '  eval "$(fglpkg env --global)"\n',
      FALSE)
  CALL addCommand("search", "",
      "Search the registry",
      " (use --all to list every package)",
      "<term>",
      "fglpkg search <term>\nfglpkg search --all",
      "FLAGS:\n"
      || "  --all                    List every package in the registry (no term)\n",
      FALSE)
  CALL addCommand("info", "view",
      "Show registry metadata for a package",
      " (--json for raw output)",
      "<pkg>[@ver]",
      "fglpkg info <package>[@<version>] [--json]",
      "FLAGS:\n"
      || "  --json                   Emit raw PackageInfo JSON instead of a summary\n",
      FALSE)
  CALL addCommand("outdated", "",
      "Show FGL deps with newer versions available",
      " (--json for JSON)",
      "",
      "fglpkg outdated [--json]",
      "FLAGS:\n"
      || "  --json                   Emit a JSON array instead of a table\n"
      || "\n"
      || "Exits non-zero when any dependency is outdated, for use as a CI gate.\n"
      || "Java dependencies are not checked (they use exact version pins).\n",
      FALSE)
  CALL addCommand("audit", "",
      "Check installed Java JARs for known vulnerabilities",
      "\n(--json, --severity=<level>, --production)",
      "",
      "fglpkg audit [flags]",
      "FLAGS:\n"
      || "  --json                          Emit a JSON report on stdout\n"
      || "  --severity=<low|medium|high|critical>\n"
      || "                                  Minimum severity that fails the build (default: medium)\n"
      || "  --production, --prod            Skip dev-scoped JARs\n"
      || "  --offline                       Reserved for a future cached-advisory mode (errors today)\n"
      || "\n"
      || "EXIT CODES:\n"
      || "  0  no findings at or above --severity\n"
      || "  1  one or more findings at or above --severity\n"
      || "  2  audit itself failed (missing lockfile, network error, etc.)\n",
      FALSE)
  CALL addCommand("sbom", "",
      "Emit a CycloneDX SBOM for the project from fglpkg.lock",
      "\n(-o file, --pretty, --production)",
      "",
      "fglpkg sbom [flags]",
      "FLAGS:\n"
      || "  -o, --output <path>             Write to file instead of stdout\n"
      || "  --pretty                        Indented JSON (default: compact)\n"
      || "  --production, --prod            Skip dev-scoped JARs\n"
      || "  --format=<cyclonedx|spdx>       Output format. Default: cyclonedx\n",
      FALSE)
  CALL addCommand("completion", "",
      "Print shell completion script",
      " (bash|zsh|fish|powershell)",
      "<sh>",
      "fglpkg completion <bash|zsh|fish|powershell>",
      "Install (bash):  fglpkg completion bash > /etc/bash_completion.d/fglpkg\n"
      || "Or source:       source <(fglpkg completion bash)\n",
      FALSE)
  CALL addCommand("bdl", "",
      "Run a BDL program from an installed package", "",
      "<pkg> <mod>",
      "fglpkg bdl <package> <module> [args...]\nfglpkg bdl --list",
      "FLAGS:\n"
      || "  --list, -l               List BDL programs across installed packages\n"
      || "\n"
      || 'Runs a program declared in an installed package\'s "programs" list via fglrun.\n'
      || "Arguments after the module name are passed to the program unchanged.\n",
      TRUE)
  CALL addCommand("publish", "",
      "Publish current package to the registry; submits for admin review",
      "\n(--dry-run prints what would happen without calling out;\n"
      || " --ci for non-interactive pipelines: requires FGLPKG_TOKEN,\n"
      || " prints a machine-readable status line)",
      "",
      "fglpkg publish [--dry-run] [--ci] [--private|--public]",
      "FLAGS:\n"
      || "  --dry-run, -n            Print what would happen without any network calls\n"
      || "  --ci                     Non-interactive mode for pipelines: requires\n"
      || "                           FGLPKG_TOKEN and prints a machine-readable status line\n"
      || "  --private                Mark the package private on first publish\n"
      || "  --public                 Mark the package public on first publish (default)\n",
      FALSE)
  CALL addCommand("pack", "",
      "Build the publishable zip locally without uploading",
      "\n(--list prints contents without writing a file)",
      "[-o file]",
      "fglpkg pack [-o <file>] [--list]",
      "FLAGS:\n"
      || "  -o, --output <file>      Write the zip to <file>\n"
      || "  --list, -l               Print the zip contents and metadata without writing\n"
      || "\n"
      || "Builds the same zip 'fglpkg publish' would upload, for local inspection.\n",
      FALSE)
  CALL addCommand("login", "",
      "Sign in to the registry",
      " (OAuth browser flow, or --token <PAT>)",
      "",
      "fglpkg login [--token <PAT>]",
      "FLAGS:\n"
      || "  --token <PAT>            Store a Personal Access Token instead of the\n"
      || "                           browser OAuth flow (for CI / non-interactive use)\n",
      FALSE)
  CALL addCommand("logout", "",
      "Remove saved credentials", "",
      "",
      "fglpkg logout",
      "Removes the saved credentials for the active registry.\n",
      FALSE)
  CALL addCommand("whoami", "",
      "Show current authenticated user", "",
      "",
      "fglpkg whoami",
      "Shows the authenticated user, partner, and scopes for the active registry.\n",
      FALSE)
  CALL addCommand("workspace", "ws",
      "Manage monorepo workspaces", "",
      "",
      "fglpkg workspace <init|add|list|info>",
      "SUBCOMMANDS:\n"
      || "  init [members...]        Create fglpkg-workspace.json in the current directory\n"
      || "  add <path>               Add a member project to the workspace\n"
      || "  list                     List workspace members\n"
      || "  info                     Print a workspace summary\n",
      FALSE)
  CALL addCommand("run", "",
      "Run a script from an installed package", "",
      "<command>",
      "fglpkg run <command> [-- args...]\nfglpkg run --list",
      "FLAGS:\n"
      || "  --list, -l               List commands defined by installed packages\n"
      || "\n"
      || 'Runs a "bin" command from an installed package. Arguments after \'--\' (or\n'
      || "after the command name) are passed to the script unchanged.\n",
      TRUE)
  CALL addCommand("docs", "",
      "List or view package documentation", "",
      "<package>",
      "fglpkg docs <package> [file]",
      "With only a package name, lists its documentation files (or prints the doc\n"
      || "directly when the package declares exactly one). Pass a file name to print a\n"
      || "specific doc.\n",
      FALSE)
  CALL addCommand("version", "",
      "Print fglpkg version, or bump package version",
      "\n(bump = patch|minor|major|prerelease|<semver>, add --git to tag)",
      "[bump]",
      "fglpkg version [<patch|minor|major|prerelease|semver>] [--git]",
      "FLAGS:\n"
      || "  --git                    Stage, commit, and tag the new version\n"
      || "\n"
      || "With no arguments, prints the fglpkg tool version. With a bump kind\n"
      || "(patch|minor|major|prerelease) or an explicit semver, updates fglpkg.json.\n",
      FALSE)
  CALL addCommand("help", "",
      "Show this help", "",
      "",
      "fglpkg help [command]",
      "",
      FALSE)
END FUNCTION
