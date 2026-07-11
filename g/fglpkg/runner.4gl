#+ fglpkg bdl / run / docs — running programs and scripts from installed
#+ packages, and viewing their documentation
#+ port of cmdBdl/cmdRun/cmdDocs + helpers from internal/cli/cli.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.env
&include "myassert.inc"

--─── shared package lookup ──────────────────────────────────────────────────

#+looks for an installed package by name: local .fglpkg/packages/<name>
#+(only inside a project dir), then the global home
FUNCTION findInstalledPackage(name STRING)
    RETURNS(BOOLEAN, STRING, manifest.TManifest, STRING)
  DEFINE ok BOOLEAN
  DEFINE m, empty manifest.TManifest
  DEFINE err STRING
  IF fglpkgutils.isProjectDir(".") THEN
    VAR localDir = os.Path.join(
        fglpkgutils.packagesDir(fglpkgutils.localHome(os.Path.pwd())), name)
    IF manifest.manifestExists(localDir) THEN
      CALL manifest.load(localDir) RETURNING ok, m, err
      IF ok THEN
        RETURN TRUE, localDir, m, NULL
      END IF
    END IF
  END IF
  VAR globalDir = os.Path.join(
      fglpkgutils.packagesDir(fglpkgutils.globalHome()), name)
  IF manifest.manifestExists(globalDir) THEN
    CALL manifest.load(globalDir) RETURNING ok, m, err
    IF ok THEN
      RETURN TRUE, globalDir, m, NULL
    END IF
  END IF
  RETURN FALSE, NULL, empty,
      SFMT('package "%1" is not installed\nRun \'fglpkg install %2\' first',
          name, name)
END FUNCTION

--─── docs ───────────────────────────────────────────────────────────────────

#+the docs command; returns the process exit code
FUNCTION cmdDocs(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE ok BOOLEAN
  DEFINE pkgDir, err STRING
  DEFINE m manifest.TManifest
  DEFINE i INT
  IF args.getLength() == 0 THEN
    RETURN runFail("usage: fglpkg docs <package> [file]")
  END IF
  VAR pkgName = args[1]
  CALL findInstalledPackage(pkgName) RETURNING ok, pkgDir, m, err
  IF NOT ok THEN
    RETURN runFail(err)
  END IF
  IF m.docs.getLength() == 0 THEN
    DISPLAY SFMT('Package "%1" does not declare any documentation files.',
        pkgName)
    RETURN 0
  END IF
  VAR docFiles = collectDocFiles(pkgDir, m.docs)
  IF docFiles.getLength() == 0 THEN
    DISPLAY SFMT('Package "%1" declares doc patterns but no matching files were found.',
        pkgName)
    RETURN 0
  END IF

  IF args.getLength() < 2 THEN
    IF docFiles.getLength() == 1 THEN
      RETURN printDocFile(os.Path.join(pkgDir, docFiles[1]), docFiles[1])
    END IF
    DISPLAY SFMT("Documentation for %1@%2:", m.name, m.version)
    FOR i = 1 TO docFiles.getLength()
      DISPLAY SFMT("  %1", docFiles[i])
    END FOR
    DISPLAY SFMT("\nView a file: fglpkg docs %1 <file>", pkgName)
    RETURN 0
  END IF

  --display a specific doc file (full path or basename match)
  VAR requested = args[2]
  FOR i = 1 TO docFiles.getLength()
    IF docFiles[i] == requested
        OR os.Path.baseName(docFiles[i]) == requested THEN
      RETURN printDocFile(os.Path.join(pkgDir, docFiles[i]), docFiles[i])
    END IF
  END FOR
  RETURN runFail(
      SFMT('doc file "%1" not found in package %2\nRun \'fglpkg docs %3\' to list available files',
          requested, pkgName, pkgName))
END FUNCTION

PRIVATE FUNCTION printDocFile(fullPath STRING, relPath STRING) RETURNS INT
  DEFINE content STRING
  TRY
    LET content = fglpkgutils.readTextFile(fullPath)
  CATCH
    RETURN runFail(SFMT("cannot read %1", relPath))
  END TRY
  CALL fglpkgutils.printStdoutNoNL(content)
  RETURN 0
END FUNCTION

#+paths (relative to pkgDir) of all files matching any docs pattern,
#+deduplicated, in walk order
FUNCTION collectDocFiles(pkgDir STRING, patterns fglpkgutils.TStringArr)
    RETURNS fglpkgutils.TStringArr
  DEFINE out fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  DEFINE i, j INT
  VAR walked = glob.collectFiles(pkgDir)
  FOR i = 1 TO walked.getLength()
    IF seen.contains(walked[i]) THEN
      CONTINUE FOR
    END IF
    FOR j = 1 TO patterns.getLength()
      IF glob.matchGlob(patterns[j], walked[i]) THEN
        LET seen[walked[i]] = TRUE
        LET out[out.getLength() + 1] = walked[i]
        EXIT FOR
      END IF
    END FOR
  END FOR
  RETURN out
END FUNCTION

--─── bdl ────────────────────────────────────────────────────────────────────

#+the bdl command: runs a program from an installed package via fglrun,
#+propagating the child's exact exit code
FUNCTION cmdBdl(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE ok BOOLEAN
  DEFINE pkgDir, err, fglrun STRING
  DEFINE m manifest.TManifest
  DEFINE i INT
  IF args.getLength() == 0 THEN
    RETURN runFail("usage: fglpkg bdl <package> <module> [args...]\n       fglpkg bdl --list")
  END IF
  IF args[1] == "--list" OR args[1] == "-l" THEN
    RETURN cmdBdlList()
  END IF
  IF args.getLength() < 2 THEN
    RETURN runFail("usage: fglpkg bdl <package> <module> [args...]")
  END IF
  VAR pkgName = args[1]
  VAR moduleName = args[2]

  CALL findInstalledPackage(pkgName) RETURNING ok, pkgDir, m, err
  IF NOT ok THEN
    RETURN runFail(err)
  END IF

  --the module must be declared in the package's programs list
  VAR declared = FALSE
  FOR i = 1 TO m.programs.getLength()
    IF m.programs[i] == moduleName THEN
      LET declared = TRUE
      EXIT FOR
    END IF
  END FOR
  IF NOT declared THEN
    VAR available = fglpkgutils.joinArr(m.programs, ", ")
    IF available.getLength() == 0 THEN
      LET available = "none"
    END IF
    RETURN runFail(
        SFMT('module "%1" is not declared in %2\'s programs list\nAvailable programs: %3',
            moduleName, pkgName, available))
  END IF

  VAR workDir = pkgDir
  IF m.root IS NOT NULL AND m.root.getLength() > 0 THEN
    LET workDir = os.Path.join(pkgDir, m.root)
  END IF
  IF NOT os.Path.exists(workDir) THEN
    RETURN runFail(
        SFMT("program directory not found: %1\nTry reinstalling: fglpkg install",
            workDir))
  END IF
  IF NOT os.Path.exists(os.Path.join(workDir, moduleName || ".42m")) THEN
    RETURN runFail(SFMT("module file not found: %1",
            os.Path.join(workDir, moduleName || ".42m")))
  END IF

  CALL genero.fglrunPath() RETURNING fglrun, err
  IF err IS NOT NULL THEN
    RETURN runFail(err)
  END IF

  --environment: fglpkg-managed paths prepended to the existing values
  VAR home = fglpkgutils.globalHome()
  VAR fglldpath = env.mergeEnvVar(env.buildFGLLDPATH(home),
      fgl_getenv("FGLLDPATH"))
  CALL fgl_setenv("FGLLDPATH", fglldpath)
  VAR classpath = env.buildJavaClasspath(home)
  IF classpath.getLength() > 0 THEN
    CALL fgl_setenv("CLASSPATH",
        env.mergeEnvVar(classpath, fgl_getenv("CLASSPATH")))
  END IF

  --run from the package's program directory (process-wide chdir is fine:
  --we exit with the child's code right after)
  IF NOT os.Path.chDir(workDir) THEN
    RETURN runFail(SFMT("cannot enter %1", workDir))
  END IF
  VAR cmd = fglpkgutils.quote(fglrun)
  LET cmd = SFMT("%1 %2", cmd, moduleName)
  FOR i = 3 TO args.getLength()
    LET cmd = SFMT("%1 %2", cmd, fglpkgutils.quote(args[i]))
  END FOR
  VAR code = 0
  RUN cmd RETURNING code
  RETURN fglpkgutils.childExitCode(code)
END FUNCTION

#+lists BDL programs across installed packages (local then global)
FUNCTION cmdBdlList() RETURNS INT
  DEFINE lines fglpkgutils.TStringArr
  DEFINE i INT
  CALL collectProgramLines(lines)
  IF lines.getLength() == 0 THEN
    DISPLAY "No BDL programs found in installed packages."
    RETURN 0
  END IF
  DISPLAY "Available BDL programs:"
  DISPLAY SFMT("  %1 %2 %3",
      fglpkgutils.padRight("PROGRAM", 25),
      fglpkgutils.padRight("PACKAGE", 25), "SOURCE")
  DISPLAY SFMT("  %1 %2 %3",
      fglpkgutils.padRight("-------", 25),
      fglpkgutils.padRight("-------", 25), "------")
  FOR i = 1 TO lines.getLength()
    DISPLAY lines[i]
  END FOR
  RETURN 0
END FUNCTION

PRIVATE FUNCTION collectProgramLines(lines fglpkgutils.TStringArr)
  CALL lines.clear()
  IF fglpkgutils.isProjectDir(".") THEN
    CALL addProgramLines(lines,
        fglpkgutils.packagesDir(fglpkgutils.localHome(os.Path.pwd())),
        "local")
  END IF
  CALL addProgramLines(lines,
      fglpkgutils.packagesDir(fglpkgutils.globalHome()), "global")
END FUNCTION

PRIVATE FUNCTION addProgramLines(
    lines fglpkgutils.TStringArr, dir STRING, source STRING)
  DEFINE i, j INT
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  VAR entries = env.listSubdirs(dir)
  FOR i = 1 TO entries.getLength()
    IF NOT manifest.manifestExists(entries[i]) THEN
      CONTINUE FOR
    END IF
    CALL manifest.load(entries[i]) RETURNING ok, m, err
    IF NOT ok THEN
      CONTINUE FOR
    END IF
    FOR j = 1 TO m.programs.getLength()
      LET lines[lines.getLength() + 1] =
          SFMT("  %1 %2 %3",
              fglpkgutils.padRight(m.programs[j], 25),
              fglpkgutils.padRight(m.name, 25), source)
    END FOR
  END FOR
END FUNCTION

--─── run ────────────────────────────────────────────────────────────────────

#+the run command: executes a "bin" script from an installed package;
#+a failing script yields a generic exit 1 (Go parity — unlike bdl)
FUNCTION cmdRun(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE scriptArgs fglpkgutils.TStringArr
  DEFINE ok BOOLEAN
  DEFINE scriptPath, pkgName, err, cmd STRING
  DEFINE i INT
  IF args.getLength() == 0 THEN
    RETURN runFail("usage: fglpkg run <command> [-- args...]\n       fglpkg run --list")
  END IF
  IF args[1] == "--list" OR args[1] == "-l" THEN
    RETURN cmdRunList()
  END IF
  VAR commandName = args[1]

  --args after '--'; without '--' everything after the command is passed
  VAR dashIdx = 0
  FOR i = 2 TO args.getLength()
    IF args[i] == "--" THEN
      LET dashIdx = i
      EXIT FOR
    END IF
  END FOR
  IF dashIdx > 0 THEN
    FOR i = dashIdx + 1 TO args.getLength()
      LET scriptArgs[scriptArgs.getLength() + 1] = args[i]
    END FOR
  ELSE
    FOR i = 2 TO args.getLength()
      LET scriptArgs[scriptArgs.getLength() + 1] = args[i]
    END FOR
  END IF

  CALL findBinCommand(commandName) RETURNING ok, scriptPath, pkgName, err
  IF NOT ok THEN
    RETURN runFail(err)
  END IF
  DISPLAY SFMT('Running "%1" from package %2...', commandName, pkgName)

  CALL buildScriptCommand(scriptPath, scriptArgs) RETURNING cmd, err
  IF err IS NOT NULL THEN
    RETURN runFail(err)
  END IF
  VAR code = 0
  RUN cmd RETURNING code
  IF code != 0 THEN
    RETURN 1
  END IF
  RETURN 0
END FUNCTION

#+finds a bin command across local + global packages; ambiguity is an
#+error (no local-wins precedence — Go parity)
FUNCTION findBinCommand(commandName STRING)
    RETURNS(BOOLEAN, STRING, STRING, STRING)
  DEFINE paths, pkgs fglpkgutils.TStringArr
  CALL collectBinMatches(commandName, paths, pkgs)
  CASE
    WHEN paths.getLength() == 0
      RETURN FALSE, NULL, NULL,
          SFMT('command "%1" not found in any installed package\nRun \'fglpkg run --list\' to see available commands',
              commandName)
    WHEN paths.getLength() > 1
      RETURN FALSE, NULL, NULL,
          SFMT('command "%1" is defined by multiple packages: %2\nRemove or rename conflicting packages to resolve',
              commandName, fglpkgutils.joinArr(pkgs, ", "))
  END CASE
  RETURN TRUE, paths[1], pkgs[1], NULL
END FUNCTION

PRIVATE FUNCTION collectBinMatches(
    commandName STRING, paths fglpkgutils.TStringArr,
    pkgs fglpkgutils.TStringArr)
  CALL paths.clear()
  CALL pkgs.clear()
  IF fglpkgutils.isProjectDir(".") THEN
    CALL addBinMatches(commandName,
        fglpkgutils.packagesDir(fglpkgutils.localHome(os.Path.pwd())),
        paths, pkgs)
  END IF
  VAR globalPkgs = fglpkgutils.packagesDir(fglpkgutils.globalHome())
  IF globalPkgs
      != fglpkgutils.packagesDir(fglpkgutils.localHome(os.Path.pwd()))
      OR NOT fglpkgutils.isProjectDir(".") THEN
    CALL addBinMatches(commandName, globalPkgs, paths, pkgs)
  END IF
END FUNCTION

PRIVATE FUNCTION addBinMatches(
    commandName STRING, dir STRING, paths fglpkgutils.TStringArr,
    pkgs fglpkgutils.TStringArr)
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  VAR entries = env.listSubdirs(dir)
  FOR i = 1 TO entries.getLength()
    IF NOT manifest.manifestExists(entries[i]) THEN
      CONTINUE FOR
    END IF
    CALL manifest.load(entries[i]) RETURNING ok, m, err
    IF NOT ok OR NOT m.bin.contains(commandName) THEN
      CONTINUE FOR
    END IF
    VAR full = os.Path.join(entries[i], m.bin[commandName])
    IF os.Path.exists(full) THEN
      LET paths[paths.getLength() + 1] = full
      LET pkgs[pkgs.getLength() + 1] = m.name
    END IF
  END FOR
END FUNCTION

#+the OS-specific script invocation: non-Windows relies on the shebang;
#+Windows dispatches by extension
FUNCTION buildScriptCommand(scriptPath STRING, args fglpkgutils.TStringArr)
    RETURNS(STRING, STRING)
  DEFINE cmd STRING
  DEFINE i INT
  IF NOT fglpkgutils.isWin() THEN
    LET cmd = fglpkgutils.quote(scriptPath)
  ELSE
    VAR ext = os.Path.extension(scriptPath).toLowerCase()
    CASE ext
      WHEN "bat"
        LET cmd = SFMT("cmd.exe /C %1", fglpkgutils.quote(scriptPath))
      WHEN "cmd"
        LET cmd = SFMT("cmd.exe /C %1", fglpkgutils.quote(scriptPath))
      WHEN "ps1"
        LET cmd = SFMT("powershell.exe -ExecutionPolicy Bypass -File %1",
            fglpkgutils.quote(scriptPath))
      WHEN "py"
        LET cmd = SFMT("python %1", fglpkgutils.quote(scriptPath))
      WHEN "sh"
        LET cmd = SFMT("bash %1", fglpkgutils.quote(scriptPath))
      WHEN "exe"
        LET cmd = fglpkgutils.quote(scriptPath)
      OTHERWISE
        RETURN NULL,
            SFMT('cannot run "%1" on Windows: unsupported file extension ".%2"\nSupported extensions: .bat, .cmd, .ps1, .py, .sh, .exe',
                scriptPath, ext)
    END CASE
  END IF
  FOR i = 1 TO args.getLength()
    LET cmd = SFMT("%1 %2", cmd, fglpkgutils.quote(args[i]))
  END FOR
  RETURN cmd, NULL
END FUNCTION

#+lists bin commands across installed packages
FUNCTION cmdRunList() RETURNS INT
  DEFINE lines fglpkgutils.TStringArr
  DEFINE i INT
  CALL collectRunLines(lines)
  IF lines.getLength() == 0 THEN
    DISPLAY "No commands available."
    DISPLAY 'Packages can define commands via the "bin" field in fglpkg.json'
    RETURN 0
  END IF
  DISPLAY "Available commands:"
  DISPLAY SFMT("  %1 %2 %3 %4",
      fglpkgutils.padRight("COMMAND", 20),
      fglpkgutils.padRight("PACKAGE", 20),
      fglpkgutils.padRight("SOURCE", 10), "SCRIPT")
  DISPLAY SFMT("  %1 %2 %3 %4",
      fglpkgutils.padRight("-------", 20),
      fglpkgutils.padRight("-------", 20),
      fglpkgutils.padRight("------", 10), "------")
  FOR i = 1 TO lines.getLength()
    DISPLAY lines[i]
  END FOR
  RETURN 0
END FUNCTION

PRIVATE FUNCTION collectRunLines(lines fglpkgutils.TStringArr)
  CALL lines.clear()
  IF fglpkgutils.isProjectDir(".") THEN
    CALL addRunLines(lines,
        fglpkgutils.packagesDir(fglpkgutils.localHome(os.Path.pwd())),
        "local")
  END IF
  CALL addRunLines(lines,
      fglpkgutils.packagesDir(fglpkgutils.globalHome()), "global")
END FUNCTION

PRIVATE FUNCTION addRunLines(
    lines fglpkgutils.TStringArr, dir STRING, source STRING)
  DEFINE i, j INT
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  VAR entries = env.listSubdirs(dir)
  FOR i = 1 TO entries.getLength()
    IF NOT manifest.manifestExists(entries[i]) THEN
      CONTINUE FOR
    END IF
    CALL manifest.load(entries[i]) RETURNING ok, m, err
    IF NOT ok THEN
      CONTINUE FOR
    END IF
    VAR cmdNames = m.bin.getKeys()
    CALL glob.sortBytewise(cmdNames)
    FOR j = 1 TO cmdNames.getLength()
      LET lines[lines.getLength() + 1] =
          SFMT("  %1 %2 %3 %4",
              fglpkgutils.padRight(cmdNames[j], 20),
              fglpkgutils.padRight(m.name, 20),
              fglpkgutils.padRight(source, 10), m.bin[cmdNames[j]])
    END FOR
  END FOR
END FUNCTION

PRIVATE FUNCTION runFail(msg STRING) RETURNS INT
  CALL fglpkgutils.printStderr(msg)
  RETURN 1
END FUNCTION
