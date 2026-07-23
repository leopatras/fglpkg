#+ consumer project v6-42m-consumer -- installs sample-v6-42m (Genero
#+ >=6.00, shipped as compiled .42m only) via fglpkg and calls into it
IMPORT FGL v6_42m

MAIN
  DISPLAY "Hello from v6-42m-consumer"
  CALL v6_42m.main()
END MAIN
