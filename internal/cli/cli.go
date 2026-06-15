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
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
	"github.com/4js-mikefolcher/fglpkg/internal/env"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	gh "github.com/4js-mikefolcher/fglpkg/internal/github"
	"github.com/4js-mikefolcher/fglpkg/internal/hooks"
	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/oauth"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
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

// Execute is the main CLI entry point.
func Execute() error {
	if len(os.Args) < 2 {
		printUsage()
		return nil
	}

	cmd := os.Args[1]
	args := os.Args[2:]

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
	case "pack":
		return cmdPack(args)
	case "unpublish":
		return cmdUnpublish(args)
	case "login":
		return cmdLogin(args)
	case "logout":
		return cmdLogout(args)
	case "whoami":
		return cmdWhoami(args)
	case "owner":
		return cmdOwner(args)
	case "token":
		return cmdToken(args)
	case "config":
		return cmdConfig(args)
	case "workspace", "ws":
		return cmdWorkspace(args)
	case "run":
		return cmdRun(args)
	case "bdl":
		return cmdBdl(args)
	case "docs":
		return cmdDocs(args)
	case "version":
		return cmdVersion(args)
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %q\nRun 'fglpkg help' for usage", cmd)
	}
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
	name := promptWithDefault("Package name", filepathBase())
	version := promptWithDefault("Version", "0.1.0")
	description := promptWithDefault("Description", "")
	author := promptWithDefault("Author", "")
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
	inst := newInstaller(home)
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

	instOpts := installer.Options{Production: flags.production}

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

	scopeLabel := scopeDisplayName(flags.scope)
	for _, pkg := range flags.pkgs {
		name, version, err := parsePackageArg(pkg)
		if err != nil {
			return err
		}
		fmt.Printf("Resolving %s@%s (Genero %s)...\n", name, version, gv)
		info, err := registry.Resolve(name, version, generoMajor)
		if err != nil {
			return fmt.Errorf("failed to resolve %s@%s: %w", name, version, err)
		}
		// Older registry server versions omit `name` from the version-info
		// response; fall back to the user-supplied name so we never write an
		// empty key into fglpkg.json.
		if info.Name == "" {
			info.Name = name
		}
		m.AddFGLDependencyScoped(info.Name, info.Version, flags.scope)
		fmt.Printf("✓ Added %s@%s to %s [%s]\n", info.Name, info.Version, manifest.Filename, scopeLabel)
	}
	if err := m.Save("."); err != nil {
		return err
	}
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
func parseFlags(args []string) (remaining []string, local, global, force bool) {
	for _, a := range args {
		switch a {
		case "--local", "-l":
			local = true
		case "--global", "-g":
			global = true
		case "--force", "-f":
			force = true
		default:
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
	local      bool
	global     bool
	force      bool
	production bool
	scope      manifest.Scope
	pkgs       []string
}

// parseInstallFlags extends parseFlags with --save-dev/-D, --save-optional/-O,
// and --production/--prod flags. It rejects conflicting combinations.
func parseInstallFlags(args []string) (installFlags, error) {
	f := installFlags{scope: manifest.ScopeProd}
	devSeen, optSeen := false, false
	for _, a := range args {
		switch a {
		case "--local", "-l":
			f.local = true
		case "--global", "-g":
			f.global = true
		case "--force", "-f":
			f.force = true
		case "--production", "--prod":
			f.production = true
		case "--save-dev", "-D":
			devSeen = true
			f.scope = manifest.ScopeDev
		case "--save-optional", "-O":
			optSeen = true
			f.scope = manifest.ScopeOptional
		case "--save-prod", "-P":
			f.scope = manifest.ScopeProd
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
	pkgArgs, forceLocal, forceGlobal, _ := parseFlags(args)
	if len(pkgArgs) == 0 {
		return fmt.Errorf("usage: fglpkg remove <package>")
	}
	home, _, err := resolveHome(forceLocal, forceGlobal)
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
	inst := newInstaller(home)
	for _, pkg := range pkgArgs {
		if err := inst.Remove(pkg); err != nil {
			return fmt.Errorf("failed to remove %s: %w", pkg, err)
		}
		if scope := m.RemoveFGLDependency(pkg); scope != "" {
			fmt.Printf("✓ Removed %s from %s\n", pkg, scopeDisplayName(scope))
		} else {
			fmt.Printf("✓ Removed %s (not declared in manifest)\n", pkg)
		}
	}
	return m.Save(".")
}

// ─── update ───────────────────────────────────────────────────────────────────

func cmdUpdate(args []string) error {
	_, forceLocal, forceGlobal, _ := parseFlags(args)
	home, _, err := resolveHome(forceLocal, forceGlobal)
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	projectDir, _ := os.Getwd()
	fmt.Println("Ignoring lock file and re-resolving all dependencies...")
	return newInstaller(home).InstallAll(m, projectDir, true)
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdList(args []string) error {
	_, forceLocal, forceGlobal, _ := parseFlags(args)
	home, _, err := resolveHome(forceLocal, forceGlobal)
	if err != nil {
		return err
	}
	pkgs, err := newInstaller(home).List()
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
	_, forceLocal, forceGlobal, _ := parseFlags(args)
	gst := false
	for _, a := range args {
		if a == "--gst" {
			gst = true
		}
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	g := env.New(home)

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

func cmdSearch(args []string) error {
	term, all, err := parseSearchArgs(args)
	if err != nil {
		return err
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
		fmt.Printf("All packages (%d):\n", len(results))
	} else {
		fmt.Printf("Results for %q:\n", term)
	}
	fmt.Printf("  %-30s %-12s %s\n", "NAME", "VERSION", "DESCRIPTION")
	fmt.Printf("  %-30s %-12s %s\n", "----", "-------", "-----------")
	for _, r := range results {
		fmt.Printf("  %-30s %-12s %s\n", r.Name, r.LatestVersion, r.Description)
	}
	return nil
}

// parseSearchArgs returns the keyword term plus an --all flag. Errors on
// `search` with no args + no --all (the historical "missing keyword" error),
// and on conflicting `search --all <term>`.
func parseSearchArgs(args []string) (term string, all bool, err error) {
	for _, a := range args {
		switch a {
		case "--all":
			all = true
		default:
			if term != "" {
				return "", false, fmt.Errorf("unexpected extra argument %q", a)
			}
			term = a
		}
	}
	if all && term != "" {
		return "", false, fmt.Errorf("--all and <term> are mutually exclusive")
	}
	if !all && term == "" {
		return "", false, fmt.Errorf("usage: fglpkg search <term>   |   fglpkg search --all")
	}
	return term, all, nil
}

// ─── publish ──────────────────────────────────────────────────────────────────

// parsePublishFlags reads the publish flags: --dry-run/-n (preview, no
// network) and --ci (non-interactive pipeline mode).
func parsePublishFlags(args []string) (dryRun, ci bool, err error) {
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		case "--ci":
			ci = true
		default:
			return false, false, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return dryRun, ci, nil
}

func cmdPublish(args []string) error {
	dryRun, ci, err := parsePublishFlags(args)
	if err != nil {
		return err
	}

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
	if err := checkVariantNotPublished(m, generoMajor); err != nil {
		return err
	}
	registryURL := defaultPublishRegistry()

	// Resolve the bearer. In --ci mode the token must come from the
	// environment (FGLPKG_TOKEN / FGLPKG_PUBLISH_TOKEN) — CI runners should
	// not depend on cached interactive credentials, and the error must not
	// suggest the interactive `fglpkg login`.
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
	fmt.Printf("Publishing %s@%s (Genero %s variant) to %s...\n", m.Name, m.Version, generoMajor, registryURL)
	projectDir, _ := os.Getwd()
	if err := runHook(m, manifest.HookPrePublish, projectDir); err != nil {
		return err
	}
	if err := publishPackage(m, registryURL, generoMajor, dryRun); err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}
	if dryRun {
		fmt.Printf("✓ Dry run complete for %s@%s — no changes made\n", m.Name, m.Version)
	} else {
		fmt.Printf("✓ Published %s@%s — pending admin review\n", m.Name, m.Version)
		if ci {
			// Stable, greppable line for pipeline consumption.
			fmt.Printf("fglpkg-published name=%s version=%s variant=genero%s status=pending\n",
				m.Name, m.Version, generoMajor)
		}
	}
	return runHook(m, manifest.HookPostPublish, projectDir)
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
func publishPackage(m *manifest.Manifest, registryURL, generoMajor string, dryRun bool) error {
	// 1. Build the zip.
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return fmt.Errorf("cannot build package zip: %w", err)
	}
	fmt.Printf("  Package zip: %d bytes (SHA256: %s)\n", len(zipData), checksum)

	variant := "genero" + generoMajor
	filename := gh.VariantAssetName(m.Name, m.Version, generoMajor)
	slug := m.Name
	visibility := m.Visibility
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
		fmt.Printf("            body: {version:%q, changelog:\"\"}\n", m.Version)
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
	if err := registry.PublishCreateVersion(slug, m.Version, "", nil, meta); err != nil {
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
	return registry.PublishSubmit(slug, m.Version)
}

// dryRunScalar renders an optional scalar metadata field for the dry-run
// preview, showing "(none)" rather than an empty line when it is unset.
func dryRunScalar(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

// docSizeLabel renders a README/USERGUIDE body for the dry-run preview as a
// human size, "(none)" when empty, and flags "(truncated)" when the cap
// marker was appended.
func docSizeLabel(content, marker string) string {
	if content == "" {
		return "(none)"
	}
	label := fmt.Sprintf("%.1f KB", float64(len(content))/1024)
	if strings.HasSuffix(content, marker) {
		label += " (truncated)"
	}
	return label
}

func buildPackageZip(m *manifest.Manifest) ([]byte, string, error) {
	var buf bytes.Buffer
	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(&buf, h))

	// Determine the root directory for package files.
	root := m.Root
	if root == "" {
		root = "."
	}

	// Use manifest's files list if specified, otherwise use defaults.
	patterns := m.Files
	if len(patterns) == 0 {
		patterns = []string{"*.42m", "*.42f", "*.sch"}
	}

	// Load .fglpkgignore from the project root (current directory). The
	// manifest field is never excluded; everything else can be filtered.
	ignore, err := loadIgnore(".")
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", ignoreFilename, err)
	}

	// Walk the root directory tree and collect files matching the patterns.
	added := make(map[string]bool)
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isPackArtifactDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		for _, pattern := range patterns {
			matched, _ := filepath.Match(pattern, base)
			if matched && !added[path] {
				// Keep the path relative to the project directory (not
				// root) so the full directory structure is preserved in
				// the zip.  When extracted into ~/.fglpkg/packages/<name>/,
				// files like com/fourjs/poiapi/Module.42m stay intact.
				relPath, relErr := filepath.Rel(".", path)
				if relErr != nil {
					relPath = path
				}
				if ignore.shouldExclude(relPath, false) {
					continue
				}
				added[path] = true
				if err := addFileToZip(zw, path, relPath); err != nil {
					return fmt.Errorf("cannot add %s to zip: %w", path, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("error walking root %q: %w", root, err)
	}

	// Include bin script files so they are present in the installed package.
	// Bin scripts named in the manifest take precedence over .fglpkgignore —
	// dropping a declared `bin` script would silently break the package.
	for _, scriptPath := range m.BinFiles() {
		fullPath := filepath.Join(root, scriptPath)
		if added[fullPath] {
			continue
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, "", fmt.Errorf("bin script %q not found: %w", scriptPath, err)
		}
		if info.IsDir() {
			return nil, "", fmt.Errorf("bin script %q is a directory, not a file", scriptPath)
		}
		relPath, relErr := filepath.Rel(".", fullPath)
		if relErr != nil {
			relPath = fullPath
		}
		if err := addFileToZip(zw, fullPath, relPath); err != nil {
			return nil, "", fmt.Errorf("cannot add bin script %s to zip: %w", scriptPath, err)
		}
		added[fullPath] = true
	}

	// Include doc files matching the docs glob patterns.
	if len(m.Docs) > 0 {
		err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if isPackArtifactDir(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if added[path] {
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
					if err := addFileToZip(zw, path, relPath); err != nil {
						return fmt.Errorf("cannot add doc file %s to zip: %w", path, err)
					}
					added[path] = true
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, "", fmt.Errorf("error collecting doc files: %w", err)
		}
	}

	// Always include the manifest, but use a publish-safe copy so the
	// shipped fglpkg.json does not advertise devDependencies — those are
	// developer-only and never resolved transitively, so they are noise to
	// consumers (and let dev-only files leak into the package, see
	// isPackArtifactDir).
	if !added[manifest.Filename] {
		mfData, err := json.MarshalIndent(m.PublishCopy(), "", "  ")
		if err != nil {
			return nil, "", fmt.Errorf("cannot serialize publishable %s: %w", manifest.Filename, err)
		}
		fw, err := zw.Create(manifest.Filename)
		if err != nil {
			return nil, "", fmt.Errorf("cannot add %s to zip: %w", manifest.Filename, err)
		}
		if _, err := fw.Write(append(mfData, '\n')); err != nil {
			return nil, "", fmt.Errorf("cannot write %s to zip: %w", manifest.Filename, err)
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

// ─── unpublish ────────────────────────────────────────────────────────────────

func cmdUnpublish(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg unpublish <package>@<version>")
	}
	name, version, err := parsePackageArg(args[0])
	if err != nil {
		return err
	}
	if version == "" || version == "latest" {
		return fmt.Errorf("a specific version is required: fglpkg unpublish <package>@<version>")
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, registryURL)
	if token == "" {
		return fmt.Errorf("not logged in to legacy publish registry %s\nSet FGLPKG_PUBLISH_TOKEN to use this command (legacy fly.dev only)", registryURL)
	}

	fmt.Printf("Unpublishing %s@%s...\n", name, version)

	// 1. Delete the GitHub Release (and its asset).
	githubToken := credentials.GitHubTokenFor(home, registryURL)
	if githubToken != "" {
		owner, repo, err := resolveGitHubRepo()
		if err == nil {
			tag := gh.ReleaseTag(name, version)
			fmt.Printf("  Deleting GitHub release %s...\n", tag)
			if err := gh.DeleteRelease(githubToken, owner, repo, tag); err != nil {
				fmt.Printf("  Warning: could not delete GitHub release: %v\n", err)
			} else {
				fmt.Println("  Deleted GitHub release")
			}
		}
	}

	// 2. Remove metadata from the registry.
	url := fmt.Sprintf("%s/packages/%s/%s/unpublish",
		strings.TrimRight(registryURL, "/"), name, version)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("✓ Unpublished %s@%s\n", name, version)
	return nil
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
	registryURL := defaultRegistry()

	pat, err := parseLoginArgs(args)
	if err != nil {
		return err
	}

	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}

	if pat != "" {
		if !strings.HasPrefix(pat, "gpr_") {
			fmt.Fprintln(os.Stderr, "  Warning: PAT does not start with 'gpr_' — storing anyway.")
		}
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

// parseLoginArgs reads the (very small) flag surface of `fglpkg login`.
func parseLoginArgs(args []string) (pat string, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--token":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--token requires a value")
			}
			pat = strings.TrimSpace(args[i+1])
			i += 2
		case "-h", "--help":
			fmt.Println("usage: fglpkg login [--token <PAT>]")
			os.Exit(0)
		default:
			return "", fmt.Errorf("unknown argument %q\nusage: fglpkg login [--token <PAT>]", a)
		}
	}
	return pat, nil
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

func cmdLogout(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := defaultRegistry()
	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}
	if _, ok := creds.Get(registryURL); !ok {
		fmt.Printf("Not logged in to %s\n", registryURL)
		return nil
	}
	creds.Delete(registryURL)
	if err := creds.Save(home); err != nil {
		return err
	}
	fmt.Printf("✓ Logged out from %s\n", registryURL)
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
	ghToken := credentials.GitHubTokenFor(home, registryURL)
	if ghToken != "" {
		fmt.Println("GitHub token: configured")
	} else {
		fmt.Println("GitHub token: not configured (set FGLPKG_GITHUB_TOKEN)")
	}
	return nil
}

// ─── owner ────────────────────────────────────────────────────────────────────

func cmdOwner(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg owner <list|add|remove> <package> [username]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		if len(rest) == 0 {
			return fmt.Errorf("usage: fglpkg owner list <package>")
		}
		return cmdOwnerList(rest[0])
	case "add":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fglpkg owner add <package> <username>")
		}
		return cmdOwnerAdd(rest[0], rest[1])
	case "remove":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fglpkg owner remove <package> <username>")
		}
		return cmdOwnerRemove(rest[0], rest[1])
	default:
		return fmt.Errorf("unknown owner subcommand %q", sub)
	}
}

func cmdOwnerList(pkg string) error {
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	resp, err := authGet(reg+"/packages/"+pkg+"/owners", token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	var result struct {
		Owners []string `json:"owners"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	fmt.Printf("Owners of %s:\n", pkg)
	for _, o := range result.Owners {
		fmt.Printf("  %s\n", o)
	}
	return nil
}

func cmdOwnerAdd(pkg, username string) error {
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	body := fmt.Sprintf(`{"username":%q}`, username)
	resp, err := authPost(reg+"/packages/"+pkg+"/owners", token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Added %s as owner of %s\n", username, pkg)
	return nil
}

func cmdOwnerRemove(pkg, username string) error {
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	req, _ := http.NewRequest(http.MethodDelete,
		reg+"/packages/"+pkg+"/owners/"+username, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Removed %s from owners of %s\n", username, pkg)
	return nil
}

// ─── token ────────────────────────────────────────────────────────────────────

func cmdToken(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg token <create|revoke|rotate> [username]")
	}
	sub, rest := args[0], args[1:]
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}

	switch sub {
	case "create":
		username := ""
		if len(rest) > 0 {
			username = rest[0]
		} else {
			username = promptWithDefault("New username", "")
		}
		email := promptWithDefault("Email (optional)", "")
		body := fmt.Sprintf(`{"username":%q,"email":%q}`, username, email)
		resp, err := authPost(reg+"/auth/token", token, body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return registryError(resp)
		}
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
		fmt.Printf("✓ Created user %s\nToken: %s\n⚠ Save this token — it will not be shown again.\n",
			result["username"], result["token"])

	case "revoke":
		target := ""
		if len(rest) > 0 {
			target = rest[0]
		}
		body := ""
		if target != "" {
			body = fmt.Sprintf(`{"username":%q}`, target)
		}
		resp, err := authDo(http.MethodDelete, reg+"/auth/token", token, body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return registryError(resp)
		}
		if target != "" {
			fmt.Printf("✓ Revoked token for %s\n", target)
		} else {
			fmt.Println("✓ Token revoked")
		}

	case "rotate":
		resp, err := authPost(reg+"/auth/token/rotate", token, "")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return registryError(resp)
		}
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
		fmt.Printf("✓ Token rotated\nNew token: %s\n⚠ Save this token — it will not be shown again.\n",
			result["token"])

	default:
		return fmt.Errorf("unknown token subcommand %q", sub)
	}
	return nil
}

// ─── config ───────────────────────────────────────────────────────────────────

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg config <github-repos> <list|add|remove> [owner/repo]")
	}
	switch args[0] {
	case "github-repos":
		return cmdConfigGitHubRepos(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func cmdConfigGitHubRepos(args []string) error {
	if len(args) == 0 {
		return cmdConfigGitHubReposList()
	}
	switch args[0] {
	case "list":
		return cmdConfigGitHubReposList()
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: fglpkg config github-repos add <owner/repo>")
		}
		return cmdConfigGitHubReposAdd(args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: fglpkg config github-repos remove <owner/repo>")
		}
		return cmdConfigGitHubReposRemove(args[1])
	default:
		return fmt.Errorf("unknown github-repos subcommand %q", args[0])
	}
}

func cmdConfigGitHubReposList() error {
	cfg, err := registry.FetchConfig()
	if err != nil {
		return err
	}
	if len(cfg.GitHubRepos) == 0 {
		fmt.Println("No GitHub repos configured.")
		return nil
	}
	fmt.Println("GitHub package repos:")
	for _, r := range cfg.GitHubRepos {
		fmt.Printf("  %s/%s\n", r.Owner, r.Repo)
	}
	return nil
}

func cmdConfigGitHubReposAdd(ownerRepo string) error {
	owner, repo, err := parseOwnerRepo(ownerRepo)
	if err != nil {
		return err
	}
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	body := fmt.Sprintf(`{"owner":%q,"repo":%q}`, owner, repo)
	resp, err := authPost(reg+"/config/github-repos", token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Added GitHub repo %s/%s\n", owner, repo)
	return nil
}

func cmdConfigGitHubReposRemove(ownerRepo string) error {
	owner, repo, err := parseOwnerRepo(ownerRepo)
	if err != nil {
		return err
	}
	home, _ := fglpkgHome()
	reg := registry.LegacyBase
	token := credentials.ActivePublishBearer(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	url := fmt.Sprintf("%s/config/github-repos/%s/%s", reg, owner, repo)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Removed GitHub repo %s/%s\n", owner, repo)
	return nil
}

func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo format, got %q", s)
	}
	return parts[0], parts[1], nil
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
	if isProjectDir() {
		wd, _ := os.Getwd()
		localDir := filepath.Join(wd, ".fglpkg", "packages", name)
		if m, err := manifest.Load(localDir); err == nil {
			return localDir, m, nil
		}
	}
	globalHome, err := fglpkgHome()
	if err == nil {
		globalDir := filepath.Join(globalHome, "packages", name)
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
  init              Create a new fglpkg.json (--template <library|app> to scaffold)
  install [pkg...]  Install all dependencies (or add specific packages)
  remove <pkg>      Remove a package
  update            Re-resolve and update all dependencies
  list              List installed packages
  env               Print environment variable exports
  search <term>     Search the registry (use --all to list every package)
  info <pkg>[@ver]  Show registry metadata for a package (--json for raw output)
  outdated          Show FGL deps with newer versions available (--json for JSON)
  audit             Check installed Java JARs for known vulnerabilities
                    (--json, --severity=<level>, --production)
  sbom              Emit a CycloneDX SBOM for the project from fglpkg.lock
                    (-o file, --pretty, --production)
  completion <sh>   Print shell completion script (bash|zsh|fish|powershell)
  bdl <pkg> <mod>   Run a BDL program from an installed package
  publish           Publish current package to the registry; submits for admin review
                    (--dry-run prints what would happen without calling out;
                     --ci for non-interactive pipelines: requires FGLPKG_TOKEN,
                     prints a machine-readable status line)
  pack [-o file]    Build the publishable zip locally without uploading
                    (--list prints contents without writing a file)
  unpublish <p>@<v> Remove a published version (LEGACY fly.dev registry only)
  login             Sign in to the registry (OAuth browser flow, or --token <PAT>)
  logout            Remove saved credentials
  whoami            Show current authenticated user
  owner             Manage package ownership (LEGACY fly.dev registry only)
  token             Manage user tokens (LEGACY fly.dev registry only)
  config            Manage registry configuration (LEGACY fly.dev registry only)
  workspace         Manage monorepo workspaces
  run <command>     Run a script from an installed package
  docs <package>    List or view package documentation
  version [bump]    Print fglpkg version, or bump package version
                    (bump = patch|minor|major|prerelease|<semver>, add --git to tag)
  help              Show this help

FLAGS (for install, remove, update, list, env):
  --local, -l       Force local project directory (.fglpkg/)
  --global, -g      Force global home directory (~/.fglpkg/)
  (default)         Auto-detect: local if .fglpkg/ or fglpkg.json exists

FLAGS (for install only):
  --force, -f           Delete fglpkg.lock and .fglpkg/ first, then re-download
                        every package from the registry (local installs only)
  --save-dev, -D        Record added packages under "devDependencies"
  --save-optional, -O   Record added packages under "optionalDependencies"
  --save-prod, -P       Record added packages under "dependencies" (default)
  --production, --prod  Skip devDependencies when installing (optional deps
                        are still attempted)

FLAGS (for env only):
  --gst             Output in Genero Studio format (implies --local)

ENVIRONMENT:
  FGLPKG_HOME              Override ~/.fglpkg
  FGLPKG_REGISTRY          Registry URL for install/search/audit/whoami/publish.
                           Default: https://service.generointelligence.ai
  FGLPKG_PUBLISH_REGISTRY  Overrides FGLPKG_REGISTRY for the publish command only
  FGLPKG_TOKEN             Bearer token for the registry (overrides stored OAuth)
  FGLPKG_PUBLISH_TOKEN     Bearer for the LEGACY fly.dev commands
                           (unpublish/owner/token/config)
  FGLPKG_GITHUB_TOKEN      GitHub PAT — only used by LEGACY downloads/unpublish
  FGLPKG_GITHUB_REPO       GitHub owner/repo — only used by LEGACY commands
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

// resolveGitHubRepo returns the GitHub owner/repo for package storage.
// Precedence: FGLPKG_GITHUB_REPO env var > registry config > error.
func resolveGitHubRepo() (owner, repo string, err error) {
	owner, repo, err = gh.RepoFromEnv()
	if err != nil {
		return "", "", err
	}
	if owner != "" {
		return owner, repo, nil
	}
	// Fall back to the registry config.
	cfg, err := registry.FetchConfig()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine GitHub repo: FGLPKG_GITHUB_REPO is not set and registry config is unavailable: %w", err)
	}
	if len(cfg.GitHubRepos) == 0 {
		return "", "", fmt.Errorf("no GitHub repos configured on the registry\nSet FGLPKG_GITHUB_REPO or ask an admin to run: fglpkg config github-repos add <owner/repo>")
	}
	return cfg.GitHubRepos[0].Owner, cfg.GitHubRepos[0].Repo, nil
}

func newInstaller(home string) *installer.Installer {
	// Always look up credentials from the global home directory, even when
	// installing to a local project directory (--local).
	globalHome, err := fglpkgHome()
	if err != nil {
		globalHome = home
	}
	registryURL := defaultRegistry()
	// GitHub token only matters for the legacy fglpkg-registry.fly.dev flow
	// (downloads indirected to private GitHub Releases). The new registry
	// serves zips itself, so the warning is downgraded — only surface it
	// when the consumer registry IS the legacy one.
	githubToken := credentials.GitHubTokenFor(globalHome, registryURL)
	if githubToken == "" && strings.Contains(registryURL, "fglpkg-registry.fly.dev") {
		fmt.Println("  Warning: no GitHub token configured — downloads from private repos will fail")
		fmt.Println("  Set FGLPKG_GITHUB_TOKEN")
	}
	registryToken, _ := credentials.ActiveBearer(context.Background(), globalHome, registryURL, oauth.Refresh)
	return installer.New(home, githubToken, registryToken)
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

// defaultPublishRegistry returns the publisher registry URL — publish,
// unpublish, owner, token, config. Stays on fglpkg-registry.fly.dev until
// the new server adds publish endpoints. Override with FGLPKG_PUBLISH_REGISTRY,
// falling back to FGLPKG_REGISTRY for self-hosted single-registry setups.
func defaultPublishRegistry() string {
	if r := os.Getenv("FGLPKG_PUBLISH_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return "https://fglpkg-registry.fly.dev"
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
func matchGlob(pattern, path string) bool {
	// Normalise separators.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	if !strings.Contains(pattern, "**") {
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Split on the first "**" occurrence.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimRight(parts[0], "/")
	suffix := strings.TrimLeft(parts[1], "/")

	// Check prefix: the path must start with the prefix directory (if any).
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+"/") && path != prefix {
			return false
		}
	}

	if suffix == "" {
		return true
	}

	// The remaining path (after prefix) must end with a segment matching suffix.
	remaining := path
	if prefix != "" {
		remaining = strings.TrimPrefix(path, prefix+"/")
	}
	matched, _ := filepath.Match(suffix, filepath.Base(remaining))
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
