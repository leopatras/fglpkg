#+ resolver benchmark — synthetic dependency graph, no network
#+ usage: fglrun benchresolver [N] [K]
#+   N = number of packages (default 1000)
#+   K = fan-out: pkg<i> depends on pkg<i+1>..pkg<i+K> (default 5)
#+ The Go twin lives in a throwaway test (see g/PORTING.md benchmarks);
#+ both build the identical graph on the fly inside the fetchers so only
#+ the resolver traversal is measured.
OPTIONS SHORT CIRCUIT
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.resolver

DEFINE _n INT
DEFINE _k INT

MAIN
  DEFINE root manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  DEFINE t0, t1 DATETIME YEAR TO FRACTION(3)
  DEFINE dt INTERVAL DAY(3) TO FRACTION(3)

  LET _n = arg_val(1)
  IF _n IS NULL OR _n < 1 THEN
    LET _n = 1000
  END IF
  LET _k = arg_val(2)
  IF _k IS NULL OR _k < 1 THEN
    LET _k = 5
  END IF

  CALL resolver.setFetchers(FUNCTION benchVersions, FUNCTION benchInfo,
      genero.mustParseGenero("4.01.12"))

  LET root.name = "bench"
  LET root.version = "1.0.0"
  LET root.dependencies.fgl[pkgName(1)] = "^1.0.0"

  LET t0 = CURRENT
  CALL resolver.resolve(root) RETURNING ok, plan, err
  LET t1 = CURRENT
  IF NOT ok THEN
    DISPLAY "resolve failed: ", err
    EXIT PROGRAM 1
  END IF
  LET dt = t1 - t0
  DISPLAY SFMT("4gl N=%1 K=%2 resolved=%3 elapsed=%4",
      _n, _k, plan.packages.getLength(), dt)
END MAIN

FUNCTION pkgName(i INT) RETURNS STRING
  RETURN SFMT("pkg%1", i USING "&&&&&&")
END FUNCTION

#+every package has the same three versions; ^1.0.0 picks 1.2.0
FUNCTION benchVersions(name STRING)
    RETURNS(BOOLEAN, resolver.TCandidateVersions, STRING)
  DEFINE out, empty resolver.TCandidateVersions
  IF NOT fglpkgutils.startsWith(name, "pkg") THEN
    RETURN FALSE, empty, SFMT("package not found: %1", name)
  END IF
  LET out[1].version = "1.0.0"
  LET out[2].version = "1.1.0"
  LET out[3].version = "1.2.0"
  RETURN TRUE, out, NULL
END FUNCTION

#+pkg<i> depends on pkg<i+1>..pkg<i+K> (capped at N)
FUNCTION benchInfo(name STRING, version STRING, generoMajor STRING)
    RETURNS(BOOLEAN, registry.TPackageInfo, STRING)
  DEFINE info registry.TPackageInfo
  DEFINE i, idx INT
  IF generoMajor IS NULL THEN
  END IF
  VAR s = name.subString(4, name.getLength())
  LET idx = s
  LET info.name = name
  LET info.version = version
  LET info.downloadUrl = SFMT("https://example.com/%1-%2.zip", name, version)
  LET info.checksum = "deadbeef"
  LET info.variant = "genero4"
  FOR i = idx + 1 TO idx + _k
    IF i > _n THEN
      EXIT FOR
    END IF
    LET info.fglDeps[pkgName(i)] = "^1.0.0"
  END FOR
  RETURN TRUE, info, NULL
END FUNCTION
