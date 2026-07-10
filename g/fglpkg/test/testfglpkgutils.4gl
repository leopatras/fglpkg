OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
&include "testassert.inc"

MAIN
  DEFINE arr fglpkgutils.TStringArr
  DEFINE parts fglpkgutils.TStringArr

  TEQ(fglpkgutils.replace("a.b.c", ".", "-"), "a-b-c")
  TEQ(fglpkgutils.backslash2slash("a\\b\\c"), "a/b/c")
  TOK(fglpkgutils.startsWith("fglpkg.json", "fglpkg"))
  TOK(NOT fglpkgutils.startsWith("fglpkg.json", "json"))
  TOK(fglpkgutils.endsWith("archive.zip", ".zip"))
  TOK(NOT fglpkgutils.endsWith("archive.zip", ".jar"))
  TOK(fglpkgutils.contains("a/b/c", "/b/"))
  TEQ(fglpkgutils.lastIndexOf("a.b.c", "."), 4)
  TEQ(fglpkgutils.trimWhiteSpace(" x\r\n "), "x")
  TEQ(fglpkgutils.parseInt(" 42 "), 42)
  TOK(fglpkgutils.parseInt("abc") IS NULL)
  TOK(fglpkgutils.isDigit("7"))
  TOK(NOT fglpkgutils.isDigit("x"))

  --split/join
  LET parts = fglpkgutils.splitOnChar("1.2.3", ".")
  TEQ(parts.getLength(), 3)
  TEQ(parts[1], "1")
  TEQ(parts[3], "3")
  LET parts = fglpkgutils.splitOnChar("a||b", "|")
  TEQ(parts.getLength(), 3)
  TEQ(parts[2], "")
  TEQ(fglpkgutils.joinArr(parts, "|"), "a||b")
  LET parts = fglpkgutils.splitOnChar("", ".")
  TEQ(parts.getLength(), 1)
  TEQ(parts[1], "")

  --sort
  LET arr[1] = "zeta"
  LET arr[2] = "alpha"
  LET arr[3] = "mike"
  CALL fglpkgutils.sortStringArray(arr)
  TEQ(arr[1], "alpha")
  TEQ(arr[2], "mike")
  TEQ(arr[3], "zeta")

  --env defaults + homes
  CALL fgl_setenv("FGLPKG_TESTVAR", "set")
  TEQ(fglpkgutils.getEnvDefault("FGLPKG_TESTVAR", "def"), "set")
  TEQ(fglpkgutils.getEnvDefault("FGLPKG_TESTVAR_UNSET", "def"), "def")
  CALL fgl_setenv("FGLPKG_HOME", "/tmp/pkghome")
  TEQ(fglpkgutils.globalHome(), "/tmp/pkghome")
  CALL fgl_setenv("FGLPKG_HOME", NULL)
  TOK(fglpkgutils.endsWith(fglpkgutils.globalHome(), ".fglpkg"))
  TEQ(fglpkgutils.localHome("/x/y"), "/x/y/.fglpkg")
  TEQ(fglpkgutils.packagesDir("/h"), "/h/packages")
  TEQ(fglpkgutils.jarsDir("/h"), "/h/jars")
  TEQ(fglpkgutils.webcomponentsDir("/h"), "/h/webcomponents")

  --registry URL normalization
  CALL fgl_setenv("FGLPKG_REGISTRY", "https://example.com/")
  TEQ(fglpkgutils.registryBaseURL(), "https://example.com")
  CALL fgl_setenv("FGLPKG_REGISTRY", NULL)
  TEQ(fglpkgutils.registryBaseURL(), "https://service.generointelligence.ai")

  --mkdirp + rmrf + tempdir + read/write file
  VAR tmp = fglpkgutils.makeTempDir()
  VAR sub = os.Path.join(os.Path.join(tmp, "a"), "b")
  CALL fglpkgutils.mkdirp(sub)
  TOK(os.Path.isDirectory(sub))
  VAR f = os.Path.join(sub, "t.txt")
  CALL fglpkgutils.writeStringToFile(f, "hello")
  TEQ(fglpkgutils.readTextFile(f), "hello")
  TOK(fglpkgutils.isProjectDir(tmp) == FALSE)
  CALL fglpkgutils.writeStringToFile(os.Path.join(tmp, "fglpkg.json"), "{}")
  TOK(fglpkgutils.isProjectDir(tmp))
  CALL fglpkgutils.rmrf(tmp)
  TOK(NOT os.Path.exists(tmp))

  --program output
  IF NOT fglpkgutils.isWin() THEN
    TEQ(fglpkgutils.getProgramOutput("echo hi"), "hi")
  END IF

  TSUMMARY()
END MAIN
