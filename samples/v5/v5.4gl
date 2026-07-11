#+ sample package v5 (fglpkg name: sample-v5)
#+ uses base.Channel.getExitStatus(), introduced in Genero 5.00.03 —
#+ hence the manifest constraint "genero": ">=5.00.03"
FUNCTION main()
  DEFINE ch base.Channel
  DEFINE line STRING
  LET ch = base.Channel.create()
  CALL ch.openPipe("echo hello-from-v5; exit 3", "r")
  LET line = ch.readLine()
  CALL ch.close()
  DISPLAY SFMT("Hello package v5: pipe said '%1', child exit status %2",
      line, ch.getExitStatus())
END FUNCTION
