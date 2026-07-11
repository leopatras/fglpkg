#+ tests for runner.4gl: doc collection, bin lookup, script commands
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.runner
&include "testassert.inc"

DEFINE _origDir STRING

MAIN
  LET _origDir = os.Path.pwd()
  CALL testCollectDocFiles()
  CALL testBuildScriptCommand()
  CALL testFindBinCommand()
  TSUMMARY()
END MAIN

FUNCTION testCollectDocFiles()
  DEFINE patterns fglpkgutils.TStringArr
  VAR dir = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(dir, "docs/api"))
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "README.md"), "r")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "docs/guide.md"), "g")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "docs/api/x.md"), "x")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "notes.txt"), "n")
  LET patterns[1] = "README.md"
  LET patterns[2] = "docs/**/*.md"
  VAR files = runner.collectDocFiles(dir, patterns)
  TEQ(fglpkgutils.joinArr(files, ","), "README.md,docs/api/x.md,docs/guide.md")
  --a file matching two patterns appears once
  LET patterns[3] = "**/*.md"
  LET files = runner.collectDocFiles(dir, patterns)
  TEQ(files.getLength(), 3)
  CALL fglpkgutils.rmrf(dir)
END FUNCTION

FUNCTION testBuildScriptCommand()
  DEFINE args fglpkgutils.TStringArr
  DEFINE cmd, err STRING
  IF fglpkgutils.isWin() THEN
    RETURN --the Unix branch is what we can assert here
  END IF
  CALL runner.buildScriptCommand("/pkg/scripts/migrate.sh", args)
      RETURNING cmd, err
  TEQ(cmd, "/pkg/scripts/migrate.sh")
  TOK(err IS NULL)
  LET args[1] = "one"
  LET args[2] = "two words"
  CALL runner.buildScriptCommand("/pkg/x.sh", args) RETURNING cmd, err
  TEQ(cmd, '/pkg/x.sh one "two words"')
END FUNCTION

FUNCTION testFindBinCommand()
  DEFINE ok BOOLEAN
  DEFINE scriptPath, pkgName, err STRING
  --isolated global home with two packages defining bin commands
  VAR home = fglpkgutils.makeTempDir()
  CALL fgl_setenv("FGLPKG_HOME", home)
  VAR work = fglpkgutils.makeTempDir()
  TOK(os.Path.chDir(work)) --not a project dir: only global is scanned

  CALL fglpkgutils.mkdirp(os.Path.join(home, "packages/alpha/scripts"))
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "packages/alpha/fglpkg.json"),
      '{"name":"alpha","version":"1.0.0","bin":{"migrate":"scripts/mig.sh"}}')
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "packages/alpha/scripts/mig.sh"), "#!/bin/sh")
  CALL fglpkgutils.mkdirp(os.Path.join(home, "packages/beta"))
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "packages/beta/fglpkg.json"),
      '{"name":"beta","version":"1.0.0","bin":{"migrate":"m.sh","other":"o.sh"}}')
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "packages/beta/m.sh"), "#!/bin/sh")
  --beta/other declared but the script file is missing: never matches

  --unique command resolves
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "packages/beta/o.sh"), "#!/bin/sh")
  CALL runner.findBinCommand("other") RETURNING ok, scriptPath, pkgName, err
  TOK(ok)
  TEQ(pkgName, "beta")
  TOK(fglpkgutils.endsWith(scriptPath, "packages/beta/o.sh"))

  --ambiguous command errors with both package names
  CALL runner.findBinCommand("migrate") RETURNING ok, scriptPath, pkgName, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "defined by multiple packages: alpha, beta"))

  --unknown command
  CALL runner.findBinCommand("nope") RETURNING ok, scriptPath, pkgName, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, 'command "nope" not found'))

  TOK(os.Path.chDir(_origDir))
  CALL fgl_setenv("FGLPKG_HOME", NULL)
  CALL fglpkgutils.rmrf(home)
  CALL fglpkgutils.rmrf(work)
END FUNCTION
