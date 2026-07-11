#+ consumer project D — installs sample packages A, B and C via fglpkg
#+ and calls into each of them
IMPORT FGL a
IMPORT FGL b
IMPORT FGL c

MAIN
  DISPLAY "Hello from consumer D"
  CALL a.main()
  CALL b.main()
  CALL c.main()
END MAIN
