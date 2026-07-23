#+ sample package com.fourjs.utils.cli -- demonstrates a multi-segment
#+ PACKAGE path (nested under com/fourjs/utils/cli/, matching the
#+ package declaration exactly)
PACKAGE com.fourjs.utils.cli

MAIN
  CALL myErr("something went wrong")
END MAIN

#+prints errstr to stderr together with the current stack trace, then
#+exits the program with status 1 -- similar to fglpkgutils.myErr in
#+this repo's own 4GL port (without its test-only _errHandler/
#+_exitHandler override hooks, which exist only to make that module's
#+own test suite able to capture errors instead of exiting)
PUBLIC FUNCTION myErr(errstr STRING)
  DEFINE ch base.Channel
  VAR msg = SFMT("ERROR: %1\nstack:\n%2", errstr, base.Application.getStackTrace())
  LET ch = base.Channel.create()
  CALL ch.openFile("<stderr>", "w")
  CALL ch.writeLine(msg)
  CALL ch.close()
  EXIT PROGRAM 1
END FUNCTION
