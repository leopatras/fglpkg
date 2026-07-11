#+ port of internal/cli/glob_test.go and ignore_test.go pattern tests
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.ignore
&include "testassert.inc"

MAIN
  CALL testMatchGlobSimple()
  CALL testMatchGlobDoublestar()
  CALL testPathMatch()
  CALL testIgnoreRules()
  CALL testLoadIgnore()
  CALL testCollectFiles()
  CALL testGithubHelpers()
  TSUMMARY()
END MAIN

FUNCTION checkGlob(pattern STRING, path STRING, want BOOLEAN)
  VAR got = glob.matchGlob(pattern, path)
  VAR gotDesc = SFMT("matchGlob('%1'&#44;'%2') -> %3", pattern, path, got)
  VAR wantDesc = SFMT("matchGlob('%1'&#44;'%2') -> %3", pattern, path, want)
  TEQ(gotDesc, wantDesc)
END FUNCTION

FUNCTION testMatchGlobSimple()
  CALL checkGlob("*.md", "README.md", TRUE)
  CALL checkGlob("*.md", "docs/guide.md", FALSE)
  CALL checkGlob("*.md", "README.txt", FALSE)
  CALL checkGlob("README.md", "README.md", TRUE)
  CALL checkGlob("README.md", "docs/README.md", FALSE)
  CALL checkGlob("*.go", "main.go", TRUE)
  CALL checkGlob("*.go", "cmd/main.go", FALSE)
  CALL checkGlob("*.go", "main.rs", FALSE)
  CALL checkGlob("CHANGELOG.md", "CHANGELOG.md", TRUE)
  CALL checkGlob("CHANGELOG.md", "OTHER.md", FALSE)
END FUNCTION

FUNCTION testMatchGlobDoublestar()
  CALL checkGlob("docs/**/*.md", "docs/guide.md", TRUE)
  CALL checkGlob("docs/**/*.md", "docs/api/guide.md", TRUE)
  CALL checkGlob("docs/**/*.md", "docs/api/v2/guide.md", TRUE)
  CALL checkGlob("docs/**/*.md", "docs/guide.txt", FALSE)
  CALL checkGlob("docs/**/*.md", "src/guide.md", FALSE)
  CALL checkGlob("**/*.md", "README.md", TRUE)
  CALL checkGlob("**/*.md", "docs/guide.md", TRUE)
  CALL checkGlob("**/*.md", "a/b/c/deep.md", TRUE)
  CALL checkGlob("**/*.md", "test.txt", FALSE)
  CALL checkGlob("docs/**", "docs/guide.md", TRUE)
  CALL checkGlob("docs/**", "docs/api/guide.md", TRUE)
  CALL checkGlob("docs/**", "src/guide.md", FALSE)
END FUNCTION

FUNCTION testPathMatch()
  --star does not cross separators
  TOK(glob.pathMatch("*.42m", "main.42m"))
  TOK(NOT glob.pathMatch("*.42m", "sub/main.42m"))
  TOK(glob.pathMatch("a/*/c", "a/b/c"))
  TOK(NOT glob.pathMatch("a/*/c", "a/b/d/c"))
  --question mark
  TOK(glob.pathMatch("m?in.go", "main.go"))
  TOK(NOT glob.pathMatch("m?in.go", "mainn.go"))
  TOK(NOT glob.pathMatch("a?b", "a/b"))
  --character classes
  TOK(glob.pathMatch("[abc].txt", "b.txt"))
  TOK(NOT glob.pathMatch("[abc].txt", "d.txt"))
  TOK(glob.pathMatch("[a-c].txt", "b.txt"))
  TOK(NOT glob.pathMatch("[^a-c].txt", "b.txt"))
  TOK(glob.pathMatch("[^a-c].txt", "d.txt"))
  --escaping
  TOK(glob.pathMatch("a\\*b", "a*b"))
  TOK(NOT glob.pathMatch("a\\*b", "axb"))
  --malformed patterns never match
  TOK(NOT glob.pathMatch("[abc", "a"))
  TOK(NOT glob.pathMatch("a\\", "a"))
  --empty cases
  TOK(glob.pathMatch("", ""))
  TOK(NOT glob.pathMatch("", "x"))
  TOK(glob.pathMatch("*", "anything"))
  TOK(NOT glob.pathMatch("*", "any/thing"))
END FUNCTION

FUNCTION mkRule(pattern STRING, negate BOOLEAN, dirOnly BOOLEAN)
    RETURNS ignore.TIgnoreRule
  DEFINE r ignore.TIgnoreRule
  LET r.pattern = pattern
  LET r.negate = negate
  LET r.dirOnly = dirOnly
  RETURN r
END FUNCTION

FUNCTION testIgnoreRules()
  DEFINE rules ignore.TIgnoreRules

  --basename matching (unanchored)
  CALL rules.clear()
  LET rules[1] = mkRule("*.bak", FALSE, FALSE)
  TOK(ignore.shouldExclude(rules, "Main.bak", FALSE))
  TOK(ignore.shouldExclude(rules, "nested/path/Old.bak", FALSE))
  TOK(NOT ignore.shouldExclude(rules, "Main.42m", FALSE))

  --unanchored segment matching
  CALL rules.clear()
  LET rules[1] = mkRule("build", FALSE, FALSE)
  TOK(ignore.shouldExclude(rules, "build/output.txt", FALSE))
  TOK(ignore.shouldExclude(rules, "nested/build/x.txt", FALSE))

  --anchored pattern
  CALL rules.clear()
  LET rules[1] = mkRule("/build", FALSE, FALSE)
  TOK(ignore.shouldExclude(rules, "build", FALSE))
  TOK(NOT ignore.shouldExclude(rules, "nested/build", FALSE))

  --negation re-includes
  CALL rules.clear()
  LET rules[1] = mkRule("*.log", FALSE, FALSE)
  LET rules[2] = mkRule("important.log", TRUE, FALSE)
  TOK(ignore.shouldExclude(rules, "foo.log", FALSE))
  TOK(NOT ignore.shouldExclude(rules, "important.log", FALSE))

  --dir-only rule
  CALL rules.clear()
  LET rules[1] = mkRule("cache", FALSE, TRUE)
  TOK(ignore.shouldExclude(rules, "cache", TRUE))
  TOK(NOT ignore.shouldExclude(rules, "cache", FALSE))

  --empty set is a noop
  CALL rules.clear()
  TOK(NOT ignore.shouldExclude(rules, "anything", FALSE))
END FUNCTION

FUNCTION testLoadIgnore()
  DEFINE rules ignore.TIgnoreRules
  VAR dir = fglpkgutils.makeTempDir()
  VAR body = "# leading comment\n*.bak\n\n# blank above\nbuild/\n!build/keep.txt\n"
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(dir, ".fglpkgignore"), body)
  LET rules = ignore.loadIgnore(dir)
  TEQ(rules.getLength(), 3)
  TEQ(rules[1].pattern, "*.bak")
  TOK(NOT rules[1].negate)
  TEQ(rules[2].pattern, "build")
  TOK(rules[2].dirOnly)
  TEQ(rules[3].pattern, "build/keep.txt")
  TOK(rules[3].negate)
  CALL fglpkgutils.rmrf(dir)

  --missing file yields an empty set
  VAR dir2 = fglpkgutils.makeTempDir()
  LET rules = ignore.loadIgnore(dir2)
  TEQ(rules.getLength(), 0)
  CALL fglpkgutils.rmrf(dir2)
END FUNCTION

FUNCTION testCollectFiles()
  VAR dir = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(dir, "sub/deep"))
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "b.txt"), "b")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "a.txt"), "a")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "sub/c.txt"), "c")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "sub/deep/d.txt"), "d")
  VAR files = glob.collectFiles(dir)
  TEQ(fglpkgutils.joinArr(files, ","), "a.txt,b.txt,sub/c.txt,sub/deep/d.txt")
  CALL fglpkgutils.rmrf(dir)
END FUNCTION

FUNCTION testGithubHelpers()
  TEQ(glob.variantAssetName("poiapi", "1.0.0", 4), "poiapi-1.0.0-genero4.zip")
  TOK(glob.isGitHubURL("https://api.github.com/repos/x/y/releases"))
  TOK(NOT glob.isGitHubURL("https://example.com/x.zip"))
END FUNCTION
