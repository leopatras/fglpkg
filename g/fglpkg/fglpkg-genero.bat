@echo off
REM fglpkg-genero.bat: self-compiling launcher for the published "fglpkg"
REM source package (the Genero 4GL reimplementation of the fglpkg tool).
REM See fglpkg-genero (the Unix sh launcher) for the full rationale and the
REM -o/--make no-copy design: compiles straight from the installed package's
REM own source directory (%FGLPKGDIR%) into a per-Genero-version cache dir
REM under %TEMP%, via fglcomp's --output-dir (-o) combined with --make --
REM no copy step needed at all. fglcomp reads sources from %FGLPKGDIR%
REM directly and only writes/checks .42m under the cache dir, so
REM --make's staleness check always sees the real, live install directory,
REM including after the installed package itself gets updated.
REM
REM fglcomp treats PACKAGE fglpkg modules specially: given -o <dir>, it
REM creates <dir>\fglpkg\ itself (mirroring the package name) and writes
REM .42m there -- so this must run from the cache dir's PARENT (CACHEBASE,
REM which must NOT itself be named "fglpkg"), not from a "fglpkg" dir
REM itself, or you get a doubly-nested fglpkg\fglpkg\.
setlocal enabledelayedexpansion

set FGLPKGDIR=%~dp0

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
if not exist "%CACHEBASE%" mkdir "%CACHEBASE%"

set FGLLDPATH=%CACHEBASE%;%FGLLDPATH%

REM pushd (unlike plain cd) switches drives too, so this works even when
REM %TEMP% is on a different drive than the script or the caller's cwd --
REM simpler and more robust than the THISDRIVE/MYDRIVE dance the old
REM xcopy-based version of this script needed.
pushd "%CACHEBASE%"
if errorlevel 1 (
  echo fglpkg-genero: could not enter cache directory "%CACHEBASE%" 1>&2
  exit /b 1
)
fglcomp --make -o . "%FGLPKGDIR%*.4gl" 1>&2
if errorlevel 1 (
  popd
  exit /b 1
)
popd

fglrun "%CACHEBASE%\fglpkg\main.42m" %*
