#+ declarative lifecycle hook execution (copy-files, mkdir)
#+ port of internal/hooks/hooks.go — arbitrary shell commands are
#+ intentionally not supported (supply-chain safety)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.manifest
&include "myassert.inc"

#+executes every operation declared for event with cwd as the working
#+directory; paths resolve relative to cwd, absolute paths and ".."
#+traversal are rejected; the first failing operation aborts the run
FUNCTION runHooks(m manifest.TManifest, event STRING, cwd STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE i INT
  DEFINE err STRING
  IF NOT m.hooks.contains(event) THEN
    RETURN TRUE, NULL
  END IF
  VAR ops = m.hooks[event]
  IF ops.getLength() == 0 THEN
    RETURN TRUE, NULL
  END IF
  VAR root = os.Path.fullPath(cwd)
  FOR i = 1 TO ops.getLength()
    CASE ops[i].op
      WHEN "copy-files"
        LET err = runCopyFiles(ops[i].src, ops[i].dst, root)
      WHEN "mkdir"
        LET err = runMkdir(ops[i].path, root)
      OTHERWISE
        LET err = SFMT('unknown op "%1"', ops[i].op)
    END CASE
    IF err IS NOT NULL THEN
      --Go indexes hook ops from 0
      RETURN FALSE, SFMT("hook %1 op[%2] %3: %4", event, i - 1, ops[i].op, err)
    END IF
  END FOR
  RETURN TRUE, NULL
END FUNCTION

#+creates the target directory (and parents); a pre-existing directory is
#+a no-op, a pre-existing file fails
PRIVATE FUNCTION runMkdir(path STRING, root STRING) RETURNS STRING
  DEFINE target, err STRING
  CALL resolveInside(root, path) RETURNING target, err
  IF err IS NOT NULL THEN
    RETURN err
  END IF
  IF os.Path.exists(target) THEN
    IF NOT os.Path.isDirectory(target) THEN
      RETURN SFMT('mkdir target "%1" exists and is not a directory', path)
    END IF
    RETURN NULL
  END IF
  CALL fglpkgutils.mkdirp(target)
  RETURN NULL
END FUNCTION

#+copies one file, a directory tree, or every glob match into dest
PRIVATE FUNCTION runCopyFiles(src STRING, dst STRING, root STRING)
    RETURNS STRING
  DEFINE srcAbs, dstAbs, err STRING
  DEFINE i INT
  CALL resolveInside(root, src) RETURNING srcAbs, err
  IF err IS NOT NULL THEN
    RETURN err
  END IF
  CALL resolveInside(root, dst) RETURNING dstAbs, err
  IF err IS NOT NULL THEN
    RETURN err
  END IF

  --direct path (no glob characters): copy file or recurse directory
  IF NOT fglpkgutils.contains(src, "*")
      AND NOT fglpkgutils.contains(src, "?")
      AND NOT fglpkgutils.contains(src, "[") THEN
    IF NOT os.Path.exists(srcAbs) THEN
      RETURN SFMT('source "%1": no such file or directory', src)
    END IF
    IF os.Path.isDirectory(srcAbs) THEN
      RETURN copyTree(srcAbs, dstAbs)
    END IF
    RETURN copyFileToDestOrPath(srcAbs, dstAbs)
  END IF

  --glob source: dest must be (or become) a directory; each match is
  --copied preserving its basename
  VAR matches = globMatches(srcAbs)
  IF matches.getLength() == 0 THEN
    RETURN SFMT('source glob "%1" matched no files', src)
  END IF
  CALL fglpkgutils.mkdirp(dstAbs)
  FOR i = 1 TO matches.getLength()
    VAR target = os.Path.join(dstAbs, os.Path.baseName(matches[i]))
    IF os.Path.isDirectory(matches[i]) THEN
      LET err = copyTree(matches[i], target)
    ELSE
      LET err = copyOneFile(matches[i], target)
    END IF
    IF err IS NOT NULL THEN
      RETURN err
    END IF
  END FOR
  RETURN NULL
END FUNCTION

#+expands a glob pattern like Go filepath.Glob: wildcard segments match
#+within one directory level (no "**")
FUNCTION globMatches(pattern STRING) RETURNS fglpkgutils.TStringArr
  DEFINE out, prefixSegs fglpkgutils.TStringArr
  --find the deepest static directory prefix
  VAR norm = fglpkgutils.backslash2slash(pattern)
  VAR segs = fglpkgutils.splitOnChar(norm, "/")
  VAR i INT
  VAR firstWild = 0
  FOR i = 1 TO segs.getLength()
    IF fglpkgutils.contains(segs[i], "*")
        OR fglpkgutils.contains(segs[i], "?")
        OR fglpkgutils.contains(segs[i], "[") THEN
      LET firstWild = i
      EXIT FOR
    END IF
  END FOR
  IF firstWild == 0 THEN
    IF os.Path.exists(pattern) THEN
      LET out[1] = pattern
    END IF
    RETURN out
  END IF
  FOR i = 1 TO firstWild - 1
    LET prefixSegs[i] = segs[i]
  END FOR
  VAR base = fglpkgutils.joinArr(prefixSegs, "/")
  IF base.getLength() == 0 THEN
    LET base = "."
  END IF
  CALL globWalk(base, norm, out)
  CALL glob.sortBytewise(out)
  RETURN out
END FUNCTION

PRIVATE FUNCTION globWalk(dir STRING, pattern STRING, out fglpkgutils.TStringArr)
  DEFINE entry STRING
  IF NOT os.Path.isDirectory(dir) THEN
    RETURN
  END IF
  VAR h = os.Path.dirOpen(dir)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    VAR full = SFMT("%1/%2", dir, entry)
    IF glob.pathMatch(pattern, full) THEN
      LET out[out.getLength() + 1] = full
    ELSE
      --descend only while the path can still be a prefix of the pattern
      IF os.Path.isDirectory(full)
          AND glob.pathMatch(patternPrefix(pattern, segCount(full)),
              full) THEN
        CALL globWalk(full, pattern, out)
      END IF
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
END FUNCTION

PRIVATE FUNCTION segCount(p STRING) RETURNS INT
  RETURN fglpkgutils.splitOnChar(p, "/").getLength()
END FUNCTION

PRIVATE FUNCTION patternPrefix(pattern STRING, n INT) RETURNS STRING
  DEFINE out fglpkgutils.TStringArr
  DEFINE i INT
  VAR segs = fglpkgutils.splitOnChar(pattern, "/")
  FOR i = 1 TO IIF(n < segs.getLength(), n, segs.getLength())
    LET out[i] = segs[i]
  END FOR
  RETURN fglpkgutils.joinArr(out, "/")
END FUNCTION

#+copies src into dst; when dst is an existing directory the file lands
#+inside it under its source basename
PRIVATE FUNCTION copyFileToDestOrPath(src STRING, dst STRING) RETURNS STRING
  IF os.Path.exists(dst) AND os.Path.isDirectory(dst) THEN
    LET dst = os.Path.join(dst, os.Path.baseName(src))
  ELSE
    CALL fglpkgutils.mkdirp(os.Path.dirName(dst))
  END IF
  RETURN copyOneFile(src, dst)
END FUNCTION

FUNCTION copyTree(src STRING, dst STRING) RETURNS STRING
  DEFINE entry, err STRING
  CALL fglpkgutils.mkdirp(dst)
  VAR h = os.Path.dirOpen(src)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    VAR s = os.Path.join(src, entry)
    VAR d = os.Path.join(dst, entry)
    IF os.Path.isDirectory(s) THEN
      LET err = copyTree(s, d)
    ELSE
      LET err = copyOneFile(s, d)
    END IF
    IF err IS NOT NULL THEN
      CALL os.Path.dirClose(h)
      RETURN err
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION copyOneFile(src STRING, dst STRING) RETURNS STRING
  CALL fglpkgutils.mkdirp(os.Path.dirName(dst))
  IF NOT os.Path.copy(src, dst) THEN
    RETURN SFMT("cannot copy %1 to %2", src, dst)
  END IF
  --preserve the executable bit
  CALL os.Path.chRwx(dst, os.Path.rwx(src)) RETURNING status
  RETURN NULL
END FUNCTION

#+joins root with rel and verifies the result does not escape root
FUNCTION resolveInside(root STRING, rel STRING) RETURNS(STRING, STRING)
  IF fglpkgutils.isAbsolutePath(rel) THEN
    RETURN NULL, SFMT('path "%1" must be relative', rel)
  END IF
  VAR joined = os.Path.fullPath(os.Path.join(root, rel))
  VAR rootClean = os.Path.fullPath(root)
  IF joined != rootClean
      AND NOT fglpkgutils.startsWith(joined,
          rootClean || os.Path.separator()) THEN
    RETURN NULL, SFMT('path "%1" escapes the package root', rel)
  END IF
  RETURN joined, NULL
END FUNCTION
