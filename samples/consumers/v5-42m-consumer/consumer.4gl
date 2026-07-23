#+ consumer project v5-42m-consumer -- installs sample-v5-42m (Genero
#+ >=5.00.03, shipped as compiled .42m only) via fglpkg and calls into it
IMPORT FGL v5_42m

MAIN
  DISPLAY "Hello from v5-42m-consumer"
  CALL v5_42m.main()
END MAIN
