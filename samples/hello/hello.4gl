#+ sample package hello (fglpkg name: hello) — demonstrates the PACKAGE
#+ statement with one PUBLIC FUNCTION, importable as `IMPORT FGL hello`
PACKAGE hello

MAIN
  CALL helloworld()
END MAIN

PUBLIC FUNCTION helloworld()
  DISPLAY "Hello, world!"
END FUNCTION
