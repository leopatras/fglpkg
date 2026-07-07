package cli

import (
	"fmt"
	"strings"
)

// command is one entry in the CLI help/usage registry. It is the single
// source of truth for a command's user-facing documentation: the top-level
// `printUsage` listing, per-command `--help` output, and the shell-completion
// command list all read from here.
//
// It deliberately does NOT own dispatch — the switch in Execute still routes
// each command to its handler. The registry only describes commands; keeping
// the two in sync is covered by TestRegistryMatchesDispatch.
type command struct {
	Name       string   // canonical command name (matches the dispatch switch)
	Aliases    []string // alternate names accepted by the dispatch switch
	Summary    string   // core one-line description, shown both in the top-level list and as the --help header
	ListDetail string   // extra appended to Summary in the top-level list ONLY (never in the command's own --help header). Include the leading separator: a space to stay on the same line, or a newline to wrap onto a hang-indented continuation line.
	Args       string   // compact positional-argument hint for the top-level list (e.g. "[pkg...]", "<pkg>"); "" when none
	Usage      string   // synopsis line(s); rendered under "USAGE:" (no "fglpkg " prefix needed — it's included)
	Long       string   // optional detailed body (flags, notes, examples), shown by --help

	// Passthrough marks commands that forward trailing arguments to a child
	// process (run, bdl). For these, -h/--help is only treated as a request
	// for fglpkg's help when it is the FIRST argument; otherwise it belongs
	// to the invoked program and must be passed through untouched.
	Passthrough bool
}

// commands is the ordered command registry. Order controls the top-level
// `printUsage` listing, so keep it grouped logically rather than alphabetical.
var commands = []command{
	{
		Name:       "init",
		Summary:    "Create a new fglpkg.json",
		ListDetail: " (--template <library|app> to scaffold)",
		Usage:      "fglpkg init [--template <library|app>]",
		Long: `FLAGS:
  --template, -t <name>    Scaffold from a built-in template (library|app)

Prompts for name, version, description, and author, then writes fglpkg.json.
`,
	},
	{
		Name:    "install",
		Summary: "Install all dependencies (or add specific packages)",
		Args:    "[pkg...]",
		Usage:   "fglpkg install [package[@version]...] [flags]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
  --force, -f              Delete fglpkg.lock and .fglpkg/ first, then
                           re-download every package (local installs only)
  --save-dev, -D           Record added packages under "devDependencies"
  --save-optional, -O      Record added packages under "optionalDependencies"
  --save-prod, -P          Record added packages under "dependencies" (default)
  --production, --prod     Skip devDependencies when installing

With no package arguments, installs everything declared in fglpkg.json.
With one or more <package>[@<version>] arguments, resolves and adds them.
Without --local/--global, the target is auto-detected: local when a
.fglpkg/ directory or fglpkg.json exists in the current directory.
`,
	},
	{
		Name:    "remove",
		Summary: "Remove a package",
		Args:    "<pkg>",
		Usage:   "fglpkg remove <package>... [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
`,
	},
	{
		Name:    "update",
		Summary: "Re-resolve and update all dependencies",
		Usage:   "fglpkg update [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)

Ignores fglpkg.lock and re-resolves every dependency to the newest version
allowed by the manifest constraints.
`,
	},
	{
		Name:    "list",
		Summary: "List installed packages",
		Usage:   "fglpkg list [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
`,
	},
	{
		Name:    "env",
		Summary: "Print environment variable exports",
		Usage:   "fglpkg env [--local|--global|--gst|--gwa]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
  --gst                    Output in Genero Studio format (implies --local)
  --gwa                    Emit --webcomponent flags for gwabuildtool, one
                           per installed COMPONENTTYPE

Prints shell export lines. Evaluate the output to load them, e.g.
  eval "$(fglpkg env --global)"
`,
	},
	{
		Name:       "search",
		Summary:    "Search the registry",
		ListDetail: " (use --all to list every package)",
		Args:       "<term>",
		Usage:      "fglpkg search <term>\nfglpkg search --all",
		Long: `FLAGS:
  --all                    List every package in the registry (no term)
`,
	},
	{
		Name:       "info",
		Aliases:    []string{"view"},
		Summary:    "Show registry metadata for a package",
		ListDetail: " (--json for raw output)",
		Args:       "<pkg>[@ver]",
		Usage:      "fglpkg info <package>[@<version>] [--json]",
		Long: `FLAGS:
  --json                   Emit raw PackageInfo JSON instead of a summary
`,
	},
	{
		Name:       "outdated",
		Summary:    "Show FGL deps with newer versions available",
		ListDetail: " (--json for JSON)",
		Usage:      "fglpkg outdated [--json]",
		Long: `FLAGS:
  --json                   Emit a JSON array instead of a table

Exits non-zero when any dependency is outdated, for use as a CI gate.
Java dependencies are not checked (they use exact version pins).
`,
	},
	{
		Name:       "audit",
		Summary:    "Check installed Java JARs for known vulnerabilities",
		ListDetail: "\n(--json, --severity=<level>, --production)",
		Usage:      "fglpkg audit [flags]",
		Long: `FLAGS:
  --json                          Emit a JSON report on stdout
  --severity=<low|medium|high|critical>
                                  Minimum severity that fails the build (default: medium)
  --production, --prod            Skip dev-scoped JARs
  --offline                       Reserved for a future cached-advisory mode (errors today)

EXIT CODES:
  0  no findings at or above --severity
  1  one or more findings at or above --severity
  2  audit itself failed (missing lockfile, network error, etc.)

NOTES:
  Java JARs are audited against the OSV.dev v1 API (anonymous, free).
  BDL packages are not scanned in this version (no public advisory feed).
`,
	},
	{
		Name:       "sbom",
		Summary:    "Emit a CycloneDX SBOM for the project from fglpkg.lock",
		ListDetail: "\n(-o file, --pretty, --production)",
		Usage:      "fglpkg sbom [flags]",
		Long: `FLAGS:
  -o, --output <path>             Write to file instead of stdout
  --pretty                        Indented JSON (default: compact)
  --production, --prod            Skip dev-scoped JARs
  --format=<cyclonedx|spdx>       Output format. Default: cyclonedx
                                  (spdx is reserved for a future release)

NOTES:
  v1 emits CycloneDX 1.5 JSON, generated from fglpkg.lock. No network
  calls — output is deterministic given the lockfile.
`,
	},
	{
		Name:       "completion",
		Summary:    "Print shell completion script",
		ListDetail: " (bash|zsh|fish|powershell)",
		Args:       "<sh>",
		Usage:      "fglpkg completion <bash|zsh|fish|powershell>",
		Long: `Install (bash):  fglpkg completion bash > /etc/bash_completion.d/fglpkg
Or source:       source <(fglpkg completion bash)
`,
	},
	{
		Name:        "bdl",
		Summary:     "Run a BDL program from an installed package",
		Args:        "<pkg> <mod>",
		Usage:       "fglpkg bdl <package> <module> [args...]\nfglpkg bdl --list",
		Passthrough: true,
		Long: `FLAGS:
  --list, -l               List BDL programs across installed packages

Runs a program declared in an installed package's "programs" list via fglrun.
Arguments after the module name are passed to the program unchanged.
`,
	},
	{
		Name:    "publish",
		Summary: "Publish current package to the registry; submits for admin review",
		ListDetail: "\n(--dry-run prints what would happen without calling out;\n" +
			" --ci for non-interactive pipelines: requires FGLPKG_TOKEN,\n" +
			" prints a machine-readable status line)",
		Usage: "fglpkg publish [--dry-run] [--ci] [--private|--public]",
		Long: `FLAGS:
  --dry-run, -n            Print what would happen without any network calls
  --ci                     Non-interactive mode for pipelines: requires
                           FGLPKG_TOKEN and prints a machine-readable status line
  --private                Mark the package private on first publish
  --public                 Mark the package public on first publish (default)

Builds the package zip, uploads it, and submits the version for admin review.
`,
	},
	{
		Name:       "pack",
		Summary:    "Build the publishable zip locally without uploading",
		ListDetail: "\n(--list prints contents without writing a file)",
		Args:       "[-o file]",
		Usage:      "fglpkg pack [-o <file>] [--list]",
		Long: `FLAGS:
  -o, --output <file>      Write the zip to <file>
  --list, -l               Print the zip contents and metadata without writing

Builds the same zip 'fglpkg publish' would upload, for local inspection.
`,
	},
	{
		Name:       "login",
		Summary:    "Sign in to the registry",
		ListDetail: " (OAuth browser flow, or --token <PAT>)",
		Usage:      "fglpkg login [--token <PAT>]",
		Long: `FLAGS:
  --token <PAT>            Store a Personal Access Token instead of the
                           browser OAuth flow (for CI / non-interactive use)

With no flags, opens a browser to complete an OAuth (code + PKCE) login.
`,
	},
	{
		Name:    "logout",
		Summary: "Remove saved credentials",
		Usage:   "fglpkg logout",
		Long:    "Removes the saved credentials for the active registry.\n",
	},
	{
		Name:    "whoami",
		Summary: "Show current authenticated user",
		Usage:   "fglpkg whoami",
		Long:    "Shows the authenticated user, partner, and scopes for the active registry.\n",
	},
	{
		Name:    "workspace",
		Aliases: []string{"ws"},
		Summary: "Manage monorepo workspaces",
		Usage:   "fglpkg workspace <init|add|list|info>",
		Long: `SUBCOMMANDS:
  init [members...]        Create fglpkg-workspace.json in the current directory
  add <path>               Add a member project to the workspace
  list                     List workspace members
  info                     Print a workspace summary
`,
	},
	{
		Name:        "run",
		Summary:     "Run a script from an installed package",
		Args:        "<command>",
		Usage:       "fglpkg run <command> [-- args...]\nfglpkg run --list",
		Passthrough: true,
		Long: `FLAGS:
  --list, -l               List commands defined by installed packages

Runs a "bin" command from an installed package. Arguments after '--' (or
after the command name) are passed to the script unchanged.
`,
	},
	{
		Name:    "docs",
		Summary: "List or view package documentation",
		Args:    "<package>",
		Usage:   "fglpkg docs <package> [file]",
		Long: `With only a package name, lists its documentation files (or prints the doc
directly when the package declares exactly one). Pass a file name to print a
specific doc.
`,
	},
	{
		Name:       "version",
		Summary:    "Print fglpkg version, or bump package version",
		ListDetail: "\n(bump = patch|minor|major|prerelease|<semver>, add --git to tag)",
		Args:       "[bump]",
		Usage:      "fglpkg version [<patch|minor|major|prerelease|semver>] [--git]",
		Long: `FLAGS:
  --git                    Stage, commit, and tag the new version

With no arguments, prints the fglpkg tool version. With a bump kind
(patch|minor|major|prerelease) or an explicit semver, updates fglpkg.json.
`,
	},
	{
		Name:    "help",
		Summary: "Show this help",
		Usage:   "fglpkg help [command]",
	},
}

// commandIndex maps every command name and alias to its registry entry.
// Built once at package init from the ordered commands slice.
var commandIndex = func() map[string]*command {
	idx := make(map[string]*command, len(commands))
	for i := range commands {
		c := &commands[i]
		idx[c.Name] = c
		for _, a := range c.Aliases {
			idx[a] = c
		}
	}
	return idx
}()

// isHelpFlag reports whether an argument is a help request.
func isHelpFlag(a string) bool {
	return a == "-h" || a == "--help"
}

// helpRequested reports whether args ask for this command's help. For
// passthrough commands (run, bdl) only the first argument is considered, so
// help flags meant for the invoked program are forwarded untouched.
func (c *command) helpRequested(args []string) bool {
	if c.Passthrough {
		return len(args) > 0 && isHelpFlag(args[0])
	}
	for _, a := range args {
		if isHelpFlag(a) {
			return true
		}
	}
	return false
}

// printCommandHelp renders a single command's help page: a header, its usage
// synopsis, and (when present) the detailed body.
func printCommandHelp(c *command) {
	// The header shows Summary only; ListDetail (parenthetical flag/arg hints)
	// is list-only and would duplicate the USAGE/FLAGS sections below.
	fmt.Printf("fglpkg %s - %s\n\n", c.Name, c.Summary)
	if len(c.Aliases) > 0 {
		fmt.Printf("ALIASES:\n  %s\n\n", strings.Join(c.Aliases, ", "))
	}
	fmt.Println("USAGE:")
	for _, line := range strings.Split(strings.TrimRight(c.Usage, "\n"), "\n") {
		fmt.Printf("  %s\n", line)
	}
	if c.Long != "" {
		fmt.Printf("\n%s", c.Long)
	}
}
