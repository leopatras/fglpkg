#+ fglpkg — Genero BDL Package Manager (4GL implementation)
#+ entry point: dispatches to fglpkg.cli and propagates the exit code
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT FGL fglpkg.cli

MAIN
  DEFER INTERRUPT
  EXIT PROGRAM cli.cliExecute()
END MAIN
