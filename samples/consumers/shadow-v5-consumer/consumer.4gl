#+ consumer project shadow-v5-consumer -- installs BOTH sample-v5 and
#+ sample-shadow-v5, which each ship a module literally named v5.4gl/
#+ v5.42m. Whichever package's own directory happens to come first on
#+ FGLLDPATH wins this IMPORT FGL v5 -- see GIS-346.
IMPORT FGL v5

MAIN
  DISPLAY "Hello from shadow-v5-consumer"
  CALL v5.main()
END MAIN
