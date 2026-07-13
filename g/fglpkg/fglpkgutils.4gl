#+ shared helpers for the fglpkg package manager (gwautils.4gl style)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
&define FGLPKG_UTILS_MODULE
&include "myassert.inc"

--output glyphs (match the Go implementation, no ANSI colors)
PUBLIC CONSTANT C_CHECK = "✓"
PUBLIC CONSTANT C_BULLET = "•"
PUBLIC CONSTANT C_ARROW = "→"
PUBLIC CONSTANT C_LINE = "─"

PUBLIC CONSTANT C_DEFAULT_REGISTRY = "https://service.generointelligence.ai"

PUBLIC TYPE TStringArr DYNAMIC ARRAY OF STRING
PUBLIC TYPE TStringDict DICTIONARY OF STRING

PUBLIC TYPE TExitHandler FUNCTION() RETURNS BOOLEAN
DEFINE _exitHandler TExitHandler
PUBLIC TYPE TLogHandler FUNCTION(msg STRING)
DEFINE _logHandler TLogHandler
PUBLIC TYPE TErrorHandler FUNCTION(err STRING)
DEFINE _errHandler TErrorHandler

DEFINE _isMac INT
DEFINE _isMacInit BOOLEAN
DEFINE _isLinux INT
DEFINE _isLinuxInit BOOLEAN

FUNCTION setExitHandler(exitHandler TExitHandler)
  LET _exitHandler = exitHandler
END FUNCTION

FUNCTION setLogHandler(logHandler TLogHandler)
  LET _logHandler = logHandler
END FUNCTION

FUNCTION setErrorHandler(errHandler TErrorHandler)
  LET _errHandler = errHandler
END FUNCTION

FUNCTION isWin() RETURNS BOOLEAN
  RETURN os.Path.separator().equals("\\")
END FUNCTION

FUNCTION isMac() RETURNS BOOLEAN
  IF _isMacInit == FALSE THEN
    LET _isMacInit = TRUE
    LET _isMac = IIF(isWin(), FALSE, getProgramOutput("uname") == "Darwin")
  END IF
  RETURN _isMac
END FUNCTION

FUNCTION isLinux() RETURNS BOOLEAN
  IF _isLinuxInit == FALSE THEN
    LET _isLinuxInit = TRUE
    LET _isLinux = IIF(isWin(), FALSE, getProgramOutput("uname") == "Linux")
  END IF
  RETURN _isLinux
END FUNCTION

FUNCTION printStderr(errstr STRING)
  DEFINE ch base.Channel
  LET ch = base.Channel.create()
  CALL ch.openFile("<stderr>", "w")
  CALL ch.writeLine(errstr)
  CALL ch.close()
END FUNCTION

FUNCTION printStdout(str STRING)
  DISPLAY str
END FUNCTION

FUNCTION printStdoutNoNL(str STRING)
  DEFINE ch base.Channel
  LET ch = base.Channel.create()
  CALL ch.openFile("", "w")
  CALL ch.writeNoNL(str)
END FUNCTION

FUNCTION myErr(errstr STRING)
  IF _errHandler IS NOT NULL THEN
    CALL _errHandler(errstr)
  ELSE
    VAR msg
        = SFMT("ERROR:%1 stack:\n%2", errstr, base.Application.getStackTrace())
    CALL printStderr(msg)
    CALL log(msg)
  END IF
  IF _exitHandler IS NOT NULL THEN
    IF _exitHandler() THEN
      EXIT PROGRAM 1
    END IF
  ELSE
    EXIT PROGRAM 1
  END IF
END FUNCTION

--user facing error: no stack trace, plain message on stderr
FUNCTION userError(errstr STRING)
  CALL printStderr(SFMT("Error: %1", errstr))
END FUNCTION

FUNCTION myWarning(errstr STRING)
  CALL printStderr(SFMT("Warning %1:%2", progName(), errstr))
END FUNCTION

FUNCTION log(msg STRING)
  IF _logHandler IS NOT NULL THEN
    CALL _logHandler(msg)
  ELSE
    IF fgl_getenv("VERBOSE") IS NOT NULL THEN
      DISPLAY "log:", msg
    END IF
  END IF
END FUNCTION

FUNCTION already_quoted(path STRING) RETURNS BOOLEAN
  DEFINE first, last STRING
  LET first = NVL(path.getCharAt(1), "NULL")
  LET last = NVL(path.getCharAt(path.getLength()), "NULL")
  IF isWin() THEN
    RETURN (first == '"' AND last == '"')
  END IF
  RETURN (first == "'" AND last == "'") OR (first == '"' AND last == '"')
END FUNCTION

FUNCTION quote(path STRING) RETURNS STRING
  RETURN quoteInt(path, FALSE)
END FUNCTION

FUNCTION quoteForce(path STRING) RETURNS STRING
  RETURN quoteInt(path, TRUE)
END FUNCTION

PRIVATE FUNCTION quoteInt(path STRING, force BOOLEAN) RETURNS STRING
  IF force OR path.getIndexOf(" ", 1) > 0 THEN
    IF NOT already_quoted(path) THEN
      LET path = '"', path, '"'
    END IF
  ELSE
    IF already_quoted(path) AND isWin() THEN --remove quotes(Windows)
      LET path = path.subString(2, path.getLength() - 1)
    END IF
  END IF
  RETURN path
END FUNCTION

#+quotes a URL for use in a shell command (gwautils.quoteUrl rule)
FUNCTION quoteUrl(url STRING) RETURNS STRING
  IF isWin() THEN
    RETURN winQuoteUrl(url)
  END IF
  IF url.getIndexOf(" ", 1) > 0
      OR url.getIndexOf("?", 1) > 0
      OR url.getIndexOf("&", 1) > 0 THEN
    LET url = '"', url, '"'
  END IF
  RETURN url
END FUNCTION

#+escapes % and & for cmd.exe `start` (gwautils.winQuoteUrl)
FUNCTION winQuoteUrl(url STRING) RETURNS STRING
  LET url = replace(url, "%", "^%")
  LET url = replace(url, "&", "^&")
  RETURN url
END FUNCTION

FUNCTION replace(src STRING, oldStr STRING, newString STRING) RETURNS STRING
  DEFINE b base.StringBuffer
  LET b = base.StringBuffer.create()
  CALL b.append(src)
  CALL b.replace(oldStr, newString, 0)
  RETURN b.toString()
END FUNCTION

FUNCTION backslash2slash(src STRING) RETURNS STRING
  RETURN replace(src, "\\", "/")
END FUNCTION

#+returns the last matching index
FUNCTION lastIndexOf(s STRING, sub STRING) RETURNS INT
  DEFINE startpos, idx, lastidx INT
  LET startpos = 1
  WHILE (idx := s.getIndexOf(sub, startpos)) > 0
    LET lastidx = idx
    LET startpos = idx + 1
  END WHILE
  RETURN lastidx
END FUNCTION

#+ returns TRUE if src contains the sub string sub
FUNCTION contains(src STRING, sub STRING) RETURNS BOOLEAN
  RETURN src.getIndexOf(sub, 1) > 0
END FUNCTION

FUNCTION startsWith(s STRING, sub STRING) RETURNS BOOLEAN
  RETURN s.getIndexOf(sub, 1) == 1
END FUNCTION

FUNCTION endsWith(s STRING, sub STRING) RETURNS BOOLEAN
  VAR idx = lastIndexOf(s, sub)
  IF idx < 1 THEN
    RETURN FALSE
  END IF
  RETURN idx + sub.getLength() - 1 == s.getLength()
END FUNCTION

FUNCTION trimWhiteSpace(s STRING) RETURNS STRING
  LET s = s.trim()
  LET s = replace(s, "\n", "")
  LET s = replace(s, "\r", "")
  RETURN s
END FUNCTION

FUNCTION getProgramOutputWithErr(cmd STRING) RETURNS(STRING, STRING)
  DEFINE tmpName, errStr STRING
  DEFINE txt TEXT
  DEFINE ret STRING
  DEFINE code INT
  LET tmpName = makeTempName()
  LET cmd = cmd, ">", quote(tmpName), " 2>&1"
  RUN cmd RETURNING code
  LOCATE txt IN FILE tmpName
  LET ret = txt
  CALL os.Path.delete(tmpName) RETURNING status
  IF code THEN
    LET errStr = ",\n  output:", ret
  ELSE
    --remove \r\n
    IF ret.getCharAt(ret.getLength()) == "\n" THEN
      LET ret = ret.subString(1, ret.getLength() - 1)
    END IF
    IF ret.getCharAt(ret.getLength()) == "\r" THEN
      LET ret = ret.subString(1, ret.getLength() - 1)
    END IF
  END IF
  RETURN ret, errStr
END FUNCTION

FUNCTION getProgramOutput(cmd STRING) RETURNS STRING
  DEFINE result, err STRING
  CALL getProgramOutputWithErr(cmd) RETURNING result, err
  IF err IS NOT NULL THEN
    CALL myErr(SFMT("failed to RUN:%1%2", cmd, err))
  END IF
  RETURN result
END FUNCTION

#+extracts the child exit code from a RUN ... RETURNING value: on Unix
#+the value is the raw wait status (exit code << 8); a signal death maps
#+to 1; Windows returns the exit code directly
FUNCTION childExitCode(runStatus INT) RETURNS INT
  IF isWin() THEN
    RETURN runStatus
  END IF
  IF runStatus MOD 256 != 0 THEN
    RETURN 1 --killed by a signal
  END IF
  RETURN runStatus / 256
END FUNCTION

FUNCTION checkRUN(cmd STRING)
  VAR code = 0
  RUN cmd RETURNING code
  CALL log(SFMT("checkRUN:%1->code:%2", cmd, code))
  IF code THEN
    CALL myErr(SFMT("RUN of:%1 failed with code:%2", cmd, code))
  END IF
END FUNCTION

#+computes a temporary file name
FUNCTION makeTempName() RETURNS STRING
  DEFINE tmpDir, tmpName, sbase, curr STRING
  DEFINE sb base.StringBuffer
  DEFINE i INT
  CASE
    WHEN fgl_getenv("FGLPKG_TMPDIR") IS NOT NULL
      LET tmpDir = fgl_getenv("FGLPKG_TMPDIR")
    WHEN isWin()
      LET tmpDir = fgl_getenv("TEMP")
    OTHERWISE
      LET tmpDir = "/tmp"
  END CASE
  LET curr = CURRENT
  LET sb = base.StringBuffer.create()
  CALL sb.append(curr)
  CALL sb.replace(" ", "_", 0)
  CALL sb.replace(":", "_", 0)
  CALL sb.replace(".", "_", 0)
  CALL sb.replace("-", "_", 0)
  LET sbase = SFMT("fglpkg_%1_%2", fgl_getpid(), sb.toString())
  LET sbase = os.Path.join(tmpDir, sbase)
  FOR i = 1 TO 10000
    LET tmpName = SFMT("%1%2.tmp", sbase, i)
    IF NOT os.Path.exists(tmpName) THEN
      RETURN tmpName
    END IF
  END FOR
  CALL myErr("makeTempName:Can't allocate a unique name")
  RETURN NULL
END FUNCTION

#+creates a temporary directory
FUNCTION makeTempDir() RETURNS STRING
  VAR tmpName = makeTempName()
  CALL mkdirp(tmpName)
  RETURN tmpName
END FUNCTION

FUNCTION readTextFile(filename STRING) RETURNS STRING
  DEFINE content STRING
  DEFINE t TEXT
  IF NOT os.Path.exists(filename) THEN
    CALL myErr(SFMT("can't open:%1", filename))
  END IF
  TRY
    LOCATE t IN FILE filename
    LET content = t
  CATCH
    CALL myErr(SFMT("readTextFile %1 error:%2", filename, err_get(status)))
  END TRY
  RETURN content
END FUNCTION

FUNCTION writeStringToFile(file STRING, content STRING)
  DEFINE ch base.Channel
  LET ch = base.Channel.create()
  CALL ch.openFile(file, "w")
  CALL ch.writeNoNL(content)
  CALL ch.close()
END FUNCTION

FUNCTION parseInt(s STRING) RETURNS INT
  DEFINE intVal INT
  LET s = s.trimWhiteSpace()
  LET intVal = s
  RETURN intVal
END FUNCTION

--checked variant: bails out if we don't return a valid INT
FUNCTION parseIntChecked(s STRING) RETURNS INT
  VAR intVal = parseInt(s)
  IF intVal IS NULL THEN
    CALL myErr(SFMT("No valid conversion from:'%1' to INT", s))
  END IF
  RETURN intVal
END FUNCTION

FUNCTION isDigit(c STRING) RETURNS BOOLEAN
  CONSTANT digits = "0123456789"
  RETURN digits.getIndexOf(c, 1) > 0
END FUNCTION

FUNCTION isLetter(c STRING) RETURNS BOOLEAN
  CONSTANT letters = "abcdefghijklmnopqrstuvwxyz"
  RETURN letters.getIndexOf(c.toLowerCase(), 1) > 0
END FUNCTION

FUNCTION isWinDriveInt(path STRING) RETURNS BOOLEAN
  RETURN isWin()
      AND path.getCharAt(2) == ":"
      AND (path.getCharAt(3) == "\\" OR path.getCharAt(3) == "/")
      AND isLetter(path.getCharAt(1))
END FUNCTION

FUNCTION pathStartsWithWinDrive(path STRING) RETURNS BOOLEAN
  RETURN path.getLength() >= 3 AND isWinDriveInt(path)
END FUNCTION

FUNCTION isAbsolutePath(path STRING) RETURNS BOOLEAN
  IF isWin() THEN
    IF pathStartsWithWinDrive(path) THEN
      RETURN TRUE
    END IF
  END IF
  RETURN startsWith(s: path, sub: "/")
      OR startsWith(s: path, sub: os.Path.separator())
END FUNCTION

#creates a directory path recursively like mkdir -p
FUNCTION mkdirp(path STRING)
  VAR winbase = FALSE
  VAR level = 0
  IF isWin() AND path.getIndexOf("\\", 1) > 0 THEN
    LET path = backslash2slash(path)
  END IF
  VAR basedir = "."
  CASE
    WHEN path.getCharAt(1) == "/"
      LET basedir = "/"
    --check for driveletter: as path start
    WHEN pathStartsWithWinDrive(path)
      LET basedir = path.subString(1, 2)
      LET winbase = TRUE
  END CASE
  VAR tok = base.StringTokenizer.create(path, "/")
  VAR part = basedir
  WHILE tok.hasMoreTokens()
    LET level = level + 1
    VAR next = tok.nextToken()
    IF level == 1 AND winbase THEN
      MYASSERT(basedir == next)
      CONTINUE WHILE
    END IF
    LET part = os.Path.join(part, next)
    IF NOT os.Path.exists(part) THEN
      IF NOT os.Path.mkdir(part) THEN
        CALL myErr(SFMT("can't create directory:%1", part))
      END IF
    ELSE
      IF NOT os.Path.isDirectory(part) THEN
        CALL myErr(SFMT("mkdirp: sub path:'%1' is not a directory", part))
      END IF
    END IF
  END WHILE
END FUNCTION

#+removes a directory tree (like rm -rf), must be given an existing dir
FUNCTION rmrf(path STRING)
  IF NOT os.Path.exists(path) THEN
    RETURN
  END IF
  MYASSERT_MSG(os.Path.isDirectory(path), SFMT("rmrf: not a directory:%1", path))
  IF isWin() THEN
    CALL checkRUN(SFMT("rmdir /S /Q %1", quote(backslash2slash(path))))
  ELSE
    CALL checkRUN(SFMT("rm -rf %1", quote(path)))
  END IF
END FUNCTION

FUNCTION progName() RETURNS STRING
  VAR ret = os.Path.baseName(arg_val(0))
  VAR ext = os.Path.extension(ret)
  IF ext.getLength() > 0 THEN
    LET ret = ret.subString(1, ret.getLength() - ext.getLength() - 1)
  END IF
  RETURN ret
END FUNCTION

--a whitespace-only value counts as unset: fgl_setenv(name, NULL) stores " "
FUNCTION getEnvDefault(name STRING, def STRING) RETURNS STRING
  VAR v = fgl_getenv(name)
  IF v IS NULL OR v.trim().getLength() == 0 THEN
    RETURN def
  END IF
  RETURN v
END FUNCTION

FUNCTION homeDir() RETURNS STRING
  VAR home = fgl_getenv(IIF(isWin(), "USERPROFILE", "HOME"))
  IF home IS NULL THEN
    CALL myErr(
        SFMT("environment variable %1 not set",
            IIF(isWin(), "USERPROFILE", "HOME")))
  END IF
  RETURN home
END FUNCTION

#+the global fglpkg home: $FGLPKG_HOME or ~/.fglpkg
FUNCTION globalHome() RETURNS STRING
  RETURN getEnvDefault("FGLPKG_HOME", os.Path.join(homeDir(), ".fglpkg"))
END FUNCTION

#+the project local fglpkg home: <dir>/.fglpkg
FUNCTION localHome(dir STRING) RETURNS STRING
  RETURN os.Path.join(dir, ".fglpkg")
END FUNCTION

#+a directory is a project dir if it has a .fglpkg/ dir or a fglpkg.json
FUNCTION isProjectDir(dir STRING) RETURNS BOOLEAN
  RETURN os.Path.isDirectory(localHome(dir))
      OR os.Path.exists(os.Path.join(dir, "fglpkg.json"))
END FUNCTION

FUNCTION packagesDir(home STRING) RETURNS STRING
  RETURN os.Path.join(home, "packages")
END FUNCTION

FUNCTION jarsDir(home STRING) RETURNS STRING
  RETURN os.Path.join(home, "jars")
END FUNCTION

FUNCTION webcomponentsDir(home STRING) RETURNS STRING
  RETURN os.Path.join(home, "webcomponents")
END FUNCTION

FUNCTION registryBaseURL() RETURNS STRING
  VAR url = getEnvDefault("FGLPKG_REGISTRY", C_DEFAULT_REGISTRY)
  --normalize: strip trailing slash
  WHILE url.getLength() > 1 AND endsWith(url, "/")
    LET url = url.subString(1, url.getLength() - 1)
  END WHILE
  RETURN url
END FUNCTION

--dynamic arrays are passed by reference: sorts in place; byte-wise
--(not locale collation) so output stays deterministic across locales
--and matches Go's sort.Strings — see g/BENCHMARKS.md
FUNCTION sortStringArray(arr TStringArr)
  CALL arr.sortByComparisonFunction(NULL, FALSE, FUNCTION cmpBytes)
END FUNCTION

#+explodes a string into one array element per character — UTF-8 safe
#+and correct regardless of FGL_LENGTH_SEMANTICS (unlike getCharAt,
#+which silently returns a space for byte offsets that land inside a
#+multi-byte character under BYTE semantics instead of erroring or
#+decoding it — see g/BENCHMARKS.md). s.split("") always yields exactly
#+length+2 elements: an empty leading and trailing element bracketing
#+one element per real character (DOC-6487); NULL/"" input yields the
#+two empty brackets and nothing else, i.e. a 0-length result here.
FUNCTION explodeChars(s STRING) RETURNS TStringArr
  DEFINE out TStringArr
  LET out = s.split("")
  --delete the trailing bracket first: deleting index 1 first would
  --shift every element down by one, moving the last real character
  --into the slot deleteElement(getLength()) would then remove instead
  CALL out.deleteElement(out.getLength())
  CALL out.deleteElement(1)
  RETURN out
END FUNCTION

#+escapes regex metacharacters so STRING.split matches s literally
#+(like Go regexp.QuoteMeta; split/replaceAll take regex patterns)
FUNCTION quoteRegexp(s STRING) RETURNS STRING
  DEFINE i INT
  CONSTANT metachars = "\\.+*?()|[]{}^$"
  VAR sb = base.StringBuffer.create()
  VAR chars = explodeChars(s)
  FOR i = 1 TO chars.getLength()
    IF metachars.getIndexOf(chars[i], 1) > 0 THEN
      CALL sb.append("\\")
    END IF
    CALL sb.append(chars[i])
  END FOR
  RETURN sb.toString()
END FUNCTION

#+splits a string on a literal single-character separator
#+(native split: getCharAt/subString walk-and-slice loops are O(n^2) —
#+subString costs O(start) even under byte length semantics)
FUNCTION splitOnChar(s STRING, sep STRING) RETURNS TStringArr
  DEFINE arr TStringArr
  DEFINE i INT
  IF sep IS NULL OR s IS NULL THEN
    --no separator ever matches / NULL input: one (empty) field,
    --same as the historical hand-rolled loop
    LET arr[1] = NVL(s, "")
    RETURN arr
  END IF
  LET arr = s.split(quoteRegexp(sep))
  --split yields empty-but-not-NULL fields; the historical loop yielded
  --"" (= NULL) — normalize so `IS NULL` checks in callers keep working
  FOR i = 1 TO arr.getLength()
    IF arr[i].getLength() == 0 THEN
      LET arr[i] = ""
    END IF
  END FOR
  RETURN arr
END FUNCTION

#+splits a string on a literal multi-character separator
FUNCTION splitOnString(s STRING, sep STRING) RETURNS TStringArr
  RETURN splitOnChar(s, sep)
END FUNCTION

#+splits a string on whitespace runs (like Go strings.Fields:
#+no empty fields, all-whitespace input yields an empty array)
FUNCTION splitFields(s STRING) RETURNS TStringArr
  DEFINE arr TStringArr
  DEFINE i INT
  IF s IS NULL THEN
    RETURN arr
  END IF
  VAR parts = s.split("[ \t\n\r]+")
  FOR i = 1 TO parts.getLength()
    IF parts[i].getLength() > 0 THEN
      LET arr[arr.getLength() + 1] = parts[i]
    END IF
  END FOR
  RETURN arr
END FUNCTION

#+deterministic string comparison matching Go's strings.Compare/
#+sort.Strings (byte-wise on the UTF-8 encoding, no locale collation).
#+Decodes via explodeChars + ORD() rather than looping getCharAt: under
#+BYTE semantics getCharAt silently corrupts multi-byte characters
#+(continuation bytes read back as a space), and comparing raw bytes
#+would also fail to fully differentiate characters sharing a lead byte
#+(e.g. all Latin-1 supplement letters start with 0xC3 in UTF-8).
#+ORD() of a whole decoded character returns its true Unicode code
#+point under CHAR semantics (see g/BENCHMARKS.md) — and since UTF-8
#+byte order and code point order are equivalent for valid UTF-8, this
#+is the same ordering Go's byte-wise compare produces. Requires
#+FGL_LENGTH_SEMANTICS=CHAR (set by the fglpkg launcher script).
#+params are named s1/s2 so this can be passed to
#+sortByComparisonFunction — function-reference compatibility in Genero
#+includes the parameter NAMES, not just the types.
FUNCTION cmpBytes(s1 STRING, s2 STRING) RETURNS INTEGER
  DEFINE i, alen, blen, ca, cb INT
  DEFINE a1, a2 TStringArr
  LET a1 = explodeChars(s1)
  LET a2 = explodeChars(s2)
  LET alen = a1.getLength()
  LET blen = a2.getLength()
  FOR i = 1 TO IIF(alen < blen, alen, blen)
    LET ca = ORD(a1[i])
    LET cb = ORD(a2[i])
    IF ca != cb THEN
      RETURN IIF(ca < cb, -1, 1)
    END IF
  END FOR
  IF alen == blen THEN
    RETURN 0
  END IF
  RETURN IIF(alen < blen, -1, 1)
END FUNCTION

#+pads a string with spaces to at least width characters (like %-Ns);
#+built with a StringBuffer — "" is NULL in 4GL, so `s || " "` would
#+propagate the NULL and never terminate
FUNCTION padRight(s STRING, width INT) RETURNS STRING
  VAR sb = base.StringBuffer.create()
  IF s IS NOT NULL THEN
    CALL sb.append(s)
  END IF
  WHILE sb.getLength() < width
    CALL sb.append(" ")
  END WHILE
  RETURN sb.toString()
END FUNCTION

#+repeats a string n times
FUNCTION repeatStr(s STRING, n INT) RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  FOR i = 1 TO n
    CALL sb.append(s)
  END FOR
  RETURN sb.toString()
END FUNCTION

#+NULL-safe concatenation (|| propagates NULL, this treats it as "")
FUNCTION concat(a STRING, b STRING) RETURNS STRING
  VAR sb = base.StringBuffer.create()
  IF a IS NOT NULL THEN
    CALL sb.append(a)
  END IF
  IF b IS NOT NULL THEN
    CALL sb.append(b)
  END IF
  RETURN sb.toString()
END FUNCTION

#+joins array elements with a separator
FUNCTION joinArr(arr TStringArr, sep STRING) RETURNS STRING
  DEFINE sb base.StringBuffer
  DEFINE i INT
  LET sb = base.StringBuffer.create()
  FOR i = 1 TO arr.getLength()
    IF i > 1 THEN
      CALL sb.append(sep)
    END IF
    CALL sb.append(arr[i])
  END FOR
  RETURN sb.toString()
END FUNCTION
