#+ consumer project v6-consumer -- installs sample-v6 (requires Genero
#+ >=6.00) via fglpkg and calls into it
IMPORT FGL v6

MAIN
  DISPLAY "Hello from v6-consumer"
  CALL v6.main()
END MAIN
