package cli

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
	"github.com/4js-mikefolcher/fglpkg/internal/env"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/hooks"
	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/oauth"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/selfupdate"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
	"github.com/4js-mikefolcher/fglpkg/internal/updatecheck"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// init wires registry.Bearer / TryRefresh to the credentials package's
// consumer-side resolver so OAuth refresh, stored PAT, and env-var override
// all flow through the registry HTTP client transparently.
func init() {
	registry.Bearer = func() string {
		home, err := fglpkgHome()
		if err != nil {
			return credentials.ConsumerEnvBearer()
		}
		tok, _ := credentials.ActiveBearer(context.Background(), home, defaultRegistry(), oauth.Refresh)
		return tok
	}
	registry.TryRefresh = func() bool {
		home, err := fglpkgHome()
		if err != nil {
			return false
		}
		return credentials.ForceRefresh(context.Background(), home, defaultRegistry(), oauth.Refresh)
	}
}

// Version and Build are set at compile time via -ldflags.
var (
	Version = "dev"
	Build   = "unknown"
)

// reader is a package-level buffered stdin reader shared across all prompts
// so buffered input is never lost between successive promptWithDefault calls.
var reader = bufio.NewReader(os.Stdin)

// privateHint appends a login suggestion to an ErrNotFound when the user has
// no bearer token. Private packages return 404 indistinguishably from missing
// packages; we can only hint when auth was never attempted.
//
// reg is the registry the package is routed to. When it names a secondary
// repository (non-empty and not GI), the hint targets that repository's
// credentials (`fglpkg login --registry <name>`) rather than the GI login,
// which would be the wrong remedy for an Artifactory-routed package.
func privateHint(err error, pkg, reg string) error {
	if !errors.Is(err, registry.ErrNotFound) || registry.Bearer() != "" {
		return err
	}
	if reg != "" && reg != config.GIName {
		return fmt.Errorf("%w\n  hint: if %q is private, run: fglpkg login --registry %s", err, pkg, reg)
	}
	return fmt.Errorf("%w\n  hint: if %q is a private package, run: fglpkg login", err, pkg)
}

// pinnedRegistry returns the registry a package is pinned to via a manifest
// `{"version": ..., "registry": ...}` dependency entry, or "" if unpinned (or
// m is nil). Used to point the not-found hint at the right credentials.
func pinnedRegistry(m *manifest.Manifest, name string) string {
	if m == nil {
		return ""
	}
	return collectFGLPins(m)[slugutil.Canonical(name)]
}

// isValidPackageSlug reports whether a string is a well-formed canonical
// package slug (2-64 chars; lowercase letters, digits, hyphens; start/end
// alphanumeric). It delegates to internal/slug, the single source of truth for
// the slug rule (GIS-271).
func isValidPackageSlug(slug string) bool {
	return slugutil.IsValid(slug)
}

// Execute is the main CLI entry point.
func Execute() error {
	if len(os.Args) < 2 {
		printUsage()
		return nil
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// `fglpkg help [command]` and the top-level -h/--help both show usage;
	// with a command argument, `help` shows that command's page.
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		if len(args) > 0 {
			if c := commandIndex[args[0]]; c != nil {
				printCommandHelp(c)
				return nil
			}
		}
		printUsage()
		return nil
	}

	// Per-command help: `fglpkg <command> --help` / `-h`. Handled here, before
	// the dispatch switch, so every command gets consistent help without each
	// handler re-implementing it. Passthrough commands (run, bdl) only treat a
	// leading help flag as ours; the rest is forwarded to the invoked program.
	if c := commandIndex[cmd]; c != nil && c.helpRequested(args) {
		printCommandHelp(c)
		return nil
	}

	// Passive update-check (GIS-255): kicked off in the background now; any
	// notice prints after the command finishes. Never blocks, never changes the
	// exit code, stays silent on error.
	pending := startUpdateCheck(cmd)
	err := func() error {
		switch cmd {
		case "init":
			return cmdInit(args)
		case "install":
			return cmdInstall(args)
		case "remove":
			return cmdRemove(args)
		case "update":
			return cmdUpdate(args)
		case "list":
			return cmdList(args)
		case "env":
			return cmdEnv(args)
		case "search":
			return cmdSearch(args)
		case "info", "view":
			return cmdInfo(args)
		case "outdated":
			return cmdOutdated(args)
		case "audit":
			return cmdAudit(args)
		case "sbom":
			return cmdSbom(args)
		case "completion":
			return cmdCompletion(args)
		case "publish":
			return cmdPublish(args)
		case "deprecate":
			return cmdDeprecate(args)
		case "pack":
			return cmdPack(args)
		case "login":
			return cmdLogin(args)
		case "logout":
			return cmdLogout(args)
		case "whoami":
			return cmdWhoami(args)
		case "workspace", "ws":
			return cmdWorkspace(args)
		case "registry":
			return cmdRegistry(args)
		case "run":
			return cmdRun(args)
		case "bdl":
			return cmdBdl(args)
		case "docs":
			return cmdDocs(args)
		case "version":
			return cmdVersion(args)
		case "self-update", "upgrade":
			return cmdSelfUpdate(args)
		case "help", "--help", "-h":
			printUsage()
			return nil
		default:
			return fmt.Errorf("unknown command: %q\nRun 'fglpkg help' for usage", cmd)
		}
	}()
	pending.Finish(os.Stderr, semverNewer)
	return err
}

// startUpdateCheck starts the passive "a new version is available" check for
// this invocation (GIS-255). It returns nil when the check should not run; a
// nil *Pending is safe to Finish.
func startUpdateCheck(cmd string) *updatecheck.Pending {
	home, err := fglpkgHome()
	if err != nil {
		return nil
	}
	settings, _ := config.LoadUpdateSettings(home) // usable defaults even on error
	state := updatecheck.LoadState(home)
	env := updatecheck.Env{
		Version:     Version,
		Command:     cmd,
		CI:          os.Getenv("CI") != "",
		NoCheckEnv:  os.Getenv("FGLPKG_NO_UPDATE_CHECK") != "",
		StdoutIsTTY: isTerminal(os.Stdout),
		Enabled:     settings.Enabled,
		Interval:    settings.Interval,
		Now:         time.Now(),
		LastCheck:   state.LastCheck,
	}
	return updatecheck.Start(home, env, state.LatestKnown, fetchLatestVersion)
}

// fetchLatestVersion returns the latest published fglpkg version from the
// registry — the network call behind the passive check.
func fetchLatestVersion() (string, error) {
	lr, err := registry.FetchLatestFGLPkg()
	if err != nil {
		return "", err
	}
	return lr.Version, nil
}

// semverNewer reports whether latest is a newer release than current.
// Unparseable versions (e.g. a "dev" build) are treated as not newer.
func semverNewer(current, latest string) bool {
	c, err1 := semver.Parse(current)
	l, err2 := semver.Parse(latest)
	if err1 != nil || err2 != nil {
		return false
	}
	return l.GreaterThan(c)
}

// isTerminal reports whether f is a character device (an interactive terminal),
// used to keep the update notice out of piped or scripted output.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// cmdSelfUpdate downloads and installs the latest release (GIS-255), verifying
// the Ed25519 release signature and SHA-256 before atomically replacing the
// running binary. See internal/selfupdate.
func cmdSelfUpdate(args []string) error {
	opts := selfupdate.Options{Current: Version, Stdout: os.Stdout}
	for _, a := range args {
		switch a {
		case "--check":
			opts.Check = true
		case "--yes", "-y":
			opts.Yes = true
		case "--force":
			opts.Force = true
		default:
			return fmt.Errorf("unknown flag %q\nUsage: fglpkg self-update [--check] [--yes] [--force]", a)
		}
	}
	opts.Confirm = func(prompt string) bool { return promptYesNo(prompt, true) }
	if home, err := fglpkgHome(); err == nil {
		opts.HomeForCache = home
	}
	return selfupdate.Run(opts)
}

// ─── init ─────────────────────────────────────────────────────────────────────

func cmdInit(args []string) error {
	tmplName, err := parseInitFlags(args)
	if err != nil {
		return err
	}
	var tmpl *projectTemplate
	if tmplName != "" {
		if tmpl = findTemplate(tmplName); tmpl == nil {
			return fmt.Errorf("unknown template %q\nAvailable templates:\n%s", tmplName, templateList())
		}
	}

	if _, err := os.Stat(manifest.Filename); err == nil {
		return fmt.Errorf("%s already exists in the current directory", manifest.Filename)
	}
	name := promptPackageSlug()
	version := promptPackageVersion()
	description := promptNonEmptyString("Description")
	author := promptNonEmptyString("Author")
	m := manifest.New(name, version, description, author)
	if tmpl != nil {
		tmpl.apply(m)
	}
	if err := m.Save("."); err != nil {
		return fmt.Errorf("failed to write %s: %w", manifest.Filename, err)
	}
	fmt.Printf("✓ Created %s\n", manifest.Filename)
	if tmpl != nil {
		if err := tmpl.writeFiles(".", name); err != nil {
			return err
		}
	}
	return nil
}

// parseInitFlags extracts the optional --template/-t value from `init` args.
// Returns "" when no template was requested.
func parseInitFlags(args []string) (string, error) {
	tmpl := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--template" || a == "-t":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a template name\nAvailable templates:\n%s", a, templateList())
			}
			i++
			tmpl = args[i]
		case strings.HasPrefix(a, "--template="):
			tmpl = strings.TrimPrefix(a, "--template=")
		default:
			return "", fmt.Errorf("unexpected argument %q\nUsage: fglpkg init [--template <name>]", a)
		}
	}
	return tmpl, nil
}

// ─── install ──────────────────────────────────────────────────────────────────

func cmdInstall(args []string) error {
	flags, err := parseInstallFlags(args)
	if err != nil {
		return err
	}
	// On `install`, --registry pins the named package's source repository, so a
	// package argument is required. (`update --registry` restricts re-resolution
	// to one repo and needs no package — see cmdUpdate.)
	if flags.registry != "" && len(flags.pkgs) == 0 {
		return fmt.Errorf("--registry requires a package to install (it pins that package's source repository)")
	}

	// `fglpkg install <pkg>` in a directory that isn't yet a project (no
	// .fglpkg/, no fglpkg.json) is treated as local: the add-package branch
	// will call manifest.LoadOrNew(".") which writes fglpkg.json HERE, so
	// the directory IS becoming a project. Without this, the package would
	// install globally while the manifest landed locally — silent
	// inconsistency that bit Laurent in SUPNA-10506.
	addingToNewProject := installImpliesNewProject(flags, isProjectDir())
	forceLocal := flags.local || addingToNewProject

	home, isLocal, err := resolveHome(forceLocal, flags.global)
	if err != nil {
		return err
	}
	// Best-effort manifest load so any configured registries drive resolution.
	// The authoritative load happens later per install path.
	regManifest, _ := manifest.Load(".")
	inst := newInstaller(home, regManifest)
	if flags.noVerifySignature {
		globalHome, _ := fglpkgHome()
		inst.WithSigning(signing.EnforceOff, globalHome, defaultRegistry())
	}
	projectDir, _ := os.Getwd()

	if isLocal {
		fmt.Println("Installing to local project directory (.fglpkg/)")
		fmt.Println("  Tip: add .fglpkg/ to your .gitignore file")
		if addingToNewProject {
			fmt.Println("  Note: no fglpkg.json found — initialising the current directory as a new project.")
		}
	}

	if flags.force {
		if !isLocal {
			return fmt.Errorf("--force is only supported for local installs; re-run inside a project directory or with --local")
		}
		if err := resetLocalInstall(projectDir, inst); err != nil {
			return err
		}
	}

	instOpts := installer.Options{Production: flags.production, NoManifestFallback: flags.noManifestFallback}

	if len(flags.pkgs) == 0 {
		m, err := manifest.Load(".")
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no %s in current directory — run 'fglpkg init' first", manifest.Filename)
			}
			return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
		}
		if flags.production {
			fmt.Println("Installing in production mode (devDependencies will be skipped)")
		}
		if err := runHook(m, manifest.HookPreInstall, projectDir); err != nil {
			return err
		}
		if err := inst.InstallAllWithOptions(m, projectDir, flags.force, instOpts); err != nil {
			return err
		}
		return runHook(m, manifest.HookPostInstall, projectDir)
	}

	m, err := manifest.LoadOrNew(".")
	if err != nil {
		return err
	}
	gv, err := genero.Detect()
	if err != nil {
		return fmt.Errorf("cannot detect Genero version: %w", err)
	}
	generoMajor := gv.MajorString()

	// Engage multi-provider routing for the add-package resolve when repositories
	// beyond the built-in GI registry are configured; otherwise fall back to the
	// GI-only client. Without this, `install <pkg>` could only ever add packages
	// from GI even though search/install-from-lock route to secondary repos.
	globalHome, err := fglpkgHome()
	if err != nil {
		globalHome = home
	}
	rs, _, _, rsErr := buildRepositorySet(globalHome, m)
	if rsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring registries config: %v\n", rsErr)
	}

	// --registry <name> disambiguates a package available from more than one
	// repository: it restricts this resolve to the named repo and pins that
	// choice in the manifest so subsequent installs stay deterministic.
	if flags.registry != "" {
		if rs == nil {
			// Only the built-in GI registry is configured. --registry gi is a
			// harmless no-op; any other name cannot be honoured.
			if flags.registry != config.GIName {
				return fmt.Errorf("--registry %q: no repository named %q is configured (add it to fglpkg.json or ~/.fglpkg/config.json)", flags.registry, flags.registry)
			}
		} else {
			rs.Restrict(flags.registry)
		}
	}

	scopeLabel := scopeDisplayName(flags.scope)
	for _, pkg := range flags.pkgs {
		name, version, err := parsePackageArg(pkg)
		if err != nil {
			return err
		}
		fmt.Printf("Resolving %s@%s (Genero %s)...\n", name, version, gv)
		var info *registry.PackageInfo
		if rs != nil {
			info, err = rs.Resolve(name, version, generoMajor)
		} else {
			info, err = registry.Resolve(name, version, generoMajor)
		}
		if err != nil {
			// Prefer the explicit --registry target, else the manifest pin, so
			// the not-found hint points at the credentials that actually matter.
			hintReg := flags.registry
			if hintReg == "" {
				hintReg = pinnedRegistry(m, name)
			}
			return fmt.Errorf("failed to resolve %s@%s: %w", name, version, privateHint(err, name, hintReg))
		}
		// Older registry server versions omit `name` from the version-info
		// response; fall back to the user-supplied name so we never write an
		// empty key into fglpkg.json.
		if info.Name == "" {
			info.Name = name
		}
		// A package can never depend on itself — the resolver dedups to one
		// version per name, so this would only pull a registry snapshot of this
		// project into its own tree. Reject early with a clear message; the
		// manifest validator enforces the same rule at load/publish time.
		if m.Name != "" && slugutil.Canonical(info.Name) == slugutil.Canonical(m.Name) {
			return fmt.Errorf("cannot add %q: a package cannot depend on itself", info.Name)
		}
		m.AddFGLDependencyPinned(info.Name, info.Version, flags.registry, flags.scope)
		fmt.Printf("✓ Added %s@%s to %s [%s]\n", info.Name, info.Version, manifest.Filename, scopeLabel)
	}
	if err := m.Save("."); err != nil {
		return err
	}
	// Rebuild the installer from the saved manifest so its routing picks up any
	// freshly-written registry pin — otherwise a pinned (collision) package
	// would fail the graph install, which was resolved from the pre-add manifest.
	inst = newInstaller(home, m)
	fmt.Println()
	if err := runHook(m, manifest.HookPreInstall, projectDir); err != nil {
		return err
	}
	if err := inst.InstallAllWithOptions(m, projectDir, true, instOpts); err != nil {
		return err
	}
	return runHook(m, manifest.HookPostInstall, projectDir)
}

// runHook executes any operations declared for event in the project's
// manifest, prefixed with a one-line user-facing log when the hook has
// at least one operation. A failure aborts the surrounding command.
func runHook(m *manifest.Manifest, event manifest.HookEvent, cwd string) error {
	if len(m.Hooks[event]) == 0 {
		return nil
	}
	fmt.Printf("Running %s hook (%d op(s))...\n", event, len(m.Hooks[event]))
	return hooks.Run(m.Hooks, event, cwd)
}

// scopeDisplayName returns a short user-facing label for a manifest.Scope.
func scopeDisplayName(s manifest.Scope) string {
	switch s {
	case manifest.ScopeDev:
		return "devDependencies"
	case manifest.ScopeOptional:
		return "optionalDependencies"
	default:
		return "dependencies"
	}
}

// resolveHome returns the fglpkg home directory based on context:
//   - --local flag: always use .fglpkg/ in the current directory
//   - --global flag: always use ~/.fglpkg/
//   - no flag (default): use .fglpkg/ if a local .fglpkg/ directory or
//     fglpkg.json exists in the current directory, otherwise ~/.fglpkg/
//
// Returns the home path and whether it is local.
func resolveHome(forceLocal, forceGlobal bool) (home string, isLocal bool, err error) {
	if forceLocal {
		wd, err := os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("cannot determine working directory: %w", err)
		}
		return filepath.Join(wd, ".fglpkg"), true, nil
	}
	if forceGlobal {
		h, err := fglpkgHome()
		return h, false, err
	}
	// Context-aware: check if we're inside a project.
	if isProjectDir() {
		wd, _ := os.Getwd()
		return filepath.Join(wd, ".fglpkg"), true, nil
	}
	h, err := fglpkgHome()
	return h, false, err
}

// installImpliesNewProject reports whether the install invocation should be
// treated as "create a new project in the current directory" — true when
// the user passed at least one package name, didn't force either scope, and
// the current directory isn't already a project. Pulled out of cmdInstall
// for direct unit testing.
func installImpliesNewProject(f installFlags, currentDirIsProject bool) bool {
	return len(f.pkgs) > 0 && !f.local && !f.global && !currentDirIsProject
}

// isProjectDir returns true if the current directory looks like a project
// (has a .fglpkg/ directory or a fglpkg.json file).
func isProjectDir() bool {
	if _, err := os.Stat(".fglpkg"); err == nil {
		return true
	}
	if _, err := os.Stat(manifest.Filename); err == nil {
		return true
	}
	return false
}

// resetLocalInstall deletes fglpkg.lock and the local package and JAR
// directories so the next install re-downloads everything from the
// registry. Safe to call when nothing exists yet (missing files are
// simply ignored).
func resetLocalInstall(projectDir string, inst *installer.Installer) error {
	lockPath := filepath.Join(projectDir, lockfile.Filename)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove %s: %w", lockfile.Filename, err)
	}
	for _, dir := range []string{inst.PackagesDir(), inst.JarsDir()} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("cannot remove %s: %w", dir, err)
		}
	}
	fmt.Println("Cleared fglpkg.lock and .fglpkg/ — reloading from registry...")
	return nil
}

// parseFlags extracts --local/-l, --global/-g, and --force/-f flags from
// args, returning the remaining args and the flag values. Commands that
// do not use --force may simply ignore the returned value.
// parseFlags parses the shared --local/--global/--force flags and returns the
// non-flag positional args. A token that looks like a flag (leading "-") but is
// neither a shared flag nor one of extraAllowed is rejected as an unknown flag,
// so e.g. `remove --registry x` errors instead of silently deleting a package
// named "x". Callers with their own flags (e.g. env's --gst/--gwa) pass them in
// extraAllowed; those tokens are recognized here and ignored (the caller reads
// them separately).
func parseFlags(args []string, extraAllowed ...string) (remaining []string, local, global, force bool, err error) {
	allowed := make(map[string]bool, len(extraAllowed))
	for _, e := range extraAllowed {
		allowed[e] = true
	}
	for _, a := range args {
		switch a {
		case "--local", "-l":
			local = true
		case "--global", "-g":
			global = true
		case "--force", "-f":
			force = true
		default:
			if allowed[a] {
				continue
			}
			if strings.HasPrefix(a, "-") {
				return nil, false, false, false, fmt.Errorf("unknown flag %q", a)
			}
			remaining = append(remaining, a)
		}
	}
	return
}

// installFlags holds the parsed flags specific to `fglpkg install`, on top of
// the shared local/global/force flags. Scope is one of manifest.ScopeProd
// (default), ScopeDev, or ScopeOptional, reflecting where newly added
// packages should be recorded.
type installFlags struct {
	local              bool
	global             bool
	force              bool
	production         bool
	noManifestFallback bool
	noVerifySignature  bool
	scope              manifest.Scope
	registry           string // --registry <name>: restrict resolution to one repo and pin it
	pkgs               []string
}

// parseInstallFlags extends parseFlags with --save-dev/-D, --save-optional/-O,
// and --production/--prod flags. It rejects conflicting combinations.
func parseInstallFlags(args []string) (installFlags, error) {
	f := installFlags{scope: manifest.ScopeProd}
	devSeen, optSeen := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if val, ok := strings.CutPrefix(a, "--registry="); ok {
			f.registry = val
			continue
		}
		switch a {
		case "--local", "-l":
			f.local = true
		case "--global", "-g":
			f.global = true
		case "--force", "-f":
			f.force = true
		case "--production", "--prod":
			f.production = true
		case "--no-manifest-fallback":
			f.noManifestFallback = true
		case "--no-verify-signature":
			f.noVerifySignature = true
		case "--save-dev", "-D":
			devSeen = true
			f.scope = manifest.ScopeDev
		case "--save-optional", "-O":
			optSeen = true
			f.scope = manifest.ScopeOptional
		case "--save-prod", "-P":
			f.scope = manifest.ScopeProd
		case "--registry":
			if i+1 >= len(args) {
				return f, fmt.Errorf("--registry requires a value")
			}
			i++
			f.registry = args[i]
		default:
			f.pkgs = append(f.pkgs, a)
		}
	}
	if devSeen && optSeen {
		return f, fmt.Errorf("--save-dev and --save-optional are mutually exclusive")
	}
	if f.production && (devSeen || optSeen) {
		return f, fmt.Errorf("--production cannot be combined with --save-dev or --save-optional")
	}
	return f, nil
}

// ─── remove ───────────────────────────────────────────────────────────────────

func cmdRemove(args []string) error {
	pkgArgs, forceLocal, forceGlobal, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(pkgArgs) == 0 {
		return fmt.Errorf("usage: fglpkg remove <package>")
	}
	home, isLocal, err := resolveHome(forceLocal, forceGlobal)
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	projectDir, _ := os.Getwd()
	if err := runHook(m, manifest.HookPreUninstall, projectDir); err != nil {
		return err
	}

	// Update the manifest first. Pruning of installed files (below) is driven
	// by re-resolving the *updated* manifest, so nothing is deleted until the
	// dependency it belongs to is actually gone from the graph.
	for _, pkg := range pkgArgs {
		if scope := m.RemoveFGLDependency(pkg); scope != "" {
			fmt.Printf("✓ Removed %s from %s\n", pkg, scopeDisplayName(scope))
		} else {
			fmt.Printf("✓ Removed %s (not declared in manifest)\n", pkg)
		}
	}
	if err := m.Save("."); err != nil {
		return err
	}

	// Reconcile installed state with the shrunk manifest: rewrite the lock and
	// (for local installs only) prune packages/JARs the graph no longer needs.
	inst := newInstaller(home, m)
	pruned, err := inst.ReconcileAfterRemove(m, projectDir, isLocal)
	if err != nil {
		fmt.Printf("warning: could not re-resolve to prune orphaned dependencies: %v\n", err)
		fmt.Println("  The manifest was updated; run 'fglpkg install' when able to reconcile installed files.")
		// Best-effort so the named packages at least leave disk. Only touch a
		// local home — a global home is shared with other projects.
		if isLocal {
			for _, pkg := range pkgArgs {
				_ = inst.Remove(pkg)
			}
		}
		return nil
	}
	if !isLocal && len(pkgArgs) > 0 {
		fmt.Println("  Note: global packages/JARs are shared across projects and were not pruned from disk.")
	}
	for _, p := range pruned {
		fmt.Printf("  pruned %s\n", p)
	}
	return nil
}

// ─── update ───────────────────────────────────────────────────────────────────

func cmdUpdate(args []string) error {
	flags, err := parseInstallFlags(args)
	if err != nil {
		return err
	}
	home, _, err := resolveHome(flags.local, flags.global)
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	projectDir, _ := os.Getwd()
	fmt.Println("Ignoring lock file and re-resolving all dependencies...")
	instOpts := installer.Options{Production: flags.production, NoManifestFallback: flags.noManifestFallback}
	inst, rs := buildInstaller(home, m)

	// --registry <name> restricts this re-resolution to a single repository
	// (spec §11). It needs no package argument (unlike `install --registry`).
	if flags.registry != "" {
		if rs == nil {
			if flags.registry != config.GIName {
				return fmt.Errorf("--registry %q: no repository named %q is configured (add it to fglpkg.json or ~/.fglpkg/config.json)", flags.registry, flags.registry)
			}
		} else {
			rs.Restrict(flags.registry)
		}
	}
	return inst.InstallAllWithOptions(m, projectDir, true, instOpts)
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdList(args []string) error {
	_, forceLocal, forceGlobal, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	home, _, err := resolveHome(forceLocal, forceGlobal)
	if err != nil {
		return err
	}
	pkgs, err := newInstaller(home, nil).List()
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		fmt.Println("No packages installed.")
		return nil
	}
	fmt.Println("Installed packages:")
	for _, p := range pkgs {
		fmt.Printf("  %-30s %s\n", p.Name, p.Version)
	}
	return nil
}

// ─── env ──────────────────────────────────────────────────────────────────────

func cmdEnv(args []string) error {
	_, forceLocal, forceGlobal, _, err := parseFlags(args, "--gst", "--gwa")
	if err != nil {
		return err
	}
	gst := false
	gwa := false
	for _, a := range args {
		if a == "--gst" {
			gst = true
		}
		if a == "--gwa" {
			gwa = true
		}
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	g := env.New(home)

	// --gwa emits gwabuildtool --webcomponent flags and exits — it's an
	// orthogonal output mode, not an additional export line.
	if gwa {
		flags, err := g.GenerateGWA()
		if err != nil {
			return err
		}
		for _, f := range flags {
			fmt.Println(f)
		}
		return nil
	}

	// Determine if we should use local-only output.
	// --gst always implies local. Otherwise, context-aware: if inside a
	// project directory (has .fglpkg/ or fglpkg.json), default to local.
	useLocal := forceLocal || gst
	if !forceGlobal && !useLocal {
		useLocal = isProjectDir()
	}

	var exports []string
	switch {
	case gst:
		exports, err = g.GenerateGST()
	case useLocal:
		exports, err = g.GenerateLocal()
	default:
		exports, err = g.Generate()
	}
	if err != nil {
		return err
	}
	for _, line := range exports {
		fmt.Println(line)
	}
	return nil
}

// ─── search ───────────────────────────────────────────────────────────────────

// searchDeprecatedStatus returns the value for a search row's STATUS column: ""
// for a live package (leaving the column blank), "deprecated" when there is no
// successor, or "deprecated -> <slug>" when the deprecation records a
// relocation. The status rides in its own column next to the package identity
// rather than being appended to the publisher-authored description, so it stays
// scannable and can't be mistaken for the description text.
func searchDeprecatedStatus(deprecated bool, movedTo string) string {
	if !deprecated {
		return ""
	}
	if movedTo != "" {
		return "deprecated -> " + movedTo
	}
	return "deprecated"
}

func cmdSearch(args []string) error {
	term, all, generoFlag, err := parseSearchArgs(args)
	if err != nil {
		return err
	}

	// Resolve the target Genero version once, up front, for compatibility
	// grading. Search is a discovery command that must run anywhere, so a
	// failed detection is not fatal — it leaves target nil and every result is
	// graded "?". An explicit but malformed --genero is a user typo, so error.
	target, err := resolveSearchTarget(generoFlag)
	if err != nil {
		return err
	}

	// Multi-provider fan-out when secondary repositories are configured.
	home, _ := fglpkgHome()
	var m *manifest.Manifest
	if mm, mErr := manifest.Load("."); mErr == nil {
		m = mm
	}
	if rs, _, _, rErr := buildRepositorySet(home, m); rErr == nil && rs != nil {
		return searchAcrossProviders(rs, term, all, target)
	}

	results, err := registry.Search(term)
	if err != nil {
		// Older registry servers reject an empty q with 400 — surface a
		// clean hint instead of the raw transport error.
		if all && strings.Contains(err.Error(), "HTTP 400") {
			return fmt.Errorf("this registry doesn't support --all (returned HTTP 400)\n" +
				"upgrade the registry, or pass a search term instead")
		}
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		if all {
			fmt.Println("No packages in the registry.")
		} else {
			fmt.Printf("No packages found matching %q\n", term)
		}
		return nil
	}
	if all {
		fmt.Printf("All packages (%d)%s:\n", len(results), searchVersionSuffix(target))
	} else {
		fmt.Printf("Results for %q%s:\n", term, searchVersionSuffix(target))
	}
	rows := make([]searchRow, 0, len(results))
	for _, r := range results {
		rows = append(rows, searchRow{
			name:        r.Name,
			version:     r.LatestVersion,
			constraint:  r.GeneroConstraint,
			description: r.Description,
			deprecated:  r.Deprecated,
			movedTo:     r.MovedTo,
		})
	}
	printSearchTable(rows, target, false)
	return nil
}

// resolveSearchTarget resolves the Genero version used to grade search results.
// An explicit --genero override is parsed leniently (a bare "4" or "4.01" is
// accepted — a patch level is not required for grading) and a malformed value
// is a fatal error; otherwise genero.Detect() is used and a failure is
// tolerated by returning a nil target (every result then grades as "?").
func resolveSearchTarget(generoFlag string) (*genero.Version, error) {
	if generoFlag != "" {
		gv, err := genero.ParseLoose(generoFlag)
		if err != nil {
			return nil, fmt.Errorf("invalid --genero version %q: %w", generoFlag, err)
		}
		return &gv, nil
	}
	if gv, err := genero.Detect(); err == nil {
		return &gv, nil
	}
	return nil, nil
}

// searchVersionSuffix renders the parenthetical shown after the results header,
// naming the resolved Genero version or explaining how to set one.
func searchVersionSuffix(target *genero.Version) string {
	if target != nil {
		return fmt.Sprintf(" (Genero %s)", target.String())
	}
	return " (Genero version unknown — set FGLPKG_GENERO_VERSION or pass --genero)"
}

// gradeCompat returns a one-column compatibility marker for a result's Genero
// constraint against the target version: "✓" compatible, "✗" incompatible, "?"
// unknown. Unknown covers no resolved target, no declared constraint, and an
// unparseable constraint — a malformed constraint degrades that one row to "?"
// rather than aborting the search.
func gradeCompat(target *genero.Version, constraint string) string {
	if target == nil || constraint == "" {
		return "?"
	}
	ok, err := target.Satisfies(constraint)
	if err != nil {
		return "?"
	}
	if ok {
		return "✓"
	}
	return "✗"
}

// displayConstraint renders a Genero constraint for the GENERO column, showing
// "-" when the registry reported none.
func displayConstraint(constraint string) string {
	if constraint == "" {
		return "-"
	}
	return constraint
}

// searchRow is one line of the annotated search table. source is empty in
// single-registry mode; movedTo is the successor slug when a deprecation is a
// relocation.
type searchRow struct {
	name        string
	version     string
	constraint  string
	description string
	deprecated  bool
	movedTo     string
	source      string
}

// searchRowFormat builds the Printf format string for one table line. Columns
// are NAME VERSION GENERO verdict [STATUS] [SOURCE] DESCRIPTION; the two-space
// gap after the verdict keeps its single-char column visually distinct. The
// GENERO width is passed in so a long constraint range (e.g. ">=3.20.00
// <5.00.00") doesn't spill into the verdict column.
func searchRowFormat(generoWidth int, showStatus, showSource bool) string {
	cols := make([]string, 0, 3)
	if showStatus {
		cols = append(cols, "%-24s")
	}
	if showSource {
		cols = append(cols, "%-24s")
	}
	cols = append(cols, "%s")
	return fmt.Sprintf("  %%-28s %%-12s %%-%ds %%s  ", generoWidth) + strings.Join(cols, " ") + "\n"
}

// printSearchTable renders the annotated results table shared by the
// single-registry and multi-provider search paths. The GENERO and verdict
// columns grade each row against its own constraint (see gradeCompat); rows
// whose provider supplies no constraint render "-"/"?". showSource adds the
// SOURCE column (multi-provider mode); the STATUS column appears only when at
// least one row is deprecated, so the common all-live listing stays narrow.
func printSearchTable(rows []searchRow, target *genero.Version, showSource bool) {
	showStatus := false
	generoWidth := 12 // floor: keeps short constraint lists looking as before
	for _, r := range rows {
		if r.deprecated {
			showStatus = true
		}
		if w := len(displayConstraint(r.constraint)); w > generoWidth {
			generoWidth = w
		}
	}
	format := searchRowFormat(generoWidth, showStatus, showSource)

	header := []any{"NAME", "VERSION", "GENERO", "?"}
	divider := []any{"----", "-------", "------", "-"}
	if showStatus {
		header = append(header, "STATUS")
		divider = append(divider, "------")
	}
	if showSource {
		header = append(header, "SOURCE")
		divider = append(divider, "------")
	}
	header = append(header, "DESCRIPTION")
	divider = append(divider, "-----------")
	fmt.Printf(format, header...)
	fmt.Printf(format, divider...)

	for _, r := range rows {
		vals := []any{r.name, r.version, displayConstraint(r.constraint), gradeCompat(target, r.constraint)}
		if showStatus {
			vals = append(vals, searchDeprecatedStatus(r.deprecated, r.movedTo))
		}
		if showSource {
			vals = append(vals, r.source)
		}
		vals = append(vals, r.description)
		fmt.Printf(format, vals...)
	}
}

// searchAcrossProviders fans out a search to every configured provider, tags
// each result with its source repository, and prints a source-tagged table.
//
// Each row is graded against its own Genero constraint: the Genero provider
// supplies one via registry.Search, while Artifactory leaves it empty until
// FetchInfo, so those rows render "-"/"?" (unknown). On a name collision the
// constraint (like the version/description) comes from the highest-priority
// source. The columns match the single-registry search layout.
func searchAcrossProviders(rs *provider.RepositorySet, term string, all bool, target *genero.Version) error {
	// Gather in provider priority order so the first-seen version/description
	// for a colliding name comes from the highest-priority repository.
	type merged struct {
		name        string
		version     string
		description string
		constraint  string
		deprecated  bool     // package-level deprecation, from the highest-priority source
		movedTo     string   // successor slug when the deprecation is a relocation
		sources     []string // every repo the name appears in, priority order
	}
	var order []string
	byName := map[string]*merged{}
	for _, p := range rs.Providers() {
		rr, err := p.Search(term)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: search in %q failed: %v\n", p.Name(), err)
			continue
		}
		for _, r := range rr {
			if m, ok := byName[r.Name]; ok {
				// A name in more than one repo: record the extra source but keep
				// the higher-priority repo's version/description.
				m.sources = append(m.sources, p.Name())
				continue
			}
			byName[r.Name] = &merged{
				name:        r.Name,
				version:     r.LatestVersion,
				description: r.Description,
				constraint:  r.GeneroConstraint,
				deprecated:  r.Deprecated,
				movedTo:     r.MovedTo,
				sources:     []string{p.Name()},
			}
			order = append(order, r.Name)
		}
	}
	if len(order) == 0 {
		if all {
			fmt.Println("No packages in any configured repository.")
		} else {
			fmt.Printf("No packages found matching %q\n", term)
		}
		return nil
	}
	if all {
		fmt.Printf("All packages (%d)%s:\n", len(order), searchVersionSuffix(target))
	} else {
		fmt.Printf("Results for %q%s:\n", term, searchVersionSuffix(target))
	}
	// The GENERO + verdict columns grade each result against its own constraint
	// (the Genero provider supplies one; Artifactory leaves it empty until
	// FetchInfo, so those rows render "-"/"?"). The STATUS column only appears
	// when at least one match is deprecated.
	collisions := 0
	rows := make([]searchRow, 0, len(order))
	for _, name := range order {
		m := byName[name]
		if len(m.sources) > 1 {
			collisions++
		}
		rows = append(rows, searchRow{
			name:        m.name,
			version:     m.version,
			constraint:  m.constraint,
			description: m.description,
			deprecated:  m.deprecated,
			movedTo:     m.movedTo,
			source:      strings.Join(m.sources, ", "),
		})
	}
	printSearchTable(rows, target, true)
	if collisions > 0 {
		fmt.Printf("\nnote: %d package name(s) are available from more than one repository (shown with all sources).\n"+
			"  Pin the source in fglpkg.json to install a colliding name.\n", collisions)
	}
	return nil
}

// parseSearchArgs returns the keyword term, the --all flag, and an optional
// --genero <version> override used to grade result compatibility. Errors on
// `search` with no args + no --all (the historical "missing keyword" error),
// and on conflicting `search --all <term>`.
func parseSearchArgs(args []string) (term string, all bool, generoFlag string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all":
			all = true
		case a == "--genero":
			if i+1 >= len(args) {
				return "", false, "", fmt.Errorf("--genero requires a version argument (e.g. --genero 4.01)")
			}
			i++
			generoFlag = args[i]
		case strings.HasPrefix(a, "--genero="):
			generoFlag = strings.TrimPrefix(a, "--genero=")
			if generoFlag == "" {
				return "", false, "", fmt.Errorf("--genero requires a version argument (e.g. --genero 4.01)")
			}
		default:
			if term != "" {
				return "", false, "", fmt.Errorf("unexpected extra argument %q", a)
			}
			term = a
		}
	}
	if all && term != "" {
		return "", false, "", fmt.Errorf("--all and <term> are mutually exclusive")
	}
	if !all && term == "" {
		return "", false, "", fmt.Errorf("usage: fglpkg search <term>   |   fglpkg search --all")
	}
	return term, all, generoFlag, nil
}

// ─── publish ──────────────────────────────────────────────────────────────────

// isValidYesNo reports whether s begins with a "y" or "n" (case-insensitive),
// i.e. whether it is a recognizable yes/no answer. The caller guarantees s is
// non-empty.
func isValidYesNo(s string) bool {
	return strings.ToLower(string(s[0])) == "y" || strings.ToLower(string(s[0])) == "n"
}

// promptYesNo asks a yes/no question and returns the answer. A bare enter
// accepts yesDefault (rendered as [Y/n] or [y/N]); an unrecognized answer
// re-prompts. A closed or interrupted stdin (e.g. Ctrl+C) returns false
// regardless of the default — a broken stream can never be affirmative consent,
// so an interrupted confirmation must not be treated as a yes.
func promptYesNo(prompt string, yesDefault bool) bool {
	defaultRes := "y"
	fullPrompt := prompt + " [Y/n]"
	if !yesDefault {
		defaultRes = "n"
		fullPrompt = prompt + " [y/N]"
	}

	for {
		fmt.Printf("%s: ", fullPrompt)
		res, err := reader.ReadString('\n')
		// Trim CR and LF to handle both Unix (\n) and Windows (\r\n) endings.
		res = strings.TrimRight(res, "\r\n")
		switch {
		case err != nil && res == "":
			// stdin closed or interrupted (e.g. Ctrl+C): treat as "not
			// confirmed" and return false regardless of the default. A
			// broken/closed stream can never be affirmative consent, so honoring
			// a yes-default here would let an interrupted publish proceed. This
			// also avoids re-prompting a stream that only ever returns EOF.
			return false
		case res == "":
			// Bare enter accepts the default.
			return defaultRes == "y"
		case isValidYesNo(res):
			return strings.ToLower(string(res[0])) == "y"
		}
		// Anything else was a typo at an interactive prompt: ask again.
	}
}

// binaryToSourceExt maps each compiled binary extension to the source
// extension it is produced from. checkForRecompile pairs a packaged binary
// with the source it must be newer than, to warn when a binary looks stale.
var binaryToSourceExt = map[string]string{
	".42m": ".4gl",
	".42f": ".per",
	".42s": ".str",
}

// checkForRecompile warns when a compiled file about to be published looks
// older than the source it was built from — a common "forgot to recompile"
// mistake. Binaries come from the package zip (so nested paths are covered);
// their sources are located anywhere in the project via a one-pass index, so
// split src/ vs build/ layouts are handled, not just compile-in-place.
func checkForRecompile(m *manifest.Manifest) {
	zipData, _, err := buildPackageZip(m)
	if err != nil {
		return
	}
	entries, err := listZipEntries(zipData)
	if err != nil {
		return
	}

	ignore, err := loadIgnore(".")
	if err != nil {
		ignore = &ignoreSet{}
	}
	index := buildSourceIndex(ignore)

	var stale []string
	seen := make(map[string]bool)
	for _, e := range entries {
		binPath := e.name
		if _, known := binaryToSourceExt[path.Ext(binPath)]; !known {
			continue
		}
		binInfo, err := os.Stat(filepath.FromSlash(binPath))
		if err != nil {
			continue
		}
		binMTime := binInfo.ModTime()

		sources, ok := resolveSource(binPath, index)
		if !ok {
			continue
		}
		// Compare against every plausible source: if any is newer than the
		// binary, the binary may be stale. When the match is ambiguous this
		// errs toward warning, which is the safer failure for a publish guard.
		for _, src := range sources {
			srcInfo, statErr := os.Stat(filepath.FromSlash(src))
			if statErr != nil {
				continue
			}
			if srcInfo.ModTime().After(binMTime) && !seen[src] {
				seen[src] = true
				stale = append(stale, src)
				break
			}
		}
	}

	if len(stale) == 0 {
		return
	}
	fmt.Printf("warning: %s may not have been recompiled\n", joinWithAnd(stale))
	if !promptYesNo("Continue?", true) {
		os.Exit(1)
	}
}

// buildSourceIndex walks the project tree once and indexes every source file
// (any extension appearing as a value in binaryToSourceExt) by its basename.
// Paths are project-relative and slash-normalized so they compare cleanly
// against zip entry names. The .fglpkg artifact dir and any files matched by
// .fglpkgignore are skipped, so vendored/installed packages and deliberately
// excluded files don't produce phantom source matches.
func buildSourceIndex(ignore *ignoreSet) map[string][]string {
	sourceExts := make(map[string]bool, len(binaryToSourceExt))
	for _, ext := range binaryToSourceExt {
		sourceExts[ext] = true
	}

	index := make(map[string][]string)
	_ = filepath.Walk(".", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable subtrees
		}
		if info.IsDir() {
			if isPackArtifactDir(p) {
				return filepath.SkipDir
			}
			// Prune whole ignored subtrees (e.g. a "test/" rule) so their
			// sources never enter the index.
			if dirShouldBeSkipped(ignore, p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExts[filepath.Ext(p)] {
			return nil
		}
		rel, relErr := filepath.Rel(".", p)
		if relErr != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		if ignore.shouldExclude(rel, false) {
			return nil
		}
		base := path.Base(rel)
		index[base] = append(index[base], rel)
		return nil
	})
	return index
}

// resolveSource picks the source file(s) most likely to have produced the
// binary at binPath (a slash-separated, project-relative zip entry name),
// choosing from the sources indexed by buildSourceIndex. It tries, in order:
//  1. an exact sibling — a source at the identical relative path (compile-in-place);
//  2. the candidate(s) sharing the longest trailing path-segment run with the
//     binary — handles split layouts like src/… compiled to lib/…;
//  3. a lone basename match anywhere in the tree — handles a flattened build dir.
//
// When several candidates tie on the best path-suffix score, all tied paths are
// returned so the caller can compare against each. ok is false when the binary
// extension is unknown or no source with the expected basename exists.
func resolveSource(binPath string, index map[string][]string) (sources []string, ok bool) {
	binExt := path.Ext(binPath)
	sourceExt, known := binaryToSourceExt[binExt]
	if !known {
		return nil, false
	}
	wantBase := strings.TrimSuffix(path.Base(binPath), binExt) + sourceExt
	candidates := index[wantBase]
	if len(candidates) == 0 {
		return nil, false
	}

	binDir := path.Dir(binPath)

	// Tier 1: exact sibling path (compile-in-place). Unambiguous, so return it.
	sibling := path.Join(binDir, wantBase)
	for _, c := range candidates {
		if c == sibling {
			return []string{c}, true
		}
	}

	// Tier 2/3: rank by longest shared trailing path segments. A single
	// candidate naturally wins (tier 3); genuine ties are all returned.
	bestScore := -1
	var best []string
	for _, c := range candidates {
		score := commonSuffixSegments(path.Dir(c), binDir)
		switch {
		case score > bestScore:
			bestScore = score
			best = []string{c}
		case score == bestScore:
			best = append(best, c)
		}
	}
	return best, true
}

// commonSuffixSegments counts how many trailing path segments two
// slash-separated directory paths share. "src/com/foo" and "lib/com/foo"
// share 2 ("com", "foo"). Empty or "." dirs contribute no segments.
func commonSuffixSegments(a, b string) int {
	as, bs := splitDirSegments(a), splitDirSegments(b)
	n := 0
	for n < len(as) && n < len(bs) && as[len(as)-1-n] == bs[len(bs)-1-n] {
		n++
	}
	return n
}

// splitDirSegments splits a slash-separated directory path into its segments,
// treating an empty path or "." as having none.
func splitDirSegments(p string) []string {
	if p == "" || p == "." {
		return nil
	}
	return strings.Split(p, "/")
}

// joinWithAnd renders a slice as a human list: "a", "a and b",
// "a, b, and c".
func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

// parsePublishFlags reads the publish flags: --dry-run/-n (preview, no
// network), --ci (non-interactive pipeline mode), --private/--public
// (visibility override on first publish), and --changelog <text> (inline
// changelog for this version, overriding the auto CHANGELOG.md extraction
// done at publish time).
// publishFlags is the parsed flag surface of `fglpkg publish`.
type publishFlags struct {
	dryRun     bool
	ci         bool
	force      bool   // Artifactory: overwrite an existing variant
	registry   string // target repository ("" or "gi" = GI)
	visibility string // "private"/"public" (GI only)
	changelog  string
}

func parsePublishFlags(args []string) (publishFlags, error) {
	var pf publishFlags
	var wantPrivate, wantPublic bool
	// Loop by index so --changelog/--registry can consume the following
	// argument. Both "--flag value" and "--flag=value" forms are accepted.
	for i := 0; i < len(args); i++ {
		a := args[i]
		if val, ok := strings.CutPrefix(a, "--changelog="); ok {
			pf.changelog = val
			continue
		}
		if val, ok := strings.CutPrefix(a, "--registry="); ok {
			pf.registry = val
			continue
		}
		switch a {
		case "--dry-run", "-n":
			pf.dryRun = true
		case "--ci":
			pf.ci = true
		case "--force", "-f":
			pf.force = true
		case "--private":
			wantPrivate = true
		case "--public":
			wantPublic = true
		case "--changelog":
			if i+1 >= len(args) {
				return publishFlags{}, fmt.Errorf("%s requires a value", a)
			}
			i++
			pf.changelog = args[i]
		case "--registry":
			if i+1 >= len(args) {
				return publishFlags{}, fmt.Errorf("%s requires a value", a)
			}
			i++
			pf.registry = args[i]
		default:
			return publishFlags{}, fmt.Errorf("unexpected argument %q", a)
		}
	}
	if wantPrivate && wantPublic {
		return publishFlags{}, fmt.Errorf("--private and --public are mutually exclusive")
	}
	if wantPrivate {
		pf.visibility = "private"
	} else if wantPublic {
		pf.visibility = "public"
	}
	return pf, nil
}

func cmdPublish(args []string) error {
	pf, err := parsePublishFlags(args)
	if err != nil {
		return err
	}
	dryRun, ci, visibilityOverride, changelogText := pf.dryRun, pf.ci, pf.visibility, pf.changelog

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	if err := m.ValidateForPublish(); err != nil {
		return err
	}
	// Detect Genero before the publish check so the latter can reject only
	// when the SAME variant (not just the same version string) is already
	// published. Allows adding new Genero major variants to an existing
	// version.
	gv, err := genero.Detect()
	if err != nil {
		return fmt.Errorf("cannot detect Genero version: %w", err)
	}
	generoMajor := gv.MajorString()

	// With no explicit --registry, fall back to the configured default publish
	// target (FGLPKG_PUBLISH_REGISTRY, then the project/global defaultRegistry),
	// so a team publishing to their own Artifactory need not pass --registry
	// every time. An empty result keeps the historical default of publishing to
	// GI. --registry, when given, always wins (it is already in pf.registry).
	if pf.registry == "" {
		pf.registry = resolveDefaultPublishRegistry(home, m)
	}

	// Publishing to a secondary (Artifactory) repository takes a distinct,
	// direct-PUT path — no GI variant pre-check, no submit/approval step.
	if pf.registry != "" && pf.registry != config.GIName {
		// Same recompile staleness guard as the GI path below — it is about the
		// local build, not the target, so it applies identically here.
		if !ci {
			checkForRecompile(m)
		}
		// Note: the GI path's `--ci` → FGLPKG_TOKEN requirement intentionally does
		// NOT apply here. Artifactory auth is per-registry (apikey/bearer/basic,
		// resolved by credentials.AuthHeaders inside publishToArtifactory), so
		// there is no single env-token analog to enforce; missing credentials are
		// already rejected there.
		projectDir, _ := os.Getwd()
		if err := runHook(m, manifest.HookPrePublish, projectDir); err != nil {
			return err
		}
		if err := publishToArtifactory(home, m, generoMajor, pf); err != nil {
			return fmt.Errorf("publish failed: %w", err)
		}
		return runHook(m, manifest.HookPostPublish, projectDir)
	}

	if err := checkVariantNotPublished(m, generoMajor); err != nil {
		return err
	}
	registryURL := defaultPublishRegistry()

	// Resolve the bearer. In --ci mode the token must come from the
	// environment (FGLPKG_TOKEN) — CI runners should not depend on cached
	// interactive credentials, and the error must not suggest `fglpkg login`.
	var token string
	if ci {
		token = credentials.ConsumerEnvBearer()
		if token == "" {
			return fmt.Errorf("--ci: no registry token in the environment; set FGLPKG_TOKEN")
		}
	} else {
		token, err = credentials.ActiveBearer(context.Background(), home, registryURL, oauth.Refresh)
		if err != nil {
			return err
		}
		if token == "" {
			return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' (or set FGLPKG_TOKEN) first", registryURL)
		}
	}

	if dryRun {
		fmt.Printf("DRY RUN — no network calls will be made\n\n")
	}
	if !ci {
		checkForRecompile(m)
	}
	variant := artifactVariant(m, generoMajor)
	fmt.Printf("Publishing %s@%s (%s) to %s...\n", m.Name, m.Version, variantDescription(variant), registryURL)
	projectDir, _ := os.Getwd()
	if err := runHook(m, manifest.HookPrePublish, projectDir); err != nil {
		return err
	}
	if err := publishPackage(m, registryURL, generoMajor, dryRun, visibilityOverride, changelogText); err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}
	if dryRun {
		fmt.Printf("✓ Dry run complete for %s@%s — no changes made\n", m.Name, m.Version)
	} else {
		fmt.Printf("✓ Published %s@%s — pending admin review\n", m.Name, m.Version)
		if ci {
			// Stable, greppable line for pipeline consumption.
			fmt.Printf("fglpkg-published name=%s version=%s variant=%s status=pending\n",
				m.Name, m.Version, variant)
		}
	}
	return runHook(m, manifest.HookPostPublish, projectDir)
}

// publishToArtifactory deploys the built package + sidecar manifest to a
// configured Artifactory generic repository via direct PUTs (spec §10).
func publishToArtifactory(home string, m *manifest.Manifest, generoMajor string, pf publishFlags) error {
	reg, err := resolveRegistry(home, pf.registry)
	if err != nil {
		return err
	}
	if reg.Type != config.TypeArtifactory {
		return fmt.Errorf("registry %q is type %q, not artifactory", reg.Name, reg.Type)
	}
	if pf.visibility != "" {
		fmt.Fprintf(os.Stderr, "  note: --private/--public is ignored when publishing to Artifactory (access is governed by the repository)\n")
	}

	creds, _ := credentials.Load(home)
	var headers map[string]string
	if creds != nil {
		headers = creds.AuthHeaders(reg.URL, reg.Auth)
	}
	if len(headers) == 0 && reg.Auth != config.AuthAnonymous {
		return fmt.Errorf("no credentials for registry %q — run 'fglpkg login --registry %s'", reg.Name, reg.Name)
	}
	p := provider.NewArtifactoryProvider(reg, nil, headersApplier(headers))

	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return fmt.Errorf("cannot build package zip: %w", err)
	}
	sidecar, err := json.MarshalIndent(m.PublishCopy(), "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize sidecar manifest: %w", err)
	}
	variant := artifactVariant(m, generoMajor)

	fmt.Printf("Publishing %s@%s (%s) to %s (%s)...\n", m.Name, m.Version, variantDescription(variant), reg.Name, reg.URL)
	if pf.dryRun {
		fmt.Printf("DRY RUN — no network calls will be made\n")
	} else {
		fmt.Printf("  Package zip: %d bytes (SHA256: %s)\n", len(zipData), checksum)
	}
	if err := p.Publish(provider.PublishRequest{
		Name:     m.Name,
		Version:  m.Version,
		Variant:  variant,
		Zip:      zipData,
		Checksum: checksum,
		Manifest: append(sidecar, '\n'),
		Force:    pf.force,
		DryRun:   pf.dryRun,
	}); err != nil {
		return err
	}
	if pf.dryRun {
		fmt.Printf("✓ Dry run complete for %s@%s — no changes made\n", m.Name, m.Version)
	} else {
		fmt.Printf("✓ Published %s@%s to %s\n", m.Name, m.Version, reg.Name)
	}
	return nil
}

// publishPackage drives the new /registry/* publish flow against the registry
// at registryURL using the bearer wired into registry.Bearer (typically the
// OAuth access token from `fglpkg login`).
//
// Steps:
//  1. Build the zip locally and SHA256 it (for the dry-run preview).
//  2. POST /registry/packages — creates the slug; 409 means "already exists"
//     which is fine.
//  3. POST /registry/packages/:slug/versions — creates the version, carrying
//     the rich metadata (repository/author/license/genero/dependencies +
//     README/USERGUIDE) from the manifest and root doc files. 409 means the
//     version already exists for a different variant; the publish proceeds to
//     step 4 to add this variant and does NOT resend the metadata (the
//     registry stores it once, at first create).
//  4. PUT /registry/packages/:slug/versions/:version/artifacts/:variant —
//     streams the zip body. Server computes size + sha256 and stores in R2.
//  5. POST /registry/packages/:slug/versions/:version/submit — marks the
//     version pending so an admin reviews and approves.
func publishPackage(m *manifest.Manifest, registryURL, generoMajor string, dryRun bool, visibilityOverride, changelogText string) error {
	// 1. Build the zip.
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return fmt.Errorf("cannot build package zip: %w", err)
	}
	fmt.Printf("  Package zip: %d bytes (SHA256: %s)\n", len(zipData), checksum)

	variant := artifactVariant(m, generoMajor)
	// The slug is the canonical (PyPI/PEP 503) form of the name — lowercased,
	// with runs of '-'/'_'/'.' collapsed to '-' — and is the package's identity
	// in every /registry/... path. The manifest name is kept as the display
	// name. Validity is already enforced upstream by ValidateForPublish (which
	// runs for every publish path, GI and Artifactory alike), so here we only
	// need the canonical form for the URL (GIS-271).
	slug := slugutil.Canonical(m.Name)
	if slug != m.Name {
		fmt.Printf("  Normalized name %q → slug %q\n", m.Name, slug)
	}
	filename := artifactFilename(slug, m.Version, variant)
	visibility := visibilityOverride
	if visibility == "" {
		visibility = m.Visibility
	}
	if visibility == "" {
		visibility = "public"
	}

	// Collect the rich per-version metadata pushed on version-create:
	// repository/author/license/genero + production dependencies from the
	// manifest, plus the root-level README / USERGUIDE markdown bodies.
	// Docs live at the PROJECT root (next to fglpkg.json), NOT under m.Root —
	// m.Root is the package *source* base (e.g. "com/fourjs/odatalib") that
	// holds the .4gl/.per files, while README.md / USERGUIDE.md sit at the
	// project dir. publish always runs from the project dir, so scan ".".
	// Absent docs are not an error. This metadata is sent on create-version
	// only — when a 409 (version exists) sends us into the add-a-variant path
	// below, it is deliberately not resent (the registry stores it once).
	const docRoot = "."
	readme, err := collectReadme(docRoot)
	if err != nil {
		return err
	}
	userguide, err := collectUserguide(docRoot)
	if err != nil {
		return err
	}

	// Resolve the per-version changelog. An inline --changelog overrides
	// everything; otherwise take the auto path — extract the CHANGELOG.md
	// section matching this version. If a CHANGELOG exists but has no entry
	// for m.Version, warn and send an empty changelog (publishing is not
	// blocked) so the author knows to add one.
	changelog := changelogText
	if changelog == "" {
		var found bool
		changelog, found, err = collectChangelog(docRoot, m.Version)
		if err != nil {
			return err
		}
		if found && changelog == "" {
			fmt.Printf("  ⚠ CHANGELOG found but has no entry for %s — publishing with an empty changelog\n", m.Version)
		}
	}

	meta := registry.VersionMeta{
		Repository:   m.Repository,
		Author:       m.Author,
		License:      m.License,
		Genero:       m.GeneroConstraint,
		Dependencies: m.Dependencies,
		Readme:       readme,
		Userguide:    userguide,
	}

	if dryRun {
		fmt.Printf("  [dry-run] would POST   %s/registry/packages\n", registryURL)
		fmt.Printf("            body: {slug:%q, name:%q, description:%q, visibility:%q}\n",
			slug, m.Name, m.Description, visibility)
		fmt.Printf("  [dry-run] would POST   %s/registry/packages/%s/versions\n", registryURL, slug)
		fmt.Printf("            body: {version:%q, changelog:%s}\n",
			m.Version, docSizeLabel(changelog, changelogTruncationMarker))
		fmt.Printf("            metadata:\n")
		fmt.Printf("              repository:   %s\n", dryRunScalar(meta.Repository))
		fmt.Printf("              author:       %s\n", dryRunScalar(meta.Author))
		fmt.Printf("              license:      %s\n", dryRunScalar(meta.License))
		fmt.Printf("              genero:       %s\n", dryRunScalar(meta.Genero))
		fmt.Printf("              dependencies: %d fgl, %d java\n",
			len(meta.Dependencies.FGL), len(meta.Dependencies.Java))
		fmt.Printf("              readme:       %s\n", docSizeLabel(readme, readmeTruncationMarker))
		fmt.Printf("              userguide:    %s\n", docSizeLabel(userguide, userguideTruncationMarker))
		fmt.Printf("  [dry-run] would PUT    %s/registry/packages/%s/versions/%s/artifacts/%s?filename=%s\n",
			registryURL, slug, m.Version, variant, filename)
		fmt.Printf("            body: <%d bytes zip>\n", len(zipData))
		fmt.Printf("  [dry-run] would POST   %s/registry/packages/%s/versions/%s/submit\n",
			registryURL, slug, m.Version)
		if m.Description != "" || len(m.Keywords) > 0 {
			fmt.Printf("  [dry-run] would PATCH  %s/registry/packages/%s   (sync search metadata)\n",
				registryURL, slug)
			fmt.Printf("            body: {description:%q, keywords:%v}\n", m.Description, m.Keywords)
		}
		return nil
	}

	// 2. Create package (or no-op if already exists).
	fmt.Println("  → POST   /registry/packages")
	if err := registry.PublishCreatePackage(slug, m.Name, m.Description, visibility); err != nil {
		return err
	}

	// 3. Create version. 409 (already exists) is fine — caller is adding
	//    a new variant to an existing version.
	fmt.Println("  → POST   /registry/packages/" + slug + "/versions")
	if err := registry.PublishCreateVersion(slug, m.Version, changelog, nil, meta); err != nil {
		if !errors.Is(err, registry.ErrVersionExists) {
			return err
		}
		fmt.Println("    (version exists; adding variant)")
	}

	// 4. Upload zip.
	fmt.Printf("  → PUT    /registry/packages/%s/versions/%s/artifacts/%s\n",
		slug, m.Version, variant)
	if err := registry.PublishUploadArtifact(slug, m.Version, variant, filename, bytes.NewReader(zipData)); err != nil {
		return err
	}

	// 5. Submit for review.
	fmt.Println("  → POST   /registry/packages/" + slug + "/versions/" + m.Version + "/submit")
	if err := registry.PublishSubmit(slug, m.Version); err != nil {
		return err
	}

	// 6. Sync package discovery metadata (description + keywords) so edits made
	//    after the slug was first created still reach the registry and feed
	//    `fglpkg search` (GIS-268 F/G). Best-effort: the version is already
	//    published, so an older registry without the metadata op — or any
	//    transient failure — warns rather than failing the publish.
	if m.Description != "" || len(m.Keywords) > 0 {
		fmt.Println("  → PATCH  /registry/packages/" + slug + "   (description, keywords)")
		if err := registry.PublishUpdateMetadata(slug, m.Description, m.Keywords); err != nil {
			fmt.Printf("    ⚠ could not sync search metadata (description/keywords): %v\n", err)
		}
	}
	return nil
}

// dryRunScalar renders an optional scalar metadata field for the dry-run
// preview, showing "(none)" rather than an empty line when it is unset.
func dryRunScalar(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

// docSizeLabel renders a README/USERGUIDE/changelog body for the dry-run
// preview as a human size, "(none)" when empty, and flags "(truncated)" when
// the cap marker was appended. Sub-kilobyte content is shown in bytes so a
// short-but-present body reads as e.g. "48 B" rather than a misleading
// "0.0 KB".
func docSizeLabel(content, marker string) string {
	if content == "" {
		return "(none)"
	}
	var label string
	if n := len(content); n < 1024 {
		label = fmt.Sprintf("%d B", n)
	} else {
		label = fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	if strings.HasSuffix(content, marker) {
		label += " (truncated)"
	}
	return label
}

// artifactVariant returns the registry variant tag for a package:
//   - "webcomponent" when the manifest declares webcomponents and NO BDL
//     content (pure-WC package, genero-version-agnostic)
//   - "genero<major>" otherwise (classic BDL, or a mixed package that
//     pairs webcomponents with a BDL wrapper — BDL forces per-major
//     fan-out, the WC bytes ride along inside each genero variant)
func artifactVariant(m *manifest.Manifest, generoMajor string) string {
	if m.HasWebcomponents() && !m.HasBDLContent() {
		return "webcomponent"
	}
	return "genero" + generoMajor
}

// artifactFilename returns the zip filename for a published artifact:
// "<name>-<version>-<variant>.zip".
func artifactFilename(name, version, variant string) string {
	return fmt.Sprintf("%s-%s-%s.zip", name, version, variant)
}

// variantDescription is a human-readable label for the variant, used in
// publish/pack progress output.
func variantDescription(variant string) string {
	if variant == "webcomponent" {
		return "webcomponent variant"
	}
	return "Genero " + strings.TrimPrefix(variant, "genero") + " variant"
}

func buildPackageZip(m *manifest.Manifest) ([]byte, string, error) {
	// Package by staging the exact archive layout in a throwaway temp
	// directory and zipping that — the publisher's source tree is never
	// written to. See specs/import-root.md.
	stageDir, err := os.MkdirTemp("", "fglpkg-pack-")
	if err != nil {
		return nil, "", fmt.Errorf("cannot create staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)

	if err := stagePackage(stageDir, m); err != nil {
		return nil, "", err
	}
	return zipStageDir(stageDir)
}

// stagePackage materializes the full publishable layout under stageDir:
// rebased BDL/bin files, webcomponents (with their "webcomponents/" strip),
// docs at the archive root, explicit include files folded into the root by
// basename, and the publish-safe manifest.
func stagePackage(stageDir string, m *manifest.Manifest) error {
	// Load .fglpkgignore from the project root (current directory). The
	// manifest is never excluded; everything else can be filtered.
	ignore, err := loadIgnore(".")
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", ignoreFilename, err)
	}

	// staged maps an archive path to the source it came from, so a second
	// distinct source claiming the same path is reported as a collision
	// (while the same source matched twice is a harmless no-op).
	staged := make(map[string]string)

	// include entries are folded in explicitly (below) and skipped by the
	// BDL walk so they are not also picked up at a rebased path.
	includeSet := make(map[string]bool)
	for _, inc := range m.Include {
		includeSet[filepath.Clean(inc)] = true
	}

	// Mixed packages run BOTH walkers. A pure-WC manifest skips the BDL walk
	// (HasBDLContent returns false); a pure-BDL manifest skips the webcomponent
	// walk (HasWebcomponents returns false).
	if m.HasBDLContent() || !m.HasWebcomponents() {
		if err := stageBDLFiles(stageDir, m, ignore, staged, includeSet); err != nil {
			return err
		}
	}
	if m.HasWebcomponents() {
		if err := stageWebcomponentFiles(stageDir, m, ignore, staged); err != nil {
			return err
		}
	}
	if err := stageDocFiles(stageDir, m, ignore, staged); err != nil {
		return err
	}
	if err := stageIncludeFiles(stageDir, m, staged); err != nil {
		return err
	}

	// Always write the manifest last, using a publish-safe copy so the shipped
	// fglpkg.json omits devDependencies and reflects the post-strip layout.
	// This is authoritative — it overwrites any file already staged at
	// fglpkg.json rather than colliding.
	mfData, err := json.MarshalIndent(m.PublishCopy(), "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize publishable %s: %w", manifest.Filename, err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, manifest.Filename), append(mfData, '\n'), 0o644); err != nil {
		return fmt.Errorf("cannot stage %s: %w", manifest.Filename, err)
	}
	return nil
}

// filesPatternMatch reports whether a manifest `files` pattern matches a source
// file during the pack staging walk.
//
//   - A pattern WITHOUT "/" (e.g. "*.42m") matches the file's BASENAME at any
//     depth under root — the historical behaviour, preserved byte-for-byte.
//   - A pattern CONTAINING "/" (e.g. "tests/*.4gl", "com/**/*.42m") is
//     path-scoped: it matches the file's path RELATIVE TO root, with "**"
//     spanning directory levels and "*" confined to a single path segment. A
//     leading "/" is accepted and anchors at root (same as no leading slash).
//
// A basename never contains "/", so every "/"-pattern matched nothing under the
// old basename-only rule; giving them path semantics therefore assigns
// behaviour only to previously dead patterns and cannot change any manifest
// that works today (GIS-275). Note the reference point: `files` path-patterns
// are relative to root (the BDL source base), whereas .fglpkgignore patterns
// are relative to the project root — see docs/user-guide.md.
func filesPatternMatch(pattern, base, relToRoot string, relToRootErr error) bool {
	slashPattern := filepath.ToSlash(pattern)
	if !strings.Contains(slashPattern, "/") {
		matched, _ := filepath.Match(pattern, base)
		return matched
	}
	if relToRootErr != nil {
		return false
	}
	anchored := strings.TrimPrefix(slashPattern, "/")
	return matchGlob(anchored, filepath.ToSlash(relToRoot))
}

// stageBDLFiles walks the BDL source tree (m.Root, default "."), applying the
// manifest's `files` patterns (defaulting to *.42m/*.42f/*.sch) and declared
// `bin` scripts, and stages each match at its path rebased under importRoot.
// Files listed in `include` are skipped here — they are folded in separately.
func stageBDLFiles(stageDir string, m *manifest.Manifest, ignore *ignoreSet, staged map[string]string, includeSet map[string]bool) error {
	root := m.Root
	if root == "" {
		root = "."
	}
	patterns := m.Files
	if len(patterns) == 0 {
		patterns = []string{"*.42m", "*.42f", "*.sch"}
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isPackArtifactDir(path) {
				return filepath.SkipDir
			}
			if dirShouldBeSkipped(ignore, path) {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		// relToRoot drives path-scoped `files` patterns (those containing "/");
		// bare patterns still match on the basename alone (GIS-275).
		relToRoot, relToRootErr := filepath.Rel(root, path)
		for _, pattern := range patterns {
			if !filesPatternMatch(pattern, base, relToRoot, relToRootErr) {
				continue
			}
			relPath, relErr := filepath.Rel(".", path)
			if relErr != nil {
				relPath = path
			}
			if includeSet[filepath.Clean(relPath)] {
				return nil // folded in explicitly at the archive root
			}
			if ignore.shouldExclude(relPath, false) {
				return nil
			}
			archivePath, err := stagePathFor(m.ImportRoot, relPath)
			if err != nil {
				return err
			}
			return stageFile(stageDir, archivePath, path, staged)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking root %q: %w", root, err)
	}

	// Bin scripts are always shipped, even if .fglpkgignore would exclude them
	// — dropping a declared `bin` script would silently break the package.
	for _, scriptPath := range m.BinFiles() {
		fullPath := filepath.Join(root, scriptPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("bin script %q not found: %w", scriptPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("bin script %q is a directory, not a file", scriptPath)
		}
		relPath, relErr := filepath.Rel(".", fullPath)
		if relErr != nil {
			relPath = fullPath
		}
		archivePath, err := stagePathFor(m.ImportRoot, relPath)
		if err != nil {
			return err
		}
		if err := stageFile(stageDir, archivePath, fullPath, staged); err != nil {
			return err
		}
	}
	return nil
}

// stageWebcomponentFiles stages each declared webcomponents/<COMPONENTTYPE>/
// tree with the leading "webcomponents/" prefix stripped — so a source file at
// webcomponents/3DChart/3DChart.html is stored as 3DChart/3DChart.html. Each
// declared COMPONENTTYPE must have a directory and a <COMPONENTTYPE>.html entry.
func stageWebcomponentFiles(stageDir string, m *manifest.Manifest, ignore *ignoreSet, staged map[string]string) error {
	for _, name := range m.Webcomponents {
		srcDir := filepath.Join("webcomponents", name)
		info, err := os.Stat(srcDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("webcomponent %q: directory %s/ is missing", name, srcDir)
		}
		// The HTML entry point is required by Genero's webcomponent loader.
		entry := filepath.Join(srcDir, name+".html")
		if _, err := os.Stat(entry); err != nil {
			return fmt.Errorf("webcomponent %q: missing required entry point %s", name, entry)
		}

		err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if dirShouldBeSkipped(ignore, path) {
					return filepath.SkipDir
				}
				return nil
			}
			relPath, relErr := filepath.Rel(".", path)
			if relErr != nil {
				relPath = path
			}
			if ignore.shouldExclude(relPath, false) {
				return nil
			}
			// Strip the leading "webcomponents/" so the archive path is
			// <COMPONENTTYPE>/<file> — matching the layout the installer
			// extracts directly into .fglpkg/webcomponents/.
			zipPath, relErr := filepath.Rel("webcomponents", relPath)
			if relErr != nil {
				return fmt.Errorf("cannot compute archive path for %s: %w", relPath, relErr)
			}
			return stageFile(stageDir, filepath.ToSlash(zipPath), path, staged)
		})
		if err != nil {
			return fmt.Errorf("error walking webcomponent %q: %w", name, err)
		}
	}
	return nil
}

// stageDocFiles stages files matching the manifest's Docs globs at their
// project-relative path (no rebasing; docs live at the archive root).
func stageDocFiles(stageDir string, m *manifest.Manifest, ignore *ignoreSet, staged map[string]string) error {
	if len(m.Docs) == 0 {
		return nil
	}
	return filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isPackArtifactDir(path) {
				return filepath.SkipDir
			}
			if dirShouldBeSkipped(ignore, path) {
				return filepath.SkipDir
			}
			return nil
		}
		relPath, relErr := filepath.Rel(".", path)
		if relErr != nil {
			relPath = path
		}
		for _, pattern := range m.Docs {
			if matchGlob(pattern, relPath) {
				if ignore.shouldExclude(relPath, false) {
					break
				}
				if err := stageFile(stageDir, relPath, path, staged); err != nil {
					return err
				}
				break
			}
		}
		return nil
	})
}

// stageIncludeFiles folds each `include` entry into the archive root under its
// basename ("copy into the top of importRoot"). A basename that collides with
// another staged file is reported by stageFile.
func stageIncludeFiles(stageDir string, m *manifest.Manifest, staged map[string]string) error {
	for _, inc := range m.Include {
		info, err := os.Stat(inc)
		if err != nil {
			return fmt.Errorf("include %q not found: %w", inc, err)
		}
		if info.IsDir() {
			return fmt.Errorf("include %q is a directory, not a file", inc)
		}
		if err := stageFile(stageDir, filepath.Base(inc), inc, staged); err != nil {
			return err
		}
	}
	return nil
}

// stagePathFor returns the archive path for a packaged file, rebased under
// importRoot when set. It errors if the file lies outside importRoot (which
// would otherwise produce a "../" escape) — the caller must fix root/importRoot
// or fold the file in via `include`.
func stagePathFor(importRoot, relPath string) (string, error) {
	base := importRoot
	if base == "" {
		base = "."
	}
	rel, err := filepath.Rel(base, relPath)
	if err != nil {
		return "", fmt.Errorf("cannot place %q under importRoot %q: %w", relPath, importRoot, err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("file %q is outside importRoot %q — fix root/importRoot/files or add it to include", relPath, importRoot)
	}
	return rel, nil
}

// stageFile copies srcDiskPath into stageDir at archivePath, creating parent
// directories. Staging two distinct sources at the same archive path is a
// collision (hard error); staging the same source twice is a no-op.
func stageFile(stageDir, archivePath, srcDiskPath string, staged map[string]string) error {
	archivePath = filepath.ToSlash(archivePath)
	if prev, ok := staged[archivePath]; ok {
		if filepath.Clean(prev) == filepath.Clean(srcDiskPath) {
			return nil
		}
		return fmt.Errorf("archive path %q is claimed by both %q and %q", archivePath, prev, srcDiskPath)
	}
	dest := filepath.Join(stageDir, filepath.FromSlash(archivePath))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := copyFile(srcDiskPath, dest); err != nil {
		return fmt.Errorf("cannot stage %s: %w", srcDiskPath, err)
	}
	staged[archivePath] = srcDiskPath
	return nil
}

// copyFile copies the contents of src into dst (created/truncated).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// zipStageDir walks the staged tree, adds every file to a deterministic zip in
// sorted archive-path order, and returns the zip bytes and their SHA256.
// Entries are written with zip.Writer.Create (constant metadata) so the archive
// is reproducible — do NOT switch to a FileInfo-derived header, which would
// stamp the staged copies' mtimes into the archive.
func zipStageDir(stageDir string) ([]byte, string, error) {
	type stagedEntry struct{ archivePath, diskPath string }
	var entries []stagedEntry
	err := filepath.Walk(stageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		entries = append(entries, stagedEntry{filepath.ToSlash(rel), path})
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("cannot read staging directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].archivePath < entries[j].archivePath })

	var buf bytes.Buffer
	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(&buf, h))
	for _, e := range entries {
		if err := addFileToZip(zw, e.diskPath, e.archivePath); err != nil {
			return nil, "", fmt.Errorf("cannot add %s to zip: %w", e.archivePath, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), nil
}

// isPackArtifactDir reports whether a directory should never appear in
// the published zip. The local package cache (.fglpkg/) is the canonical
// case: it holds installed devDependencies, and shipping them turns
// every package into a transitive grab-bag of its developer's tooling.
func isPackArtifactDir(path string) bool {
	return filepath.Base(path) == ".fglpkg"
}

// addFileToZip adds a file at diskPath into the zip using zipPath as
// its name, preserving directory structure.
func addFileToZip(zw *zip.Writer, diskPath, zipPath string) error {
	f, err := os.Open(diskPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Always use forward slashes in zip entries for portability.
	fw, err := zw.Create(filepath.ToSlash(zipPath))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}

// ─── bdl ──────────────────────────────────────────────────────────────────────

func cmdBdl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg bdl <package> <module> [args...]\n       fglpkg bdl --list")
	}

	if args[0] == "--list" || args[0] == "-l" {
		return cmdBdlList()
	}

	if len(args) < 2 {
		return fmt.Errorf("usage: fglpkg bdl <package> <module> [args...]")
	}

	pkgName := args[0]
	moduleName := args[1]
	programArgs := args[2:]

	// Find the package.
	pkgDir, m, err := findInstalledPackage(pkgName)
	if err != nil {
		return err
	}

	// Verify the module is declared in programs.
	found := false
	for _, p := range m.Programs {
		if p == moduleName {
			found = true
			break
		}
	}
	if !found {
		available := "none"
		if len(m.Programs) > 0 {
			available = strings.Join(m.Programs, ", ")
		}
		return fmt.Errorf("module %q is not declared in %s's programs list\nAvailable programs: %s", moduleName, pkgName, available)
	}

	// Derive the working directory from root.
	workDir := pkgDir
	if m.Root != "" {
		workDir = filepath.Join(pkgDir, m.Root)
	}
	if _, err := os.Stat(workDir); err != nil {
		return fmt.Errorf("program directory not found: %s\nTry reinstalling: fglpkg install", workDir)
	}

	// Verify the .42m file exists.
	modulePath := filepath.Join(workDir, moduleName+".42m")
	if _, err := os.Stat(modulePath); err != nil {
		return fmt.Errorf("module file not found: %s", modulePath)
	}

	// Find fglrun.
	fglrunPath, err := genero.FglrunPath()
	if err != nil {
		return err
	}

	// Build the environment.
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	g := env.New(home)

	fglldpath, err := g.BuildFGLLDPATH()
	if err != nil {
		return err
	}
	classpath, err := g.BuildJavaClasspath()
	if err != nil {
		return err
	}

	// Merge with existing env values.
	fglldpath = env.MergeEnvVar(fglldpath, os.Getenv("FGLLDPATH"))
	classpath = env.MergeEnvVar(classpath, os.Getenv("CLASSPATH"))

	// Build the full environment, replacing FGLLDPATH and CLASSPATH.
	cmdEnv := os.Environ()
	cmdEnv = setEnvVar(cmdEnv, "FGLLDPATH", fglldpath)
	if classpath != "" {
		cmdEnv = setEnvVar(cmdEnv, "CLASSPATH", classpath)
	}

	// Execute fglrun.
	cmd := exec.Command(fglrunPath, append([]string{moduleName}, programArgs...)...)
	cmd.Dir = workDir
	cmd.Env = cmdEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("fglrun failed: %w", err)
	}
	return nil
}

func cmdBdlList() error {
	type entry struct {
		Program string
		Package string
		Source  string
	}
	var entries []entry

	scanPackages := func(dir, source string) {
		pkgEntries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range pkgEntries {
			if !e.IsDir() {
				continue
			}
			m, err := manifest.Load(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, p := range m.Programs {
				entries = append(entries, entry{Program: p, Package: m.Name, Source: source})
			}
		}
	}

	// Local packages first.
	wd, _ := os.Getwd()
	localPkgs := filepath.Join(wd, ".fglpkg", "packages")
	scanPackages(localPkgs, "local")

	// Global packages.
	home, err := fglpkgHome()
	if err == nil {
		globalPkgs := filepath.Join(home, "packages")
		if globalPkgs != localPkgs {
			scanPackages(globalPkgs, "global")
		}
	}

	if len(entries) == 0 {
		fmt.Println("No BDL programs found in installed packages.")
		return nil
	}

	fmt.Println("Available BDL programs:")
	fmt.Printf("  %-25s %-25s %s\n", "PROGRAM", "PACKAGE", "SOURCE")
	fmt.Printf("  %-25s %-25s %s\n", "-------", "-------", "------")
	for _, e := range entries {
		fmt.Printf("  %-25s %-25s %s\n", e.Program, e.Package, e.Source)
	}
	return nil
}

// setEnvVar replaces or appends a KEY=VALUE pair in an environment slice.
func setEnvVar(environ []string, key, value string) []string {
	prefix := key + "="
	for i, e := range environ {
		if strings.HasPrefix(e, prefix) {
			environ[i] = prefix + value
			return environ
		}
	}
	return append(environ, prefix+value)
}

// ─── login ────────────────────────────────────────────────────────────────────

// cmdLogin signs the user into the consumer registry.
//
//	fglpkg login                   # browser OAuth (code + PKCE)
//	fglpkg login --token <gpr_…>   # non-interactive: store the supplied PAT
//
// The browser flow registers a one-off public OAuth client via DCR, runs auth
// code + PKCE against the registry, persists the resulting access + refresh
// tokens, and verifies via whoami. The --token flow skips the browser and
// just stores the PAT; whoami is attempted but a failure does not block
// storage (so offline CI bootstrap works).
func cmdLogin(args []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}

	la, err := parseLoginArgs(args)
	if err != nil {
		return err
	}

	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}

	// A --registry naming a non-GI (Artifactory) repository stores credentials
	// per its configured auth scheme, keyed by the repo URL.
	if la.registry != "" && la.registry != config.GIName {
		return loginToRegistry(home, creds, la)
	}

	registryURL := defaultRegistry()
	// FGLPKG_TOKEN is resolved ahead of stored credentials, so a login to GI has
	// no visible effect until the env var is unset. Warn rather than silently
	// storing credentials the user won't see take effect.
	if credentials.ConsumerEnvBearer() != "" {
		fmt.Fprintln(os.Stderr, "  Warning: FGLPKG_TOKEN is set and overrides stored credentials;")
		fmt.Fprintln(os.Stderr, "           this login will not take effect until you unset FGLPKG_TOKEN.")
	}
	if la.token != "" {
		pat := la.token
		if !strings.HasPrefix(pat, "gpr_") {
			fmt.Fprintln(os.Stderr, "  Warning: PAT does not start with 'gpr_' — storing anyway.")
		}
		// An explicit --token login switches this registry to PAT auth. Drop any
		// existing OAuth token so it doesn't keep winning over the new PAT at
		// resolution time (ActiveBearer prefers OAuth ahead of the PAT).
		creds.ClearOAuth(registryURL)
		creds.Set(registryURL, pat, "")
		if err := creds.Save(home); err != nil {
			return err
		}
		who, err := whoamiRequest(registryURL, pat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: token stored but verification failed: %v\n", err)
			fmt.Printf("✓ Token saved for %s\n", registryURL)
			return nil
		}
		fmt.Printf("✓ Logged in to %s as %s\n", registryURL, whoamiSubject(who))
		return nil
	}

	// Browser OAuth flow.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tokens, err := oauth.RunLogin(ctx, registryURL, oauth.LoginConfig{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "  To use a Personal Access Token instead: fglpkg login --token <gpr_…>")
		return fmt.Errorf("login failed: %w", err)
	}
	creds.SetOAuth(registryURL, tokens, "")
	if err := creds.Save(home); err != nil {
		return err
	}
	who, err := whoamiRequest(registryURL, tokens.AccessToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: tokens stored but verification failed: %v\n", err)
		fmt.Printf("✓ Tokens saved for %s\n", registryURL)
		return nil
	}
	fmt.Printf("✓ Logged in to %s as %s\n", registryURL, whoamiSubject(who))
	if tokens.RefreshToken != "" {
		fmt.Println("  Access token will be refreshed automatically while you stay signed in.")
	}
	return nil
}

// loginToRegistry stores credentials for a configured (non-GI) repository
// according to its auth scheme, keyed by the repository URL.
func loginToRegistry(home string, creds *credentials.File, la loginArgs) error {
	reg, err := resolveRegistry(home, la.registry)
	if err != nil {
		return err
	}
	switch reg.Auth {
	case config.AuthBearer:
		if la.token == "" {
			return fmt.Errorf("registry %q uses bearer auth; pass --token <access-token>", reg.Name)
		}
		creds.Set(reg.URL, la.token, "")
	case config.AuthBasic:
		if la.user == "" || la.password == "" {
			return fmt.Errorf("registry %q uses basic auth; pass --user <u> --password <p|token>", reg.Name)
		}
		creds.SetBasic(reg.URL, la.user, la.password)
	case config.AuthAPIKey:
		if la.apiKey == "" {
			return fmt.Errorf("registry %q uses apikey auth; pass --api-key <key>", reg.Name)
		}
		creds.SetAPIKey(reg.URL, la.apiKey)
	case config.AuthAnonymous:
		fmt.Printf("Registry %q uses anonymous access — no login needed.\n", reg.Name)
		return nil
	default:
		return fmt.Errorf("registry %q has unknown auth scheme %q", reg.Name, reg.Auth)
	}
	if err := creds.Save(home); err != nil {
		return err
	}
	fmt.Printf("✓ Credentials saved for registry %q (%s, %s auth)\n", reg.Name, reg.URL, reg.Auth)
	return nil
}

// resolveRegistry finds a configured registry descriptor by logical name,
// consulting the built-in GI entry, the global config, and the project manifest.
func resolveRegistry(home, name string) (config.Registry, error) {
	var projRegs []config.Registry
	if m, err := manifest.Load("."); err == nil {
		projRegs = m.Registries
	}
	regs, err := config.Load(home, os.Getenv("FGLPKG_REGISTRY"), projRegs)
	if err != nil {
		return config.Registry{}, err
	}
	r, ok := config.Find(regs, name)
	if !ok {
		return config.Registry{}, fmt.Errorf(
			"registry %q is not configured (add it to fglpkg.json or ~/.fglpkg/config.json)", name)
	}
	return r, nil
}

// loginArgs is the parsed flag surface of `fglpkg login`.
type loginArgs struct {
	registry string // --registry <name>; "" or "gi" = the default GI registry
	token    string // --token: GI PAT, or an Artifactory bearer access token
	user     string // --user: Artifactory basic username
	password string // --password: Artifactory basic secret (password or token)
	apiKey   string // --api-key: Artifactory API key
}

// parseLoginArgs reads the flag surface of `fglpkg login`/`logout`.
func parseLoginArgs(args []string) (la loginArgs, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		need := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			return strings.TrimSpace(args[i+1]), nil
		}
		switch a {
		case "--registry":
			if la.registry, err = need(); err != nil {
				return loginArgs{}, err
			}
			i += 2
		case "--token":
			if la.token, err = need(); err != nil {
				return loginArgs{}, err
			}
			i += 2
		case "--user":
			if la.user, err = need(); err != nil {
				return loginArgs{}, err
			}
			i += 2
		case "--password":
			if la.password, err = need(); err != nil {
				return loginArgs{}, err
			}
			i += 2
		case "--api-key":
			if la.apiKey, err = need(); err != nil {
				return loginArgs{}, err
			}
			i += 2
		default:
			return loginArgs{}, fmt.Errorf("unknown argument %q\nusage: fglpkg login [--registry <name>] [--token <PAT>] [--user <u> --password <p>] [--api-key <k>]", a)
		}
	}
	return la, nil
}

// whoamiSubject returns a one-line subject for "Logged in as …" messages.
// Prefers "Name <email>", falls back to email, then User.ID, then "(user)".
func whoamiSubject(w whoamiResult) string {
	name := strings.TrimSpace(w.User.Name)
	email := strings.TrimSpace(w.User.Email)
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("%s <%s>", name, email)
	case email != "":
		return email
	case name != "":
		return name
	case w.User.ID != "":
		return w.User.ID
	default:
		return "(user)"
	}
}

// ─── logout ───────────────────────────────────────────────────────────────────

func cmdLogout(args []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	la, err := parseLoginArgs(args)
	if err != nil {
		return err
	}
	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}
	registryURL := defaultRegistry()
	isGI := la.registry == "" || la.registry == config.GIName
	if !isGI {
		reg, err := resolveRegistry(home, la.registry)
		if err != nil {
			return err
		}
		registryURL = reg.URL
	}
	// FGLPKG_TOKEN authenticates GI ahead of stored credentials and cannot be
	// removed by fglpkg (it lives in the environment). Warn so the user isn't
	// surprised that whoami still works after logging out.
	envNote := func() {
		if isGI && credentials.ConsumerEnvBearer() != "" {
			fmt.Fprintln(os.Stderr, "  Note: FGLPKG_TOKEN is set in your environment and still authenticates you.")
			fmt.Fprintln(os.Stderr, "        Unset FGLPKG_TOKEN to fully log out.")
		}
	}
	if _, ok := creds.Get(registryURL); !ok {
		fmt.Printf("Not logged in to %s\n", registryURL)
		envNote()
		return nil
	}
	creds.Delete(registryURL)
	if err := creds.Save(home); err != nil {
		return err
	}
	fmt.Printf("✓ Logged out from %s\n", registryURL)
	envNote()
	return nil
}

// ─── whoami ───────────────────────────────────────────────────────────────────

func cmdWhoami(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := defaultRegistry()
	token, err := credentials.ActiveBearer(context.Background(), home, registryURL, oauth.Refresh)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' first", registryURL)
	}
	who, err := whoamiRequest(registryURL, token)
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}
	fmt.Printf("Registry: %s\n", registryURL)
	fmt.Printf("User:     %s\n", whoamiSubject(who))
	// Report where the active credential came from — the env var overrides
	// stored login, which is why logout/login can appear to have no effect.
	if credentials.ConsumerEnvBearer() != "" {
		fmt.Println("Auth:     FGLPKG_TOKEN (environment variable)")
	} else {
		fmt.Println("Auth:     stored login (~/.fglpkg/credentials.json)")
	}
	if who.Partner != nil {
		fmt.Printf("Partner:  %s\n", who.Partner.Name)
	} else {
		fmt.Println("Partner:  (none)")
	}
	if len(who.Scopes) > 0 {
		fmt.Printf("Scopes:   %s\n", strings.Join(who.Scopes, ", "))
	} else {
		fmt.Println("Scopes:   (none)")
	}
	return nil
}

func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo format, got %q", s)
	}
	return parts[0], parts[1], nil
}

// ─── registry ───────────────────────────────────────────────────────────────

func cmdRegistry(args []string) error {
	if len(args) == 0 {
		return cmdRegistryList()
	}
	switch args[0] {
	case "list":
		return cmdRegistryList()
	case "add":
		return cmdRegistryAdd(args[1:])
	case "remove", "rm":
		return cmdRegistryRemove(args[1:])
	default:
		return fmt.Errorf("unknown registry subcommand %q\nusage: fglpkg registry <list|add|remove>", args[0])
	}
}

// registryEditFlags carries the parsed flags for `registry add`.
type registryEditFlags struct {
	name     string
	url      string
	typ      string
	repoKey  string
	auth     string
	priority *int // nil = unset (auto-assign); an explicit value (incl. 0) is validated
	packages []string
	project  bool // write to the project fglpkg.json instead of the global config
}

const registryAddUsage = "usage: fglpkg registry add <name> <url> " +
	"[--type genero|artifactory] [--repo-key K] [--auth bearer|basic|apikey|anonymous] " +
	"[--priority N] [--packages 'acme-*,foo-*'] [--project]"

func parseRegistryAddFlags(args []string) (registryEditFlags, error) {
	f := registryEditFlags{typ: config.TypeArtifactory}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--project":
			f.project = true
		case a == "--type" || strings.HasPrefix(a, "--type="):
			v, err := flagValue(a, &i, args)
			if err != nil {
				return f, err
			}
			f.typ = v
		case a == "--repo-key" || strings.HasPrefix(a, "--repo-key="):
			v, err := flagValue(a, &i, args)
			if err != nil {
				return f, err
			}
			f.repoKey = v
		case a == "--auth" || strings.HasPrefix(a, "--auth="):
			v, err := flagValue(a, &i, args)
			if err != nil {
				return f, err
			}
			f.auth = v
		case a == "--priority" || strings.HasPrefix(a, "--priority="):
			v, err := flagValue(a, &i, args)
			if err != nil {
				return f, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return f, fmt.Errorf("--priority must be an integer, got %q", v)
			}
			f.priority = &n
		case a == "--packages" || strings.HasPrefix(a, "--packages="):
			v, err := flagValue(a, &i, args)
			if err != nil {
				return f, err
			}
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					f.packages = append(f.packages, p)
				}
			}
		case strings.HasPrefix(a, "-"):
			return f, fmt.Errorf("unknown flag %q\n%s", a, registryAddUsage)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) != 2 {
		return f, fmt.Errorf("registry add needs exactly <name> and <url>\n%s", registryAddUsage)
	}
	f.name, f.url = positional[0], positional[1]
	return f, nil
}

// flagValue resolves either "--flag value" (advancing i) or "--flag=value".
func flagValue(a string, i *int, args []string) (string, error) {
	if v, ok := strings.CutPrefix(a, "--"); ok {
		if eq := strings.IndexByte(v, '='); eq >= 0 {
			return v[eq+1:], nil
		}
	}
	*i++
	if *i >= len(args) {
		return "", fmt.Errorf("%s requires a value", a)
	}
	return args[*i], nil
}

func cmdRegistryAdd(args []string) error {
	f, err := parseRegistryAddFlags(args)
	if err != nil {
		return err
	}
	if f.name == config.GIName {
		return fmt.Errorf("%q is the built-in registry and cannot be redefined", config.GIName)
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	env := os.Getenv("FGLPKG_REGISTRY")

	// Current effective set: reject a duplicate name and auto-assign a priority
	// when none was given (max existing + 1) so the common case needs no --priority.
	current, err := config.Load(home, env, projectRegistries())
	if err != nil {
		return err
	}
	if _, dup := config.Find(current, f.name); dup {
		return fmt.Errorf("a registry named %q already exists; run 'fglpkg registry remove %s' first", f.name, f.name)
	}
	// A nil priority means the flag was omitted → auto-assign max+1 so the common
	// case needs no --priority. An explicit value (including 0) is left as-is and
	// validated by config.Resolve, which rejects any priority < 1. (GIS-249)
	if f.priority == nil {
		max := 0
		for _, r := range current {
			if r.Priority > max {
				max = r.Priority
			}
		}
		p := max + 1
		f.priority = &p
	}

	r := config.Registry{
		Name:     f.name,
		Type:     f.typ,
		URL:      f.url,
		RepoKey:  f.repoKey,
		Priority: *f.priority,
		Auth:     f.auth,
		Packages: f.packages,
	}

	// Validate against the prospective effective set (type/auth/repoKey and
	// priority-uniqueness) before writing anything.
	if f.project {
		m, err := manifest.Load(".")
		if err != nil {
			return fmt.Errorf("registry add --project requires an fglpkg.json in the current directory: %w", err)
		}
		proj := append(append([]config.Registry(nil), m.Registries...), r)
		global, err := config.LoadGlobal(home)
		if err != nil {
			return err
		}
		if _, err := config.Resolve(config.BuiltinGI(env), global, proj); err != nil {
			return err
		}
		m.Registries = proj
		if err := m.Save("."); err != nil {
			return err
		}
		fmt.Printf("✓ Added registry %q to %s\n", f.name, manifest.Filename)
		return nil
	}

	g, err := config.LoadGlobalFile(home)
	if err != nil {
		return err
	}
	global := append(append([]config.Registry(nil), g.Registries...), r)
	if _, err := config.Resolve(config.BuiltinGI(env), global, projectRegistries()); err != nil {
		return err
	}
	g.Registries = global
	if err := config.WriteGlobalFile(home, g); err != nil {
		return err
	}
	fmt.Printf("✓ Added registry %q to %s\n", f.name, filepath.Join(home, config.GlobalFilename))
	if r.Auth != config.AuthAnonymous {
		fmt.Printf("  Run 'fglpkg login --registry %s' to store credentials.\n", f.name)
	}
	return nil
}

func cmdRegistryRemove(args []string) error {
	var name string
	project := false
	for _, a := range args {
		switch {
		case a == "--project":
			project = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q\nusage: fglpkg registry remove <name> [--project]", a)
		case name == "":
			name = a
		default:
			return fmt.Errorf("registry remove takes a single <name>")
		}
	}
	if name == "" {
		return fmt.Errorf("usage: fglpkg registry remove <name> [--project]")
	}
	if name == config.GIName {
		return fmt.Errorf("the built-in %q registry cannot be removed", config.GIName)
	}

	if project {
		m, err := manifest.Load(".")
		if err != nil {
			return fmt.Errorf("registry remove --project requires an fglpkg.json in the current directory: %w", err)
		}
		kept, removed := dropRegistry(m.Registries, name)
		if !removed {
			return fmt.Errorf("no registry named %q in %s", name, manifest.Filename)
		}
		m.Registries = kept
		// Clear a now-dangling default, mirroring the global branch below: a bare
		// `publish` resolving the removed name would otherwise fail. (GIS-249 C3)
		if m.DefaultRegistry == name {
			m.DefaultRegistry = ""
		}
		if err := m.Save("."); err != nil {
			return err
		}
		fmt.Printf("✓ Removed registry %q from %s\n", name, manifest.Filename)
		return nil
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	g, err := config.LoadGlobalFile(home)
	if err != nil {
		return err
	}
	kept, removed := dropRegistry(g.Registries, name)
	if !removed {
		return fmt.Errorf("no registry named %q in %s (use --project to remove one declared in fglpkg.json)",
			name, config.GlobalFilename)
	}
	g.Registries = kept
	if g.DefaultRegistry == name {
		g.DefaultRegistry = ""
	}
	if err := config.WriteGlobalFile(home, g); err != nil {
		return err
	}
	fmt.Printf("✓ Removed registry %q from %s\n", name, filepath.Join(home, config.GlobalFilename))
	return nil
}

// dropRegistry returns regs without the named entry and whether it was present.
func dropRegistry(regs []config.Registry, name string) ([]config.Registry, bool) {
	kept := make([]config.Registry, 0, len(regs))
	removed := false
	for _, r := range regs {
		if r.Name == name {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	return kept, removed
}

// projectRegistries returns the current project's declared registries, or nil
// when there is no manifest in the working directory.
func projectRegistries() []config.Registry {
	if m, err := manifest.Load("."); err == nil {
		return m.Registries
	}
	return nil
}

func cmdRegistryList() error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	var projRegs []config.Registry
	if m, err := manifest.Load("."); err == nil {
		projRegs = m.Registries
	}
	regs, err := config.Load(home, os.Getenv("FGLPKG_REGISTRY"), projRegs)
	if err != nil {
		return err
	}
	creds, _ := credentials.Load(home)

	fmt.Printf("%-16s %-12s %-4s %-9s %-6s %s\n", "NAME", "TYPE", "PRIO", "AUTH", "LOGIN", "URL")
	for _, r := range regs {
		fmt.Printf("%-16s %-12s %-4d %-9s %-6s %s\n",
			r.Name, r.Type, r.Priority, r.Auth, registryLoginStatus(creds, r), r.URL)
	}
	return nil
}

// registryLoginStatus reports whether usable credentials exist for a registry.
func registryLoginStatus(creds *credentials.File, r config.Registry) string {
	if r.Auth == config.AuthAnonymous {
		return "anon"
	}
	// FGLPKG_TOKEN is the GI/consumer bearer honoured first by ActiveBearer, so
	// a genero registry is authenticated by it even with no stored credentials.
	// It does not apply to Artifactory repos (those auth via stored headers).
	if r.Type == config.TypeGenero && credentials.ConsumerEnvBearer() != "" {
		return "env"
	}
	if creds == nil {
		return "no"
	}
	if len(creds.AuthHeaders(r.URL, r.Auth)) > 0 {
		return "yes"
	}
	// GI bearer may be OAuth, which AuthHeaders does not cover.
	if e, ok := creds.Get(r.URL); ok && (e.OAuth != nil || e.Pat != "" || e.APIKey != "") {
		return "yes"
	}
	return "no"
}

// ─── workspace ────────────────────────────────────────────────────────────────

func cmdWorkspace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg workspace <init|add|list|info>")
	}
	switch args[0] {
	case "init":
		return cmdWorkspaceInit(args[1:])
	case "add":
		return cmdWorkspaceAdd(args[1:])
	case "list":
		return cmdWorkspaceList()
	case "info":
		return cmdWorkspaceInfo()
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func cmdWorkspaceInit(args []string) error {
	if workspace.Exists(".") {
		return fmt.Errorf("%s already exists in the current directory", workspace.WorkspaceFilename)
	}
	if err := workspace.Init(".", args); err != nil {
		return err
	}
	fmt.Printf("✓ Created %s\n", workspace.WorkspaceFilename)
	return nil
}

func cmdWorkspaceAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg workspace add <path>")
	}
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace — run 'fglpkg workspace init' first")
	}
	for _, path := range args {
		if err := workspace.AddMember(wsRoot, path); err != nil {
			return err
		}
		fmt.Printf("✓ Added %q to workspace\n", path)
	}
	return nil
}

func cmdWorkspaceList() error {
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace")
	}
	ws, err := workspace.Load(wsRoot)
	if err != nil {
		return err
	}
	fmt.Printf("Workspace: %s\n", wsRoot)
	for _, m := range ws.Members {
		fmt.Printf("  %-30s v%s\n", m.Manifest.Name, m.Manifest.Version)
	}
	return nil
}

func cmdWorkspaceInfo() error {
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace")
	}
	ws, err := workspace.Load(wsRoot)
	if err != nil {
		return err
	}
	fmt.Print(ws.Summary())
	return nil
}

// ─── run ─────────────────────────────────────────────────────────────────────

func cmdRun(args []string) error {
	if len(args) > 0 && (args[0] == "--list" || args[0] == "-l") {
		return cmdRunList()
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg run <command> [-- args...]\n       fglpkg run --list")
	}

	commandName := args[0]

	// Split on "--" to separate fglpkg args from script args.
	var scriptArgs []string
	for i, a := range args[1:] {
		if a == "--" {
			scriptArgs = args[i+2:]
			break
		}
	}
	// If no "--" found, pass remaining args directly.
	if scriptArgs == nil && len(args) > 1 {
		scriptArgs = args[1:]
	}

	scriptPath, pkgName, err := findBinCommand(commandName)
	if err != nil {
		return err
	}

	fmt.Printf("Running %q from package %s...\n", commandName, pkgName)

	cmd, err := buildScriptCommand(scriptPath, scriptArgs)
	if err != nil {
		return err
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// buildScriptCommand creates an exec.Cmd appropriate for the current OS.
// On Unix, the script is executed directly (relying on the shebang line).
// On Windows, the interpreter is selected based on the file extension.
func buildScriptCommand(scriptPath string, args []string) (*exec.Cmd, error) {
	if runtime.GOOS != "windows" {
		return exec.Command(scriptPath, args...), nil
	}

	ext := strings.ToLower(filepath.Ext(scriptPath))
	switch ext {
	case ".bat", ".cmd":
		// Native Windows batch — run via cmd.exe /C.
		cmdArgs := append([]string{"/C", scriptPath}, args...)
		return exec.Command("cmd.exe", cmdArgs...), nil
	case ".ps1":
		// PowerShell script.
		cmdArgs := append([]string{"-ExecutionPolicy", "Bypass", "-File", scriptPath}, args...)
		return exec.Command("powershell.exe", cmdArgs...), nil
	case ".py":
		cmdArgs := append([]string{scriptPath}, args...)
		return exec.Command("python", cmdArgs...), nil
	case ".sh":
		cmdArgs := append([]string{scriptPath}, args...)
		return exec.Command("bash", cmdArgs...), nil
	case ".exe":
		return exec.Command(scriptPath, args...), nil
	default:
		return nil, fmt.Errorf(
			"cannot run %q on Windows: unsupported file extension %q\n"+
				"Supported extensions: .bat, .cmd, .ps1, .py, .sh, .exe",
			filepath.Base(scriptPath), ext,
		)
	}
}

func cmdRunList() error {
	type entry struct {
		command string
		pkgName string
		source  string
		script  string
	}
	var entries []entry

	scanPackagesDir := func(packagesDir, source string) {
		dirEntries, err := os.ReadDir(packagesDir)
		if err != nil {
			return
		}
		for _, e := range dirEntries {
			if !e.IsDir() {
				continue
			}
			pkgDir := filepath.Join(packagesDir, e.Name())
			m, err := manifest.Load(pkgDir)
			if err != nil {
				continue
			}
			// Sort command names for deterministic output.
			cmds := make([]string, 0, len(m.Bin))
			for cmd := range m.Bin {
				cmds = append(cmds, cmd)
			}
			sort.Strings(cmds)
			for _, cmd := range cmds {
				entries = append(entries, entry{
					command: cmd,
					pkgName: m.Name,
					source:  source,
					script:  m.Bin[cmd],
				})
			}
		}
	}

	if isProjectDir() {
		wd, _ := os.Getwd()
		scanPackagesDir(filepath.Join(wd, ".fglpkg", "packages"), "local")
	}
	globalHome, err := fglpkgHome()
	if err == nil {
		scanPackagesDir(filepath.Join(globalHome, "packages"), "global")
	}

	if len(entries) == 0 {
		fmt.Println("No commands available.")
		fmt.Println("Packages can define commands via the \"bin\" field in fglpkg.json")
		return nil
	}

	fmt.Println("Available commands:")
	fmt.Printf("  %-20s %-20s %-10s %s\n", "COMMAND", "PACKAGE", "SOURCE", "SCRIPT")
	fmt.Printf("  %-20s %-20s %-10s %s\n", "-------", "-------", "------", "------")
	for _, e := range entries {
		fmt.Printf("  %-20s %-20s %-10s %s\n", e.command, e.pkgName, e.source, e.script)
	}
	return nil
}

// findBinCommand scans installed packages (local first, then global) for
// a bin command matching the given name. Returns the full path to the
// script and the owning package name.
func findBinCommand(commandName string) (scriptPath, pkgName string, err error) {
	type match struct {
		scriptPath string
		pkgName    string
	}
	var matches []match

	scanPackagesDir := func(packagesDir string) {
		entries, readErr := os.ReadDir(packagesDir)
		if readErr != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pkgDir := filepath.Join(packagesDir, e.Name())
			m, loadErr := manifest.Load(pkgDir)
			if loadErr != nil {
				continue
			}
			if scriptRel, ok := m.Bin[commandName]; ok {
				fullPath := filepath.Join(pkgDir, scriptRel)
				if _, statErr := os.Stat(fullPath); statErr == nil {
					matches = append(matches, match{
						scriptPath: fullPath,
						pkgName:    m.Name,
					})
				}
			}
		}
	}

	globalHome, homeErr := fglpkgHome()
	globalPkgs := ""
	if homeErr == nil {
		globalPkgs = filepath.Join(globalHome, "packages")
	}

	// Scan local packages first (higher priority).
	if isProjectDir() {
		wd, _ := os.Getwd()
		localPkgs := filepath.Join(wd, ".fglpkg", "packages")
		scanPackagesDir(localPkgs)
		// Scan global if different from local.
		if globalPkgs != "" && globalPkgs != localPkgs {
			scanPackagesDir(globalPkgs)
		}
	} else if globalPkgs != "" {
		scanPackagesDir(globalPkgs)
	}

	if len(matches) == 0 {
		return "", "", fmt.Errorf("command %q not found in any installed package\nRun 'fglpkg run --list' to see available commands", commandName)
	}
	if len(matches) > 1 {
		var names []string
		for _, m := range matches {
			names = append(names, m.pkgName)
		}
		return "", "", fmt.Errorf("command %q is defined by multiple packages: %s\nRemove or rename conflicting packages to resolve", commandName, strings.Join(names, ", "))
	}

	return matches[0].scriptPath, matches[0].pkgName, nil
}

// ─── docs ────────────────────────────────────────────────────────────────────

func cmdDocs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg docs <package> [file]")
	}

	pkgName := args[0]

	pkgDir, m, err := findInstalledPackage(pkgName)
	if err != nil {
		return err
	}

	if len(m.Docs) == 0 {
		fmt.Printf("Package %q does not declare any documentation files.\n", pkgName)
		return nil
	}

	docFiles, err := collectDocFiles(pkgDir, m.Docs)
	if err != nil {
		return err
	}

	if len(docFiles) == 0 {
		fmt.Printf("Package %q declares doc patterns but no matching files were found.\n", pkgName)
		return nil
	}

	// If no specific file requested and there's only one doc, show it directly.
	if len(args) < 2 {
		if len(docFiles) == 1 {
			fullPath := filepath.Join(pkgDir, docFiles[0])
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("cannot read %s: %w", docFiles[0], err)
			}
			fmt.Print(string(content))
			return nil
		}
		fmt.Printf("Documentation for %s@%s:\n", m.Name, m.Version)
		for _, f := range docFiles {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("\nView a file: fglpkg docs %s <file>\n", pkgName)
		return nil
	}

	// Display a specific doc file.
	requestedFile := args[1]

	var matchPath string
	for _, f := range docFiles {
		if f == requestedFile || filepath.Base(f) == requestedFile {
			matchPath = f
			break
		}
	}
	if matchPath == "" {
		return fmt.Errorf("doc file %q not found in package %s\nRun 'fglpkg docs %s' to list available files", requestedFile, pkgName, pkgName)
	}

	fullPath := filepath.Join(pkgDir, matchPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", matchPath, err)
	}
	fmt.Print(string(content))
	return nil
}

// findInstalledPackage looks for a package by name, checking local then global.
// Returns the package directory, its manifest, and an error.
func findInstalledPackage(name string) (string, *manifest.Manifest, error) {
	// Packages are installed under their canonical slug (GIS-271), so accept any
	// spelling the user types — under_score_test, Under-Score-Test — and look up
	// the canonical directory the resolver/installer actually wrote.
	slug := slugutil.Canonical(name)
	if isProjectDir() {
		wd, _ := os.Getwd()
		localDir := filepath.Join(wd, ".fglpkg", "packages", slug)
		if m, err := manifest.Load(localDir); err == nil {
			return localDir, m, nil
		}
	}
	globalHome, err := fglpkgHome()
	if err == nil {
		globalDir := filepath.Join(globalHome, "packages", slug)
		if m, err := manifest.Load(globalDir); err == nil {
			return globalDir, m, nil
		}
	}
	return "", nil, fmt.Errorf("package %q is not installed\nRun 'fglpkg install %s' first", name, name)
}

// collectDocFiles walks the package directory and returns paths (relative to
// pkgDir) of all files matching any of the given glob patterns.
func collectDocFiles(pkgDir string, patterns []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)

	err := filepath.Walk(pkgDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, relErr := filepath.Rel(pkgDir, path)
		if relErr != nil {
			return nil
		}
		if seen[relPath] {
			return nil
		}
		for _, pattern := range patterns {
			if matchGlob(pattern, relPath) {
				files = append(files, relPath)
				seen[relPath] = true
				break
			}
		}
		return nil
	})
	return files, err
}

// ─── Auth HTTP helpers ────────────────────────────────────────────────────────

// whoamiResult is the merged view of /registry/whoami (new protocol) and
// /auth/whoami (legacy). Empty fields are rendered as "(none)".
type whoamiResult struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	Partner *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"partner"`
	Scopes []string `json:"scopes"`

	// Legacy fields surfaced by /auth/whoami.
	Username string `json:"username,omitempty"`
}

// whoamiRequest probes the consumer registry's /registry/whoami endpoint,
// falling back to /auth/whoami on 404. The legacy response only carries a
// username; we surface it via User.Name so the output formatter has
// something to print.
func whoamiRequest(registryURL, token string) (whoamiResult, error) {
	base := strings.TrimRight(registryURL, "/")
	res, err := whoamiFetch(base+"/registry/whoami", token)
	if err == nil {
		return res, nil
	}
	// Fall back to /auth/whoami only on 404 — other statuses indicate a
	// real failure that the new endpoint already surfaced.
	if err != errNotFound {
		return whoamiResult{}, err
	}
	legacy, err := whoamiFetch(base+"/auth/whoami", token)
	if err != nil {
		return whoamiResult{}, err
	}
	if legacy.Username != "" && legacy.User.Name == "" {
		legacy.User.Name = legacy.Username
	}
	return legacy, nil
}

func whoamiFetch(u, token string) (whoamiResult, error) {
	resp, err := authGet(u, token)
	if err != nil {
		return whoamiResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return whoamiResult{}, errNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return whoamiResult{}, fmt.Errorf("invalid or expired token")
	}
	if resp.StatusCode != http.StatusOK {
		return whoamiResult{}, registryError(resp)
	}
	var out whoamiResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return whoamiResult{}, fmt.Errorf("invalid whoami response: %w", err)
	}
	return out, nil
}

var errNotFound = fmt.Errorf("not found")

func authGet(url, token string) (*http.Response, error) {
	return authDo(http.MethodGet, url, token, "")
}

func authPost(url, token, body string) (*http.Response, error) {
	return authDo(http.MethodPost, url, token, body)
}

func authDo(method, url, token, body string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func registryError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
	if e.Error != "" {
		return fmt.Errorf("registry error (%d): %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
}

// ─── Utilities ────────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Print(`fglpkg - Genero BDL Package Manager

USAGE:
  fglpkg <command> [arguments]

COMMANDS:
`)
	for _, c := range commands {
		name := c.Name
		if c.Args != "" {
			name += " " + c.Args
		}
		// The list entry is Summary plus its list-only ListDetail; either may
		// contain newlines. The first line prints beside the command name, the
		// rest hang-indent under the description column.
		lines := strings.Split(c.Summary+c.ListDetail, "\n")
		fmt.Printf("  %-18s%s\n", name, lines[0])
		for _, cont := range lines[1:] {
			fmt.Printf("  %-18s%s\n", "", cont)
		}
	}
	fmt.Print(`
Run 'fglpkg <command> --help' for command-specific options.

ENVIRONMENT:
  FGLPKG_HOME              Override ~/.fglpkg
  FGLPKG_REGISTRY          GI registry URL for install/search/audit/whoami/publish.
                           Default: https://service.generointelligence.ai
  FGLPKG_TOKEN             Bearer token for the GI registry. Takes precedence over
                           stored login; cannot be cleared by 'fglpkg logout'.
  FGLPKG_PUBLISH_REGISTRY  Name of the repository 'fglpkg publish' targets when no
                           --registry is given (overrides fglpkg.json defaultRegistry)
  FGLPKG_GENERO_VERSION    Override Genero version detection
  FGLPKG_INSTALL_CONCURRENCY  Cap parallel downloads during install (default 4)

`)
	if runtime.GOOS == "windows" {
		fmt.Print(`SETUP:
  PowerShell:    fglpkg env --global | Invoke-Expression
  Command Prompt: run "fglpkg env --global" and set the displayed variables

`)
	} else {
		fmt.Print(`SETUP:
  Add to ~/.bashrc:  eval "$(fglpkg env --global)"

`)
	}
}

func fglpkgHome() (string, error) {
	if h := os.Getenv("FGLPKG_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".fglpkg"), nil
}

func newInstaller(home string, m *manifest.Manifest) *installer.Installer {
	inst, _ := buildInstaller(home, m)
	return inst
}

// buildInstaller is newInstaller's core, additionally returning the
// RepositorySet backing the installer's fetchers (nil in the single-registry
// case). Callers that need to constrain resolution — e.g. `update --registry`
// — use the returned set to call Restrict before installing.
func buildInstaller(home string, m *manifest.Manifest) (*installer.Installer, *provider.RepositorySet) {
	// Always look up credentials from the global home directory, even when
	// installing to a local project directory (--local).
	globalHome, err := fglpkgHome()
	if err != nil {
		globalHome = home
	}
	registryURL := defaultRegistry()
	githubToken := credentials.GitHubTokenFor(globalHome, registryURL)
	registryToken, _ := credentials.ActiveBearer(context.Background(), globalHome, registryURL, oauth.Refresh)
	inst := installer.New(home, githubToken, registryToken, registryURL)

	// Engage multi-provider routing only when repositories beyond the built-in
	// GI registry are configured — otherwise the single-registry path stays
	// byte-identical (no Source stamped in the lockfile).
	var set *provider.RepositorySet
	if rs, repoAuth, regNames, err := buildRepositorySet(globalHome, m); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring registries config: %v\n", err)
	} else {
		// Record the configured names in every case (even single-registry) so a
		// lock referencing a since-removed repository is rejected (§9).
		inst = inst.WithConfiguredRegistries(regNames)
		if rs != nil {
			set = rs
			inst = inst.WithFetchers(rs.Versions, rs.Info).WithRepoAuth(repoAuth).WithPinDeclarer(rs)
		}
	}

	// Layer 1 signature verification. The keys manifest is cached in the
	// global home even for a local install. Mode comes from FGLPKG_SIGNING /
	// config.json / the built-in default (warn).
	inst.WithSigning(signing.EnforceMode(globalHome), globalHome, registryURL)
	return inst, set
}

// buildRepositorySet resolves the configured registries and, when more than the
// built-in GI registry is present, builds a RepositorySet plus the per-repo
// download auth. Returns (nil, nil, nil) for the single-registry case.
func buildRepositorySet(globalHome string, m *manifest.Manifest) (*provider.RepositorySet, []installer.RepoAuth, []string, error) {
	var projRegs []config.Registry
	var pins map[string]string
	if m != nil {
		projRegs = m.Registries
		pins = collectFGLPins(m)
	}
	regs, err := config.Load(globalHome, os.Getenv("FGLPKG_REGISTRY"), projRegs)
	if err != nil {
		return nil, nil, nil, err
	}
	regNames := make([]string, 0, len(regs))
	for _, r := range regs {
		regNames = append(regNames, r.Name)
	}
	if len(regs) <= 1 {
		return nil, nil, regNames, nil // only the built-in gi — keep legacy behaviour
	}

	creds, _ := credentials.Load(globalHome)
	var provs []provider.Provider
	var repoAuth []installer.RepoAuth
	for _, r := range regs {
		switch r.Type {
		case config.TypeArtifactory:
			var headers map[string]string
			if creds != nil {
				headers = creds.AuthHeaders(r.URL, r.Auth)
			}
			provs = append(provs, provider.NewArtifactoryProvider(r, nil, headersApplier(headers)))
			// Register the repo's URL prefix even when anonymous (headers may be
			// empty): the installer must recognise the host as a configured
			// secondary repo so it never falls back to sending the GI registry
			// bearer there (GIS-267).
			repoAuth = append(repoAuth, installer.RepoAuth{URLPrefix: r.URL, Headers: headers})
		default: // genero
			provs = append(provs, provider.NewGeneroProvider(r.Name))
		}
	}
	return provider.NewRepositorySet(provs, regs, pins), repoAuth, regNames, nil
}

// headersApplier turns a fixed header map into a provider.AuthApplier (nil when
// there are no headers, i.e. anonymous).
func headersApplier(headers map[string]string) provider.AuthApplier {
	if len(headers) == 0 {
		return nil
	}
	return func(req *http.Request) {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
}

// collectFGLPins merges the per-dependency registry pins from every scope.
func collectFGLPins(m *manifest.Manifest) map[string]string {
	pins := map[string]string{}
	for _, d := range []manifest.Dependencies{m.Dependencies, m.DevDependencies, m.OptionalDependencies} {
		for name, reg := range d.FGLPins {
			pins[slugutil.Canonical(name)] = reg
		}
	}
	return pins
}

// defaultRegistry returns the consumer registry URL — install, search,
// audit, info, outdated, list, env, whoami, login, logout. Override with
// FGLPKG_REGISTRY.
func defaultRegistry() string {
	if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return "https://service.generointelligence.ai"
}

func defaultPublishRegistry() string {
	return defaultRegistry()
}

// resolveDefaultPublishRegistry returns the logical name of the repository
// `fglpkg publish` should target when no --registry flag is given, in
// decreasing precedence: FGLPKG_PUBLISH_REGISTRY, the project manifest's
// defaultRegistry, then the global config's defaultRegistry. Returns "" when
// none is set — the caller then publishes to GI as before. This is a
// publish-only default and never influences consume-side routing.
func resolveDefaultPublishRegistry(home string, m *manifest.Manifest) string {
	if v := strings.TrimSpace(os.Getenv("FGLPKG_PUBLISH_REGISTRY")); v != "" {
		return v
	}
	if m != nil && m.DefaultRegistry != "" {
		return m.DefaultRegistry
	}
	if v, err := config.GlobalDefaultRegistry(home); err == nil && v != "" {
		return v
	}
	return ""
}

func parsePackageArg(arg string) (name, version string, err error) {
	for i, c := range arg {
		if c == '@' && i > 0 {
			return arg[:i], arg[i+1:], nil
		}
	}
	return arg, "latest", nil
}

func filepathBase() string {
	dir, _ := os.Getwd()
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			return dir[i+1:]
		}
	}
	return dir
}

// matchGlob matches a path against a glob pattern, with support for "**"
// to match any number of directory levels.  For simple patterns (no "**")
// it also tries matching just the file's basename.
// matchGlob tests whether path matches pattern. Patterns are anchored to
// the project root: "USERGUIDE.md" matches only the root-level file, not
// any nested USERGUIDE.md. Use "**/USERGUIDE.md" to match at any depth.
// (Earlier versions silently fell back to matching pattern against the
// basename, which let a devDependency's USERGUIDE.md sneak into a parent
// project's published zip — see buildPackageZip.)
func matchGlob(pattern, p string) bool {
	// Normalise separators, then match with the "path" package (always
	// "/"-based) rather than "path/filepath", whose separator is "\" on
	// Windows — there "*" would match across "/" and over-match.
	pattern = filepath.ToSlash(pattern)
	p = filepath.ToSlash(p)

	if !strings.Contains(pattern, "**") {
		matched, _ := path.Match(pattern, p)
		return matched
	}

	// Split on the first "**" occurrence.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimRight(parts[0], "/")
	suffix := strings.TrimLeft(parts[1], "/")

	// Check prefix: the path must start with the prefix directory (if any).
	if prefix != "" {
		if !strings.HasPrefix(p, prefix+"/") && p != prefix {
			return false
		}
	}

	if suffix == "" {
		return true
	}

	// The remaining path (after prefix) must end with a segment matching suffix.
	remaining := p
	if prefix != "" {
		remaining = strings.TrimPrefix(p, prefix+"/")
	}
	matched, _ := path.Match(suffix, path.Base(remaining))
	return matched
}

// promptWithDefault prints a prompt and reads a full line from stdin,
// supporting spaces in the input. Returns def if the user presses enter
// without typing anything.
func promptWithDefault(label, def string) string {
	if def != "" {
		fmt.Printf("%s (%s): ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	val, err := reader.ReadString('\n')
	if err != nil && len(val) == 0 {
		return def
	}
	// Trim CR and LF to handle both Unix (\n) and Windows (\r\n) line endings.
	val = strings.TrimRight(val, "\r\n")
	if val == "" {
		return def
	}
	return val
}

// promptPackageSlug prompts for the package name and re-prompts until the entry
// normalizes to a valid registry slug — after lowercasing and collapsing runs
// of '.'/'_'/'-' (PEP 503; see internal/slug) it must be 2-64 chars of letters,
// digits, and hyphens. Catching this at init avoids a late rejection at publish.
// The current directory name is offered as the default, but only when it
// normalizes to a valid slug. The name is kept verbatim as the display name;
// its canonical slug is echoed when the two differ.
func promptPackageSlug() string {
	const slugPrompt = "Package name"

	defaultName := filepathBase()
	if !isValidPackageSlug(slugutil.Canonical(defaultName)) {
		defaultName = ""
	}

	name := promptWithDefault(slugPrompt, defaultName)
	for !isValidPackageSlug(slugutil.Canonical(name)) {
		fmt.Printf("error: %q does not normalize to a valid package slug — after lowercasing and collapsing '.'/'_'/'-' it must be 2-64 chars of letters, digits, and hyphens\n", name)
		name = promptWithDefault(slugPrompt, defaultName)
	}
	if s := slugutil.Canonical(name); s != name {
		fmt.Printf("  → will publish under slug %q\n", s)
	}
	return name
}

// promptPackageVersion prompts for the initial version and re-prompts until
// the entry is strict semver (MAJOR.MINOR.PATCH with an optional -prerelease),
// defaulting to 0.1.0. Validating here keeps a published package's version in
// the ordered, comparable form the resolver and `outdated` rely on, rather
// than letting an arbitrary string through to the registry.
func promptPackageVersion() string {
	const versionPrompt = "Version"
	const defaultVersion = "0.1.0"

	version := promptWithDefault(versionPrompt, defaultVersion)
	for !semver.ValidateVersion(version) {
		fmt.Printf("error: Invalid version \"%s\" - must be MAJOR.MINOR.PATCH, e.g. 1.0.0 or 2.1.0-rc.1\n", version)
		version = promptWithDefault(versionPrompt, defaultVersion)
	}
	return version
}

// promptNonEmptyString prompts with the given label and re-prompts until the
// user enters a non-empty value, used for required free-text fields that have
// no sensible default (e.g. description, author). The label is lowercased when
// echoed back in the error line, so callers should pass it in display case
// (e.g. "Description" yields "Invalid description - cannot be empty").
func promptNonEmptyString(prompt string) string {
	str := promptWithDefault(prompt, "")
	toLower := strings.ToLower(prompt)
	for str == "" {
		fmt.Printf("error: Invalid %s - cannot be empty\n", toLower)
		str = promptWithDefault(prompt, "")
	}
	return str
}
