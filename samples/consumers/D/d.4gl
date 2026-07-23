#+ consumer project D — installs sample packages A, B and C via fglpkg
#+ and calls into each of them
IMPORT FGL a.Core AS a
IMPORT FGL b.Core AS b
IMPORT FGL c.Core AS c

MAIN
  DISPLAY "Hello from consumer D"
  CALL a.main()
  CALL b.main()
  CALL c.main()
END MAIN
