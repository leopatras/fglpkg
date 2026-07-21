@echo off
REM fglpkg-genero.bat: self-compiling launcher for the published "fglpkg"
REM source package (the Genero 4GL reimplementation of the fglpkg tool).
REM See fglpkg-genero (the Unix sh launcher) for the full rationale:
REM compiling in place on every invocation would let .42m files from one
REM Genero version linger and clash with another, so instead this syncs
REM sources into a per-Genero-version cache dir under %TEMP% and lets
REM `fglcomp --make` (only recompiles a source whose .42m is missing or
REM older than it) skip the real work once that version's cache is warm.
setlocal enabledelayedexpansion

set FGLPKGDIR=%~dp0
set THISDRIVE=%~dd0
FOR %%i IN ("%CD%") DO set MYDRIVE=%%~di

REM FGL_LENGTH_SEMANTICS=CHAR is required: fglpkgutils.cmpBytes (and any
REM other per-character loop) relies on ORD() resolving full Unicode code
REM points, not just the first byte of a multi-byte UTF-8 character.
set FGL_LENGTH_SEMANTICS=CHAR

REM "fglcomp 6.00.02 rev-5054478" -> "6.00" (major.minor only -- .42m
REM compatibility is a major.minor concern, not a patch-level one).
REM findstr isolates the version line first so a later output line
REM (e.g. "Genero 4gl compiler") can't clobber FGLVER.
set "FGLVER="
for /f "tokens=2" %%v in ('fglcomp -V 2^>^&1 ^| findstr /b "fglcomp"') do set "FGLVER=%%v"
if "%FGLVER%"=="" (
  echo fglpkg-genero: could not determine the Genero version ^(fglcomp -V failed^) 1>&2
  exit /b 1
)
for /f "tokens=1,2 delims=." %%a in ("%FGLVER%") do set "MAJMIN=%%a.%%b"

set CACHEBASE=%TEMP%\fglpkg-%MAJMIN%
set CACHEDIR=%CACHEBASE%\fglpkg
if not exist "%CACHEDIR%" mkdir "%CACHEDIR%"

REM /D copies only if the source is newer than an existing destination
REM file (or it doesn't exist yet) -- the same "cheap to redo, lets
REM fglcomp --make see accurate mtimes" idea as `cp -p` on Unix.
xcopy "%FGLPKGDIR%*.4gl" "%CACHEDIR%\" /D /Y /Q >NUL
xcopy "%FGLPKGDIR%*.inc" "%CACHEDIR%\" /D /Y /Q >NUL

set FGLLDPATH=%CACHEBASE%;%FGLLDPATH%

pushd %CD%
%THISDRIVE%
cd "%CACHEDIR%"
fglcomp --make *.4gl
if %errorlevel% neq 0 goto fglpkg_genero_err
popd
%MYDRIVE%
fglrun "%CACHEDIR%\main.42m" %*
goto :eof

:fglpkg_genero_err
popd
%MYDRIVE%
exit /b 1
