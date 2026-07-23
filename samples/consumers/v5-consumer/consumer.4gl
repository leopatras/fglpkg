#+ consumer project v5-consumer -- installs sample-v5 (requires Genero
#+ >=5.00.03) via fglpkg and calls into it
IMPORT FGL v5

MAIN
  DISPLAY "Hello from v5-consumer"
  CALL v5.main()
END MAIN
