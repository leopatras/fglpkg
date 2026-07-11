#+ command dispatch and Phase-1 command implementations
#+ port of cmd/fglpkg/main.go + internal/cli/{cli,info,version,pack}.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.credentials
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.installer
IMPORT FGL fglpkg.env
IMPORT FGL fglpkg.hooks
IMPORT FGL fglpkg.pack
IMPORT FGL fglpkg.oauth
IMPORT FGL fglpkg.publish
IMPORT FGL fglpkg.outdated
IMPORT FGL fglpkg.completion
IMPORT FGL fglpkg.runner
IMPORT FGL fglpkg.workspace
IMPORT FGL fglpkg.sbom
IMPORT FGL fglpkg.audit
IMPORT FGL fglpkg.templates
IMPORT FGL fglpkg.commands
&include "myassert.inc"

PRIVATE TYPE TInstallFlags RECORD
  local BOOLEAN,
  global BOOLEAN,
  force BOOLEAN,
  production BOOLEAN,
  scope STRING,
  pkgs fglpkgutils.TStringArr
END RECORD

DEFINE _stdin base.Channel

#+the main CLI entry point; returns the process exit code
FUNCTION cliExecute() RETURNS INT
  DEFINE args fglpkgutils.TStringArr
  DEFINE i INT

  CALL wireAuth()

  IF num_args() == 0 THEN
    CALL commands.printUsage()
    RETURN 0
  END IF

  VAR cmd = arg_val(1)
  FOR i = 2 TO num_args()
    LET args[args.getLength() + 1] = arg_val(i)
  END FOR

  --`fglpkg help [command]` and top-level -h/--help show usage
  IF cmd == "help" OR cmd == "--help" OR cmd == "-h" THEN
    IF args.getLength() > 0 THEN
      VAR hidx = commands.findCommand(args[1])
      IF hidx > 0 THEN
        CALL commands.printCommandHelp(hidx)
        RETURN 0
      END IF
    END IF
    CALL commands.printUsage()
    RETURN 0
  END IF

  --per-command help, handled before dispatch so every command gets
  --consistent --help without each handler re-implementing it
  VAR idx = commands.findCommand(cmd)
  IF idx > 0 AND commands.helpRequested(idx, args) THEN
    CALL commands.printCommandHelp(idx)
    RETURN 0
  END IF

  CASE cmd
    WHEN "init"
      RETURN cmdInit(args)
    WHEN "install"
      RETURN cmdInstall(args)
    WHEN "remove"
      RETURN cmdRemove(args)
    WHEN "update"
      RETURN cmdUpdate(args)
    WHEN "list"
      RETURN cmdList(args)
    WHEN "env"
      RETURN cmdEnv(args)
    WHEN "search"
      RETURN cmdSearch(args)
    WHEN "info"
      RETURN cmdInfo(args)
    WHEN "view"
      RETURN cmdInfo(args)
    WHEN "pack"
      RETURN cmdPack(args)
    WHEN "version"
      RETURN cmdVersion(args)
    WHEN "outdated"
      RETURN outdated.cmdOutdated(args)
    WHEN "audit"
      RETURN audit.cmdAudit(args)
    WHEN "sbom"
      RETURN sbom.cmdSbom(args)
    WHEN "completion"
      RETURN completion.cmdCompletion(args)
    WHEN "publish"
      RETURN publish.cmdPublish(args)
    WHEN "login"
      RETURN cmdLogin(args)
    WHEN "logout"
      RETURN cmdLogout(args)
    WHEN "whoami"
      RETURN cmdWhoami(args)
    WHEN "workspace"
      RETURN cmdWorkspace(args)
    WHEN "ws"
      RETURN cmdWorkspace(args)
    WHEN "run"
      RETURN runner.cmdRun(args)
    WHEN "bdl"
      RETURN runner.cmdBdl(args)
    WHEN "docs"
      RETURN runner.cmdDocs(args)
    OTHERWISE
      RETURN fail(SFMT('unknown command: "%1"\nRun \'fglpkg help\' for usage',
              cmd))
  END CASE
  RETURN 0
END FUNCTION

#+wires the registry bearer and installer download tokens to the stored
#+credentials (FGLPKG_TOKEN > OAuth > PAT)
PRIVATE FUNCTION wireAuth()
  CALL credentials.setRefresher(FUNCTION oauth.refresh)
  CALL registry.setBearerFunc(FUNCTION bearerFromCreds)
  CALL registry.setRefreshFunc(FUNCTION forceRefreshHook)
  VAR home = fglpkgutils.globalHome()
  VAR regURL = fglpkgutils.registryBaseURL()
  VAR creds = credentials.getCreds(home, regURL)
  CALL installer.setTokens(creds.githubToken,
      credentials.activeBearer(home, regURL))
END FUNCTION

#+the registry 401-retry hook: unconditional OAuth refresh + persist
FUNCTION forceRefreshHook() RETURNS BOOLEAN
  RETURN credentials.forceRefresh(
      fglpkgutils.globalHome(), fglpkgutils.registryBaseURL())
END FUNCTION

FUNCTION bearerFromCreds() RETURNS STRING
  RETURN credentials.activeBearer(
      fglpkgutils.globalHome(), fglpkgutils.registryBaseURL())
END FUNCTION

#+prints an error like the Go binary (message on stderr, exit code 1)
PRIVATE FUNCTION fail(msg STRING) RETURNS INT
  CALL fglpkgutils.printStderr(msg)
  RETURN 1
END FUNCTION

--─── init ───────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdInit(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE i INT
  DEFINE tmplName STRING
  DEFINE ok BOOLEAN
  DEFINE err STRING

  LET i = 1
  WHILE i <= args.getLength()
    CASE
      WHEN args[i] == "--template" OR args[i] == "-t"
        IF i + 1 > args.getLength() THEN
          RETURN fail(SFMT("%1 requires a template name\nAvailable templates:\n%2",
                  args[i], templates.templateList()))
        END IF
        LET i = i + 1
        LET tmplName = args[i]
      WHEN fglpkgutils.startsWith(args[i], "--template=")
        LET tmplName = args[i].subString(12, args[i].getLength())
      OTHERWISE
        RETURN fail(SFMT('unexpected argument "%1"\nUsage: fglpkg init [--template <name>]',
                args[i]))
    END CASE
    LET i = i + 1
  END WHILE
  IF tmplName IS NOT NULL AND NOT templates.templateExists(tmplName) THEN
    RETURN fail(SFMT('unknown template "%1"\nAvailable templates:\n%2',
            tmplName, templates.templateList()))
  END IF

  IF manifest.manifestExists(".") THEN
    RETURN fail(SFMT("%1 already exists in the current directory",
            manifest.MANIFEST_FILENAME))
  END IF

  VAR name = promptPackageSlug()
  VAR version = promptPackageVersion()
  VAR description = promptNonEmptyString("Description")
  VAR author = promptNonEmptyString("Author")
  VAR m = manifest.newManifest(name, version, description, author)
  IF tmplName IS NOT NULL THEN
    CALL templates.applyTemplate(m, tmplName)
  END IF
  CALL manifest.save(m, ".") RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to write %1: %2",
            manifest.MANIFEST_FILENAME, err))
  END IF
  DISPLAY SFMT("%1 Created %2", fglpkgutils.C_CHECK, manifest.MANIFEST_FILENAME)
  IF tmplName IS NOT NULL THEN
    CALL templates.writeTemplateFiles(tmplName, ".", name) RETURNING ok, err
    IF NOT ok THEN
      RETURN fail(err)
    END IF
  END IF
  RETURN 0
END FUNCTION

--─── install ────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdInstall(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE flags TInstallFlags
  DEFINE ok, gok BOOLEAN
  DEFINE err STRING
  DEFINE m manifest.TManifest
  DEFINE gv genero.TGeneroVersion
  DEFINE info registry.TPackageInfo
  DEFINE i INT
  DEFINE home STRING
  DEFINE isLocal BOOLEAN
  DEFINE name, version STRING

  CALL parseInstallFlags(args) RETURNING ok, flags, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF

  --`fglpkg install <pkg>` in a non-project dir becomes a new local
  --project: the manifest lands here, so the install must too
  VAR addingToNewProject =
      flags.pkgs.getLength() > 0
      AND NOT flags.local
      AND NOT flags.global
      AND NOT fglpkgutils.isProjectDir(".")
  VAR forceLocal = flags.local OR addingToNewProject

  CALL resolveHome(forceLocal, flags.global) RETURNING home, isLocal

  IF isLocal THEN
    DISPLAY "Installing to local project directory (.fglpkg/)"
    DISPLAY "  Tip: add .fglpkg/ to your .gitignore file"
    IF addingToNewProject THEN
      DISPLAY "  Note: no fglpkg.json found — initialising the current directory as a new project."
    END IF
  END IF

  IF flags.force THEN
    IF NOT isLocal THEN
      RETURN fail("--force is only supported for local installs; re-run inside a project directory or with --local")
    END IF
    CALL resetLocalInstall(".", home)
  END IF

  IF flags.pkgs.getLength() == 0 THEN
    IF NOT manifest.manifestExists(".") THEN
      RETURN fail(SFMT("no %1 in current directory — run 'fglpkg init' first",
              manifest.MANIFEST_FILENAME))
    END IF
    CALL manifest.load(".") RETURNING ok, m, err
    IF NOT ok THEN
      RETURN fail(SFMT("failed to load %1: %2",
              manifest.MANIFEST_FILENAME, err))
    END IF
    IF flags.production THEN
      DISPLAY "Installing in production mode (devDependencies will be skipped)"
    END IF
    IF NOT runHookCmd(m, "preinstall") THEN
      RETURN 1
    END IF
    CALL installer.installAll(m, ".", home, flags.force, flags.production)
        RETURNING ok, err
    IF NOT ok THEN
      RETURN fail(err)
    END IF
    IF NOT runHookCmd(m, "postinstall") THEN
      RETURN 1
    END IF
    RETURN 0
  END IF

  --add + install the named packages
  CALL manifest.loadOrNew(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  CALL genero.detect() RETURNING gok, gv, err
  IF NOT gok THEN
    RETURN fail(SFMT("cannot detect Genero version: %1", err))
  END IF

  FOR i = 1 TO flags.pkgs.getLength()
    CALL parsePackageArg(flags.pkgs[i]) RETURNING name, version
    DISPLAY SFMT("Resolving %1@%2 (Genero %3)...",
        name, version, genero.versionString(gv))
    CALL registry.resolvePackage(name, version, genero.majorString(gv))
        RETURNING ok, info, err
    IF NOT ok THEN
      RETURN fail(SFMT("failed to resolve %1@%2: %3",
              name, version, privateHint(err, name)))
    END IF
    IF info.name IS NULL THEN
      LET info.name = name
    END IF
    CALL manifest.addFGLDependencyScoped(m, info.name,
        info.version, flags.scope)
    DISPLAY SFMT("%1 Added %2@%3 to %4 [%5]",
        fglpkgutils.C_CHECK, info.name, info.version,
        manifest.MANIFEST_FILENAME, scopeDisplayName(flags.scope))
  END FOR
  CALL manifest.save(m, ".") RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  DISPLAY ""
  IF NOT runHookCmd(m, "preinstall") THEN
    RETURN 1
  END IF
  CALL installer.installAll(m, ".", home, TRUE, flags.production)
      RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  IF NOT runHookCmd(m, "postinstall") THEN
    RETURN 1
  END IF
  RETURN 0
END FUNCTION

#+runs a lifecycle hook with a one-line log; FALSE aborts the command
PRIVATE FUNCTION runHookCmd(m manifest.TManifest, event STRING)
    RETURNS BOOLEAN
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF NOT m.hooks.contains(event) OR m.hooks[event].getLength() == 0 THEN
    RETURN TRUE
  END IF
  DISPLAY SFMT("Running %1 hook (%2 op(s))...",
      event, m.hooks[event].getLength())
  CALL hooks.runHooks(m, event, ".") RETURNING ok, err
  IF NOT ok THEN
    CALL fglpkgutils.printStderr(err)
  END IF
  RETURN ok
END FUNCTION

PRIVATE FUNCTION scopeDisplayName(s STRING) RETURNS STRING
  CASE s
    WHEN manifest.SCOPE_DEV
      RETURN "devDependencies"
    WHEN manifest.SCOPE_OPTIONAL
      RETURN "optionalDependencies"
    OTHERWISE
      RETURN "dependencies"
  END CASE
END FUNCTION

#+appends a login hint to a not-found error when no bearer is configured
#+(private packages 404 indistinguishably from missing ones)
PRIVATE FUNCTION privateHint(err STRING, pkg STRING) RETURNS STRING
  IF NOT registry.isNotFoundErr(err) THEN
    RETURN err
  END IF
  VAR tok = registry.bearer()
  IF tok IS NOT NULL AND tok.getLength() > 0 THEN
    RETURN err
  END IF
  RETURN SFMT('%1\n  hint: if "%2" is a private package, run: fglpkg login',
      err, pkg)
END FUNCTION

#+the fglpkg home based on context: --local/--global override, otherwise
#+local when the current directory looks like a project
PRIVATE FUNCTION resolveHome(forceLocal BOOLEAN, forceGlobal BOOLEAN)
    RETURNS(STRING, BOOLEAN)
  IF forceLocal THEN
    RETURN fglpkgutils.localHome(os.Path.pwd()), TRUE
  END IF
  IF forceGlobal THEN
    RETURN fglpkgutils.globalHome(), FALSE
  END IF
  IF fglpkgutils.isProjectDir(".") THEN
    RETURN fglpkgutils.localHome(os.Path.pwd()), TRUE
  END IF
  RETURN fglpkgutils.globalHome(), FALSE
END FUNCTION

#+deletes fglpkg.lock and the local package/JAR dirs so the next install
#+re-downloads everything (missing files are ignored)
PRIVATE FUNCTION resetLocalInstall(projectDir STRING, home STRING)
  VAR lockPath = lockfile.lockPath(projectDir)
  IF os.Path.exists(lockPath) THEN
    CALL os.Path.delete(lockPath) RETURNING status
  END IF
  CALL fglpkgutils.rmrf(fglpkgutils.packagesDir(home))
  CALL fglpkgutils.rmrf(fglpkgutils.jarsDir(home))
  DISPLAY "Cleared fglpkg.lock and .fglpkg/ — reloading from registry..."
END FUNCTION

#+splits "pkg@version" into name + version ("latest" when no @)
FUNCTION parsePackageArg(arg STRING) RETURNS(STRING, STRING)
  VAR idx = arg.getIndexOf("@", 2) --a leading @ is part of the name
  IF idx > 1 THEN
    RETURN arg.subString(1, idx - 1), arg.subString(idx + 1, arg.getLength())
  END IF
  RETURN arg, "latest"
END FUNCTION

#+shared --local/-l --global/-g --force/-f parsing; other args returned
PRIVATE FUNCTION parseFlags(args fglpkgutils.TStringArr)
    RETURNS(fglpkgutils.TStringArr, BOOLEAN, BOOLEAN, BOOLEAN)
  DEFINE remaining fglpkgutils.TStringArr
  DEFINE local, global, force BOOLEAN
  DEFINE i INT
  FOR i = 1 TO args.getLength()
    CASE args[i]
      WHEN "--local"
        LET local = TRUE
      WHEN "-l"
        LET local = TRUE
      WHEN "--global"
        LET global = TRUE
      WHEN "-g"
        LET global = TRUE
      WHEN "--force"
        LET force = TRUE
      WHEN "-f"
        LET force = TRUE
      OTHERWISE
        LET remaining[remaining.getLength() + 1] = args[i]
    END CASE
  END FOR
  RETURN remaining, local, global, force
END FUNCTION

PRIVATE FUNCTION parseInstallFlags(args fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, TInstallFlags, STRING)
  DEFINE f TInstallFlags
  DEFINE devSeen, optSeen BOOLEAN
  DEFINE i INT
  LET f.scope = manifest.SCOPE_PROD
  FOR i = 1 TO args.getLength()
    CASE args[i]
      WHEN "--local"
        LET f.local = TRUE
      WHEN "-l"
        LET f.local = TRUE
      WHEN "--global"
        LET f.global = TRUE
      WHEN "-g"
        LET f.global = TRUE
      WHEN "--force"
        LET f.force = TRUE
      WHEN "-f"
        LET f.force = TRUE
      WHEN "--production"
        LET f.production = TRUE
      WHEN "--prod"
        LET f.production = TRUE
      WHEN "--save-dev"
        LET devSeen = TRUE
        LET f.scope = manifest.SCOPE_DEV
      WHEN "-D"
        LET devSeen = TRUE
        LET f.scope = manifest.SCOPE_DEV
      WHEN "--save-optional"
        LET optSeen = TRUE
        LET f.scope = manifest.SCOPE_OPTIONAL
      WHEN "-O"
        LET optSeen = TRUE
        LET f.scope = manifest.SCOPE_OPTIONAL
      WHEN "--save-prod"
        LET f.scope = manifest.SCOPE_PROD
      WHEN "-P"
        LET f.scope = manifest.SCOPE_PROD
      OTHERWISE
        LET f.pkgs[f.pkgs.getLength() + 1] = args[i]
    END CASE
  END FOR
  IF devSeen AND optSeen THEN
    RETURN FALSE, f, "--save-dev and --save-optional are mutually exclusive"
  END IF
  IF f.production AND (devSeen OR optSeen) THEN
    RETURN FALSE, f,
        "--production cannot be combined with --save-dev or --save-optional"
  END IF
  RETURN TRUE, f, NULL
END FUNCTION

--─── remove / update / list ─────────────────────────────────────────────────

PRIVATE FUNCTION cmdRemove(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE pkgArgs fglpkgutils.TStringArr
  DEFINE forceLocal, forceGlobal, force, ok BOOLEAN
  DEFINE home, err STRING
  DEFINE isLocal BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE i INT
  CALL parseFlags(args) RETURNING pkgArgs, forceLocal, forceGlobal, force
  IF pkgArgs.getLength() == 0 THEN
    RETURN fail("usage: fglpkg remove <package>")
  END IF
  CALL resolveHome(forceLocal, forceGlobal) RETURNING home, isLocal
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to load %1: %2", manifest.MANIFEST_FILENAME, err))
  END IF
  IF NOT runHookCmd(m, "preuninstall") THEN
    RETURN 1
  END IF
  FOR i = 1 TO pkgArgs.getLength()
    CALL installer.removePackage(pkgArgs[i], home) RETURNING ok, err
    IF NOT ok THEN
      RETURN fail(SFMT("failed to remove %1: %2", pkgArgs[i], err))
    END IF
    VAR scope = manifest.removeFGLDependency(m, pkgArgs[i])
    IF scope IS NOT NULL THEN
      DISPLAY SFMT("%1 Removed %2 from %3",
          fglpkgutils.C_CHECK, pkgArgs[i], scopeDisplayName(scope))
    ELSE
      DISPLAY SFMT("%1 Removed %2 (not declared in manifest)",
          fglpkgutils.C_CHECK, pkgArgs[i])
    END IF
  END FOR
  CALL manifest.save(m, ".") RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION cmdUpdate(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE rest fglpkgutils.TStringArr
  DEFINE forceLocal, forceGlobal, force, ok, isLocal BOOLEAN
  DEFINE home, err STRING
  DEFINE m manifest.TManifest
  CALL parseFlags(args) RETURNING rest, forceLocal, forceGlobal, force
  CALL resolveHome(forceLocal, forceGlobal) RETURNING home, isLocal
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to load %1: %2", manifest.MANIFEST_FILENAME, err))
  END IF
  DISPLAY "Ignoring lock file and re-resolving all dependencies..."
  CALL installer.installAll(m, ".", home, TRUE, FALSE) RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION cmdList(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE rest fglpkgutils.TStringArr
  DEFINE forceLocal, forceGlobal, force, isLocal BOOLEAN
  DEFINE home STRING
  DEFINE i INT
  CALL parseFlags(args) RETURNING rest, forceLocal, forceGlobal, force
  CALL resolveHome(forceLocal, forceGlobal) RETURNING home, isLocal
  VAR pkgs = installer.listInstalled(home)
  IF pkgs.getLength() == 0 THEN
    DISPLAY "No packages installed."
    RETURN 0
  END IF
  DISPLAY "Installed packages:"
  FOR i = 1 TO pkgs.getLength()
    DISPLAY SFMT("  %1 %2",
        fglpkgutils.padRight(pkgs[i].name, 30), pkgs[i].version)
  END FOR
  RETURN 0
END FUNCTION

--─── env ────────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdEnv(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE rest fglpkgutils.TStringArr
  DEFINE forceLocal, forceGlobal, force, gst, gwa BOOLEAN
  DEFINE lines fglpkgutils.TStringArr
  DEFINE i INT
  CALL parseFlags(args) RETURNING rest, forceLocal, forceGlobal, force
  FOR i = 1 TO args.getLength()
    IF args[i] == "--gst" THEN
      LET gst = TRUE
    END IF
    IF args[i] == "--gwa" THEN
      LET gwa = TRUE
    END IF
  END FOR
  VAR home = fglpkgutils.globalHome()
  IF gwa THEN
    LET lines = env.generateGWA(home)
  ELSE
    VAR useLocal = forceLocal OR gst
    IF NOT forceGlobal AND NOT useLocal THEN
      LET useLocal = fglpkgutils.isProjectDir(".")
    END IF
    CASE
      WHEN gst
        LET lines = env.generateGST(home)
      WHEN useLocal
        LET lines = env.generateLocal(home)
      OTHERWISE
        LET lines = env.generateExports(home)
    END CASE
  END IF
  FOR i = 1 TO lines.getLength()
    DISPLAY lines[i]
  END FOR
  RETURN 0
END FUNCTION

--─── search ─────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdSearch(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE term STRING
  DEFINE all, ok BOOLEAN
  DEFINE results DYNAMIC ARRAY OF registry.TSearchResult
  DEFINE err STRING
  DEFINE i INT
  FOR i = 1 TO args.getLength()
    IF args[i] == "--all" THEN
      LET all = TRUE
    ELSE
      IF term IS NOT NULL THEN
        RETURN fail(SFMT('unexpected extra argument "%1"', args[i]))
      END IF
      LET term = args[i]
    END IF
  END FOR
  IF all AND term IS NOT NULL THEN
    RETURN fail("--all and <term> are mutually exclusive")
  END IF
  IF NOT all AND term IS NULL THEN
    RETURN fail("usage: fglpkg search <term>   |   fglpkg search --all")
  END IF

  CALL registry.search(NVL(term, "")) RETURNING ok, results, err
  IF NOT ok THEN
    IF all AND fglpkgutils.contains(err, "HTTP 400") THEN
      RETURN fail("this registry doesn't support --all (returned HTTP 400)\nupgrade the registry, or pass a search term instead")
    END IF
    RETURN fail(SFMT("search failed: %1", err))
  END IF

  IF results.getLength() == 0 THEN
    IF all THEN
      DISPLAY "No packages in the registry."
    ELSE
      DISPLAY SFMT('No packages found matching "%1"', term)
    END IF
    RETURN 0
  END IF
  IF all THEN
    DISPLAY SFMT("All packages (%1):", results.getLength())
  ELSE
    DISPLAY SFMT('Results for "%1":', term)
  END IF
  DISPLAY SFMT("  %1%2%3",
      fglpkgutils.padRight("NAME", 31), fglpkgutils.padRight("VERSION", 13),
      "DESCRIPTION")
  DISPLAY SFMT("  %1%2%3",
      fglpkgutils.padRight("----", 31), fglpkgutils.padRight("-------", 13),
      "-----------")
  FOR i = 1 TO results.getLength()
    DISPLAY SFMT("  %1%2%3",
        fglpkgutils.padRight(results[i].name, 31),
        fglpkgutils.padRight(results[i].latestVersion, 13),
        NVL(results[i].description, ""))
  END FOR
  RETURN 0
END FUNCTION

--─── info ───────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdInfo(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE jsonOut, ok BOOLEAN
  DEFINE target, err STRING
  DEFINE vl registry.TVersionList
  DEFINE info registry.TPackageInfo
  DEFINE i INT
  DEFINE name, version STRING
  FOR i = 1 TO args.getLength()
    CASE
      WHEN args[i] == "--json"
        LET jsonOut = TRUE
      WHEN fglpkgutils.startsWith(args[i], "-")
        RETURN fail(SFMT('unknown flag "%1"', args[i]))
      OTHERWISE
        IF target IS NOT NULL THEN
          RETURN fail(SFMT('too many arguments: "%1"', args[i]))
        END IF
        LET target = args[i]
    END CASE
  END FOR
  IF target IS NULL THEN
    RETURN fail("usage: fglpkg info <package>[@<version>] [--json]")
  END IF

  CALL parsePackageArg(target) RETURNING name, version

  CALL registry.fetchVersionList(name) RETURNING ok, vl, err
  IF NOT ok THEN
    RETURN fail(privateHint(err, name))
  END IF
  IF vl.versions.getLength() == 0 THEN
    RETURN fail(SFMT('package "%1" has no published versions', name))
  END IF

  VAR resolvedVersion = version
  IF version IS NULL OR version == "latest" OR version == "*" THEN
    LET resolvedVersion = latestOf(vl.versions)
  END IF

  CALL registry.fetchInfo(name, resolvedVersion) RETURNING ok, info, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  IF info.name IS NULL THEN
    LET info.name = name
  END IF
  IF info.version IS NULL THEN
    LET info.version = resolvedVersion
  END IF

  IF jsonOut THEN
    DISPLAY manifest.prettyJSON(util.JSON.stringify(info))
    RETURN 0
  END IF
  CALL printInfo(info, vl, resolvedVersion == latestOf(vl.versions))
  RETURN 0
END FUNCTION

#+the newest entry of a version list by semver precedence
PRIVATE FUNCTION latestOf(versions fglpkgutils.TStringArr) RETURNS STRING
  DEFINE sorted fglpkgutils.TStringArr
  IF versions.getLength() == 0 THEN
    RETURN NULL
  END IF
  CALL versions.copyTo(sorted)
  CALL semver.sortVersionStrings(sorted)
  RETURN sorted[sorted.getLength()]
END FUNCTION

PRIVATE FUNCTION printInfo(
    info registry.TPackageInfo, vl registry.TVersionList, isLatest BOOLEAN)
  DEFINE i INT
  DEFINE majors fglpkgutils.TStringArr
  VAR header = SFMT("%1@%2", info.name, info.version)
  IF isLatest THEN
    LET header = header || " (latest)"
  END IF
  DISPLAY header
  DISPLAY fglpkgutils.repeatStr(fglpkgutils.C_LINE, header.getLength())
  DISPLAY ""
  CALL printField("Description", info.description)
  CALL printField("Author", info.author)
  CALL printField("License", info.license)
  CALL printField("Genero", info.genero)
  CALL printField("Published", info.publishedAt)
  IF info.checksum IS NOT NULL THEN
    CALL printField("Checksum", SFMT("sha256:%1", info.checksum))
  END IF
  CALL printField("Download", info.downloadUrl)
  IF info.variants.getLength() > 0 THEN
    FOR i = 1 TO info.variants.getLength()
      LET majors[i] = info.variants[i].generoMajor
    END FOR
    CALL printField("Variants", fglpkgutils.joinArr(majors, ", "))
  END IF
  IF info.fglDeps.getLength() > 0 THEN
    DISPLAY "\nFGL dependencies:"
    VAR names = info.fglDeps.getKeys()
    CALL fglpkgutils.sortStringArray(names)
    VAR width = 0
    FOR i = 1 TO names.getLength()
      IF names[i].getLength() > width THEN
        LET width = names[i].getLength()
      END IF
    END FOR
    FOR i = 1 TO names.getLength()
      DISPLAY SFMT("  %1  %2",
          fglpkgutils.padRight(names[i], width), info.fglDeps[names[i]])
    END FOR
  END IF
  IF info.javaDeps.getLength() > 0 THEN
    DISPLAY "\nJava dependencies:"
    FOR i = 1 TO info.javaDeps.getLength()
      DISPLAY SFMT("  %1:%2:%3",
          info.javaDeps[i].groupId, info.javaDeps[i].artifactId,
          info.javaDeps[i].version)
    END FOR
  END IF
  IF vl.versions.getLength() > 0 THEN
    DISPLAY SFMT("\nVersions (%1): %2",
        vl.versions.getLength(), fglpkgutils.joinArr(vl.versions, ", "))
  END IF
  DISPLAY SFMT("\nInstall: fglpkg install %1@%2", info.name, info.version)
END FUNCTION

PRIVATE FUNCTION printField(label STRING, value STRING)
  IF value IS NULL OR value.getLength() == 0 THEN
    RETURN
  END IF
  DISPLAY SFMT("  %1 %2", fglpkgutils.padRight(label || ":", 12), value)
END FUNCTION

--─── pack ───────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdPack(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE listOnly, ok BOOLEAN
  DEFINE outPath, err STRING
  DEFINE m manifest.TManifest
  DEFINE gv genero.TGeneroVersion
  DEFINE res pack.TPackResult
  DEFINE i INT

  LET i = 1
  WHILE i <= args.getLength()
    CASE args[i]
      WHEN "--list"
        LET listOnly = TRUE
      WHEN "-l"
        LET listOnly = TRUE
      WHEN "-o"
        IF i + 1 > args.getLength() THEN
          RETURN fail(SFMT("flag %1 requires a filename", args[i]))
        END IF
        LET i = i + 1
        LET outPath = args[i]
      WHEN "--output"
        IF i + 1 > args.getLength() THEN
          RETURN fail(SFMT("flag %1 requires a filename", args[i]))
        END IF
        LET i = i + 1
        LET outPath = args[i]
      OTHERWISE
        RETURN fail(SFMT('unexpected argument "%1"', args[i]))
    END CASE
    LET i = i + 1
  END WHILE

  IF NOT manifest.manifestExists(".") THEN
    RETURN fail(SFMT("no %1 in current directory — run 'fglpkg init' first",
            manifest.MANIFEST_FILENAME))
  END IF
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to load %1: %2", manifest.MANIFEST_FILENAME, err))
  END IF
  CALL manifest.validate(m) RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(SFMT("manifest is invalid: %1", err))
  END IF

  --pure-WC packages are genero-version-agnostic: skip runtime detection
  VAR generoMajor = ""
  IF manifest.hasBDLContent(m) OR NOT manifest.hasWebcomponents(m) THEN
    CALL genero.detect() RETURNING ok, gv, err
    IF NOT ok THEN
      RETURN fail(SFMT("cannot detect Genero version: %1", err))
    END IF
    LET generoMajor = genero.majorString(gv)
  END IF
  VAR variant = pack.artifactVariant(m, generoMajor)

  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  IF NOT ok THEN
    RETURN fail(SFMT("cannot build package zip: %1", err))
  END IF

  DISPLAY SFMT("Package:  %1@%2 (%3)",
      m.name, m.version, pack.variantDescription(variant))
  DISPLAY SFMT("Size:     %1 bytes", res.size)
  DISPLAY SFMT("SHA256:   %1", res.checksum)
  DISPLAY SFMT("Files:    %1", res.entries.getLength())
  FOR i = 1 TO res.entries.getLength()
    DISPLAY SFMT("  %1  %2",
        padLeft(SFMT("%1", res.entries[i].size), 8), res.entries[i].name)
  END FOR

  IF listOnly THEN
    CALL os.Path.delete(res.zipPath) RETURNING status
    RETURN 0
  END IF

  IF outPath IS NULL THEN
    LET outPath = pack.artifactFilename(m.name, m.version, variant)
  END IF
  VAR parent = os.Path.dirName(outPath)
  IF parent IS NOT NULL AND parent != "." THEN
    CALL fglpkgutils.mkdirp(parent)
  END IF
  IF os.Path.exists(outPath) THEN
    CALL os.Path.delete(outPath) RETURNING status
  END IF
  IF NOT os.Path.rename(res.zipPath, outPath) THEN
    IF NOT os.Path.copy(res.zipPath, outPath) THEN
      RETURN fail(SFMT("cannot write %1", outPath))
    END IF
    CALL os.Path.delete(res.zipPath) RETURNING status
  END IF
  DISPLAY SFMT("\nWrote %1", os.Path.fullPath(outPath))
  RETURN 0
END FUNCTION

PRIVATE FUNCTION padLeft(s STRING, width INT) RETURNS STRING
  VAR len = IIF(s IS NULL, 0, s.getLength())
  IF len >= width THEN
    RETURN s
  END IF
  RETURN fglpkgutils.concat(fglpkgutils.padRight("", width - len), s)
END FUNCTION

--─── version ────────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdVersion(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE gitMode, ok BOOLEAN
  DEFINE bumpArg, err STRING
  DEFINE m manifest.TManifest
  DEFINE cur, nxt semver.TSemver
  DEFINE i INT

  IF args.getLength() == 0 THEN
    DISPLAY SFMT("fglpkg version %1 (build %2)",
        commands.TOOL_VERSION, commands.TOOL_BUILD)
    RETURN 0
  END IF

  FOR i = 1 TO args.getLength()
    IF args[i] == "--git" THEN
      LET gitMode = TRUE
    ELSE
      IF bumpArg IS NOT NULL THEN
        RETURN fail(SFMT('unexpected argument "%1"', args[i]))
      END IF
      LET bumpArg = args[i]
    END IF
  END FOR
  IF bumpArg IS NULL THEN
    RETURN fail("usage: fglpkg version <patch|minor|major|prerelease|<semver>> [--git]")
  END IF

  IF NOT manifest.manifestExists(".") THEN
    RETURN fail(SFMT("no %1 in current directory — run 'fglpkg init' first",
            manifest.MANIFEST_FILENAME))
  END IF
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to load %1: %2", manifest.MANIFEST_FILENAME, err))
  END IF
  CALL semver.parseVersion(m.version) RETURNING ok, cur, err
  IF NOT ok THEN
    RETURN fail(SFMT('current version "%1" is not valid semver: %2',
            m.version, err))
  END IF
  CALL semver.bump(cur, bumpArg) RETURNING ok, nxt, err
  IF NOT ok THEN
    RETURN fail(err)
  END IF
  IF semver.versionToString(nxt) == semver.versionToString(cur) THEN
    RETURN fail(SFMT("new version %1 is the same as current — nothing to do",
            semver.versionToString(nxt)))
  END IF

  IF gitMode THEN
    LET err = requireCleanGitTree()
    IF err IS NOT NULL THEN
      RETURN fail(err)
    END IF
  END IF

  VAR oldStr = m.version
  LET m.version = semver.versionToString(nxt)
  CALL manifest.save(m, ".") RETURNING ok, err
  IF NOT ok THEN
    RETURN fail(SFMT("failed to write %1: %2", manifest.MANIFEST_FILENAME, err))
  END IF
  DISPLAY SFMT("%1 %2 %3 (in %4)",
      oldStr, fglpkgutils.C_ARROW, m.version, manifest.MANIFEST_FILENAME)

  IF gitMode THEN
    VAR tag = SFMT("v%1", m.version)
    VAR code = 0
    RUN SFMT("git add %1", manifest.MANIFEST_FILENAME) RETURNING code
    IF code == 0 THEN
      RUN SFMT("git commit -m %1", fglpkgutils.quoteForce(tag)) RETURNING code
    END IF
    IF code == 0 THEN
      RUN SFMT("git tag %1", tag) RETURNING code
    END IF
    IF code THEN
      RETURN fail("git command failed")
    END IF
    DISPLAY SFMT("Created commit and tag %1", tag)
    DISPLAY SFMT("To publish: git push && git push origin %1", tag)
  ELSE
    DISPLAY SFMT("To tag this release: git tag v%1", m.version)
  END IF
  RETURN 0
END FUNCTION

#+errors when the git working tree has uncommitted changes
PRIVATE FUNCTION requireCleanGitTree() RETURNS STRING
  DEFINE out, err STRING
  CALL fglpkgutils.getProgramOutputWithErr("git status --porcelain")
      RETURNING out, err
  IF err IS NOT NULL THEN
    RETURN "cannot run 'git status' (is this a git repo?)"
  END IF
  IF out.trim().getLength() > 0 THEN
    RETURN SFMT("git working tree is not clean; commit or stash changes before using --git\n\n%1",
        out)
  END IF
  RETURN NULL
END FUNCTION

--─── workspace ──────────────────────────────────────────────────────────────

PRIVATE FUNCTION cmdWorkspace(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE members fglpkgutils.TStringArr
  DEFINE ok BOOLEAN
  DEFINE ws workspace.TWorkspace
  DEFINE err STRING
  DEFINE i INT
  IF args.getLength() == 0 THEN
    RETURN fail("usage: fglpkg workspace <init|add|list|info>")
  END IF
  CASE args[1]
    WHEN "init"
      IF workspace.workspaceExists(".") THEN
        RETURN fail(SFMT("%1 already exists in the current directory",
                workspace.WORKSPACE_FILENAME))
      END IF
      FOR i = 2 TO args.getLength()
        LET members[members.getLength() + 1] = args[i]
      END FOR
      LET err = workspace.init(".", members)
      IF err IS NOT NULL THEN
        RETURN fail(err)
      END IF
      DISPLAY SFMT("%1 Created %2",
          fglpkgutils.C_CHECK, workspace.WORKSPACE_FILENAME)
      RETURN 0
    WHEN "add"
      IF args.getLength() < 2 THEN
        RETURN fail("usage: fglpkg workspace add <path>")
      END IF
      VAR addRoot = workspace.findRoot(".")
      IF addRoot IS NULL THEN
        RETURN fail("not inside a workspace — run 'fglpkg workspace init' first")
      END IF
      FOR i = 2 TO args.getLength()
        LET err = workspace.addMember(addRoot, args[i])
        IF err IS NOT NULL THEN
          RETURN fail(err)
        END IF
        DISPLAY SFMT('%1 Added "%2" to workspace',
            fglpkgutils.C_CHECK, args[i])
      END FOR
      RETURN 0
    WHEN "list"
      VAR listRoot = workspace.findRoot(".")
      IF listRoot IS NULL THEN
        RETURN fail("not inside a workspace")
      END IF
      CALL workspace.load(listRoot) RETURNING ok, ws, err
      IF NOT ok THEN
        RETURN fail(err)
      END IF
      DISPLAY SFMT("Workspace: %1", listRoot)
      FOR i = 1 TO ws.members.getLength()
        DISPLAY SFMT("  %1 v%2",
            fglpkgutils.padRight(ws.members[i].m.name, 30),
            ws.members[i].m.version)
      END FOR
      RETURN 0
    WHEN "info"
      VAR infoRoot = workspace.findRoot(".")
      IF infoRoot IS NULL THEN
        RETURN fail("not inside a workspace")
      END IF
      CALL workspace.load(infoRoot) RETURNING ok, ws, err
      IF NOT ok THEN
        RETURN fail(err)
      END IF
      CALL fglpkgutils.printStdoutNoNL(workspace.summary(ws))
      RETURN 0
    OTHERWISE
      RETURN fail(SFMT('unknown workspace subcommand "%1"', args[1]))
  END CASE
  RETURN 0
END FUNCTION

--─── login / logout / whoami ────────────────────────────────────────────────

PRIVATE FUNCTION cmdLogin(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE pat, err STRING
  DEFINE ok BOOLEAN
  DEFINE tokens credentials.TOAuthTokens
  DEFINE who registry.TWhoami
  DEFINE i INT

  LET i = 1
  WHILE i <= args.getLength()
    IF args[i] == "--token" THEN
      IF i + 1 > args.getLength() THEN
        RETURN fail("--token requires a value")
      END IF
      LET i = i + 1
      LET pat = args[i].trim()
    ELSE
      RETURN fail(SFMT('unknown argument "%1"\nusage: fglpkg login [--token <PAT>]',
              args[i]))
    END IF
    LET i = i + 1
  END WHILE

  VAR home = fglpkgutils.globalHome()
  VAR registryURL = fglpkgutils.registryBaseURL()

  IF pat IS NOT NULL THEN
    --PAT branch: store first, then verify (verification failure never
    --blocks storage — CI friendly)
    IF NOT fglpkgutils.startsWith(pat, "gpr_") THEN
      CALL fglpkgutils.printStderr(
          "  Warning: PAT does not start with 'gpr_' — storing anyway.")
    END IF
    CALL credentials.setPat(home, registryURL, pat, "")
    CALL registry.whoamiRequest(pat) RETURNING ok, who, err
    IF NOT ok THEN
      CALL fglpkgutils.printStderr(
          SFMT("  Warning: token stored but verification failed: %1", err))
      DISPLAY SFMT("%1 Token saved for %2", fglpkgutils.C_CHECK, registryURL)
      RETURN 0
    END IF
    DISPLAY SFMT("%1 Logged in to %2 as %3",
        fglpkgutils.C_CHECK, registryURL, registry.whoamiSubject(who))
    RETURN 0
  END IF

  --browser OAuth branch
  CALL oauth.runLogin(registryURL) RETURNING ok, tokens, err
  IF NOT ok THEN
    CALL fglpkgutils.printStderr(
        "  To use a Personal Access Token instead: fglpkg login --token <gpr_…>")
    RETURN fail(SFMT("login failed: %1", err))
  END IF
  CALL credentials.setOAuth(home, registryURL, tokens)
  CALL registry.whoamiRequest(tokens.accessToken) RETURNING ok, who, err
  IF NOT ok THEN
    CALL fglpkgutils.printStderr(
        SFMT("  Warning: tokens stored but verification failed: %1", err))
    DISPLAY SFMT("%1 Tokens saved for %2", fglpkgutils.C_CHECK, registryURL)
    RETURN 0
  END IF
  DISPLAY SFMT("%1 Logged in to %2 as %3",
      fglpkgutils.C_CHECK, registryURL, registry.whoamiSubject(who))
  IF tokens.refreshToken IS NOT NULL THEN
    DISPLAY "  Access token will be refreshed automatically while you stay signed in."
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION cmdLogout(args fglpkgutils.TStringArr) RETURNS INT
  UNUSED_VAR(args.getLength())
  VAR home = fglpkgutils.globalHome()
  VAR registryURL = fglpkgutils.registryBaseURL()
  IF NOT credentials.deleteCreds(home, registryURL) THEN
    DISPLAY SFMT("Not logged in to %1", registryURL)
    RETURN 0
  END IF
  DISPLAY SFMT("%1 Logged out from %2", fglpkgutils.C_CHECK, registryURL)
  RETURN 0
END FUNCTION

PRIVATE FUNCTION cmdWhoami(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE ok BOOLEAN
  DEFINE who registry.TWhoami
  DEFINE err STRING
  UNUSED_VAR(args.getLength())
  VAR home = fglpkgutils.globalHome()
  VAR registryURL = fglpkgutils.registryBaseURL()
  VAR tok = credentials.activeBearer(home, registryURL)
  IF tok IS NULL THEN
    RETURN fail(SFMT("not logged in to %1\nRun 'fglpkg login' first",
            registryURL))
  END IF
  CALL registry.whoamiRequest(tok) RETURNING ok, who, err
  IF NOT ok THEN
    RETURN fail(SFMT("whoami failed: %1", err))
  END IF
  DISPLAY SFMT("Registry: %1", registryURL)
  DISPLAY SFMT("User:     %1", registry.whoamiSubject(who))
  DISPLAY SFMT("Partner:  %1", NVL(who.partner.name, "(none)"))
  IF who.scopes.getLength() > 0 THEN
    DISPLAY SFMT("Scopes:   %1", fglpkgutils.joinArr(who.scopes, ", "))
  ELSE
    DISPLAY "Scopes:   (none)"
  END IF
  RETURN 0
END FUNCTION

--─── interactive prompts ────────────────────────────────────────────────────

#+prints a prompt and reads a full line from stdin; returns def on empty
#+input or EOF
PRIVATE FUNCTION promptWithDefault(label STRING, def STRING) RETURNS STRING
  DEFINE line STRING
  IF _stdin IS NULL THEN
    LET _stdin = base.Channel.create()
    --a NULL file name opens the standard input stream
    CALL _stdin.openFile(NULL, "r")
  END IF
  IF def IS NOT NULL AND def.getLength() > 0 THEN
    CALL fglpkgutils.printStdoutNoNL(SFMT("%1 (%2): ", label, def))
  ELSE
    CALL fglpkgutils.printStdoutNoNL(SFMT("%1: ", label))
  END IF
  LET line = _stdin.readLine()
  IF line IS NULL THEN
    RETURN def --EOF
  END IF
  LET line = fglpkgutils.trimWhiteSpace(line)
  IF line.getLength() == 0 THEN
    RETURN def
  END IF
  RETURN line
END FUNCTION

#+a valid registry slug: 2-64 chars, lowercase letters/digits/hyphens,
#+no leading or trailing hyphen
FUNCTION isValidPackageSlug(slug STRING) RETURNS BOOLEAN
  DEFINE i INT
  VAR len = slug.getLength()
  IF len < 2 OR len > 64 THEN
    RETURN FALSE
  END IF
  FOR i = 1 TO len
    VAR c = slug.getCharAt(i)
    VAR isLowerAlnum =
        (c >= "a" AND c <= "z" AND fglpkgutils.isLetter(c))
        OR fglpkgutils.isDigit(c)
    CASE
      WHEN i == 1 OR i == len
        IF NOT isLowerAlnum THEN
          RETURN FALSE
        END IF
      OTHERWISE
        IF NOT isLowerAlnum AND c != "-" THEN
          RETURN FALSE
        END IF
    END CASE
  END FOR
  RETURN TRUE
END FUNCTION

PRIVATE FUNCTION promptPackageSlug() RETURNS STRING
  VAR defaultSlug = os.Path.baseName(os.Path.pwd())
  IF NOT isValidPackageSlug(defaultSlug) THEN
    LET defaultSlug = ""
  END IF
  VAR name = promptWithDefault("Package name", defaultSlug)
  WHILE NOT isValidPackageSlug(name)
    DISPLAY SFMT('error: Invalid package name "%1" - must be 2-64 chars: lowercase letters, digits, hyphens',
        NVL(name, ""))
    LET name = promptWithDefault("Package name", defaultSlug)
  END WHILE
  RETURN name
END FUNCTION

PRIVATE FUNCTION promptPackageVersion() RETURNS STRING
  VAR version = promptWithDefault("Version", "0.1.0")
  WHILE NOT semver.validateVersion(version)
    DISPLAY SFMT('error: Invalid version "%1" - must be MAJOR.MINOR.PATCH, e.g. 1.0.0 or 2.1.0-rc.1',
        NVL(version, ""))
    LET version = promptWithDefault("Version", "0.1.0")
  END WHILE
  RETURN version
END FUNCTION

PRIVATE FUNCTION promptNonEmptyString(label STRING) RETURNS STRING
  VAR val = promptWithDefault(label, "")
  WHILE val IS NULL OR val.getLength() == 0
    DISPLAY SFMT("error: Invalid %1 - cannot be empty", label.toLowerCase())
    LET val = promptWithDefault(label, "")
  END WHILE
  RETURN val
END FUNCTION
