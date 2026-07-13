@echo off
rem fglpkg launcher: runs the 4GL implementation via fglrun
rem FGL_LENGTH_SEMANTICS=CHAR is required: fglpkgutils.cmpBytes (and any
rem other per-character loop) relies on ORD() resolving full Unicode
rem code points, not just the first byte of a multi-byte UTF-8
rem character -- see g/BENCHMARKS.md. LANG=.fglutf8 makes sure UTF-8 is
rem the active encoding (mirrors gwabuildtool.bat).
SETLOCAL
set FGLPKG_BINDIR=%~dp0
set FGLPKG_BINDIR=%FGLPKG_BINDIR:~0,-1%
for %%F in ("%FGLPKG_BINDIR%") do set FGLPKG_GDIR=%%~dpF
set FGLLDPATH=%FGLPKG_GDIR%
set LANG=.fglutf8
set FGL_LENGTH_SEMANTICS=CHAR
fglrun.exe "%FGLPKG_BINDIR%\main.42m" %*
ENDLOCAL
EXIT /B %ERRORLEVEL%
