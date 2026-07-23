#+ sample package A (fglpkg name: sample-a) — no dependencies
#+ PACKAGE a; module name (Core) deliberately differs from the package
#+ name -- fglcomp fails to resolve a PUBLIC function from another
#+ package when a single-segment PACKAGE and its module share the exact
#+ same name (verified: "function 'main' not found in package 'a'" even
#+ though a.4gl compiles cleanly on its own and does export main as
#+ PUBLIC). Distinct PascalCase module names avoid the trap entirely.
PACKAGE a

PUBLIC FUNCTION main()
  DISPLAY "Hello package A"
END FUNCTION
