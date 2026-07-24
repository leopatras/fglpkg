#+ sample package sample-shadow-v5 -- deliberately ships a module named
#+ v5.4gl/v5.42m, the SAME module name sample-v5 ships. buildFGLLDPATH
#+ adds each installed package's own directory to FGLLDPATH verbatim
#+ (one entry per package, in directory-listing order), so installing
#+ this package alongside sample-v5 makes `IMPORT FGL v5` ambiguous:
#+ whichever package's directory happens to come first on FGLLDPATH
#+ wins, and the other package's identically-named module becomes
#+ silently unreachable -- no warning today (GIS-346, third paragraph).
FUNCTION main()
  DISPLAY "Hello from sample-shadow-v5's OWN v5 module -- if you see this instead of 'Hello package v5', sample-shadow-v5 is shadowing sample-v5 on FGLLDPATH"
END FUNCTION
