#+ sample package v5_42m (fglpkg name: sample-v5-42m) -- same demo as
#+ sample-v5 (uses base.Channel.getExitStatus(), introduced in Genero
#+ 5.00.03), but published as compiled .42m ONLY: fglpkg.json's "files"
#+ lists just "*.42m", so this source file is never shipped. A separate
#+ real .42m must be compiled and published per supported Genero major
#+ (there is no on-demand recompile to fall back on, unlike a
#+ source-only package).
FUNCTION main()
  DEFINE ch base.Channel
  DEFINE line STRING
  LET ch = base.Channel.create()
  CALL ch.openPipe("echo hello-from-v5_42m; exit 3", "r")
  LET line = ch.readLine()
  CALL ch.close()
  DISPLAY SFMT("Hello package v5_42m: pipe said '%1', child exit status %2",
      line, ch.getExitStatus())
END FUNCTION
