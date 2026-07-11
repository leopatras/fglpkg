#+ port of internal/manifest tests (manifest_test.go, webcomponent_test.go)
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
&include "testassert.inc"

MAIN
  CALL testLoadBasics()
  CALL testUnknownFieldRejection()
  CALL testDependenciesChecks()
  CALL testHooksChecks()
  CALL testScopedDependencies()
  CALL testJavaDependencies()
  CALL testValidate()
  CALL testValidateForPublish()
  CALL testWebcomponents()
  CALL testBinDocs()
  CALL testSerialization()
  CALL testSaveLoadRoundTrip()
  CALL testMavenURL()
  TSUMMARY()
END MAIN

FUNCTION mustLoad(text STRING) RETURNS manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  CALL manifest.loadFromString(text) RETURNING ok, m, err
  TOK(ok)
  TOK(err IS NULL)
  RETURN m
END FUNCTION

FUNCTION loadErr(text STRING) RETURNS STRING
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  CALL manifest.loadFromString(text) RETURNING ok, m, err
  TOK(NOT ok)
  RETURN err
END FUNCTION

FUNCTION testLoadBasics()
  VAR m = mustLoad('{"name":"p","version":"1.0.0","description":"d",'
      || '"author":"a","license":"MIT","genero":"^4.0.0",'
      || '"keywords":["db","util"],"$schema":"https://x/s.json",'
      || '"type":"webcomponent"}')
  TEQ(m.name, "p")
  TEQ(m.version, "1.0.0")
  TEQ(m.description, "d")
  TEQ(m.license, "MIT")
  TEQ(m.genero, "^4.0.0")
  TEQ(m.keywords.getLength(), 2)
  TEQ(m.schema, "https://x/s.json")
  TEQ(m.typ, "webcomponent")
  --malformed JSON
  VAR err = loadErr('{"name": ')
  TOK(fglpkgutils.contains(err, "malformed JSON"))
END FUNCTION

FUNCTION testUnknownFieldRejection()
  VAR err = loadErr('{"name":"p","version":"1.0.0","bogus":1}')
  TOK(fglpkgutils.contains(err, 'unknown field "bogus"'))
  --scripts gets the migration hint
  LET err = loadErr('{"name":"p","version":"1.0.0","scripts":{"build":"x"}}')
  TOK(fglpkgutils.contains(err, 'replaced by "hooks"'))
END FUNCTION

FUNCTION testDependenciesChecks()
  --flat dependencies rejected with hint
  VAR err = loadErr('{"name":"p","version":"1.0.0",'
      || '"dependencies":{"myutils":"^1.0.0"}}')
  TOK(fglpkgutils.contains(err, 'unknown key "myutils" under "dependencies"'))
  TOK(fglpkgutils.contains(err, 'dependencies.fgl.myutils'))
  --nested fgl accepted
  VAR m = mustLoad('{"name":"p","version":"1.0.0",'
      || '"dependencies":{"fgl":{"myutils":"^1.0.0","dbtools":"2.1.0"}}}')
  TEQ(m.dependencies.fgl.getLength(), 2)
  TEQ(m.dependencies.fgl["myutils"], "^1.0.0")
  --non-string fgl value rejected
  LET err = loadErr('{"name":"p","version":"1.0.0",'
      || '"dependencies":{"fgl":{"myutils":1}}}')
  TOK(fglpkgutils.contains(err, 'invalid "dependencies.fgl"'))
  --java accepted
  LET m = mustLoad('{"name":"p","version":"1.0.0","dependencies":{"java":['
      || '{"groupId":"com.google.code.gson","artifactId":"gson",'
      || '"version":"2.10.1"}]}}')
  TEQ(m.dependencies.java.getLength(), 1)
  TEQ(m.dependencies.java[1].artifactId, "gson")
END FUNCTION

FUNCTION testHooksChecks()
  --valid hooks accepted
  VAR m = mustLoad('{"name":"p","version":"1.0.0","hooks":{'
      || '"postinstall":[{"op":"mkdir","path":"gen"},'
      || '{"op":"copy-files","from":"tpl/*.4gl","to":"gen"}]}}')
  TEQ(m.hooks.getLength(), 1)
  TEQ(m.hooks["postinstall"].getLength(), 2)
  TEQ(m.hooks["postinstall"][2].src, "tpl/*.4gl")
  --unknown event rejected
  VAR err = loadErr('{"name":"p","version":"1.0.0","hooks":{'
      || '"postintsall":[{"op":"mkdir","path":"x"}]}}')
  TOK(fglpkgutils.contains(err, 'unknown hook event "postintsall"'))
  --unknown op rejected
  LET err = loadErr('{"name":"p","version":"1.0.0","hooks":{'
      || '"postinstall":[{"op":"shell","path":"x"}]}}')
  TOK(fglpkgutils.contains(err, 'unknown hook op "shell"'))
  --unknown op field rejected
  LET err = loadErr('{"name":"p","version":"1.0.0","hooks":{'
      || '"postinstall":[{"op":"mkdir","path":"x","cmd":"rm"}]}}')
  TOK(fglpkgutils.contains(err, 'unknown field "cmd" in hook operation'))
END FUNCTION

FUNCTION testScopedDependencies()
  DEFINE m manifest.TManifest
  DEFINE constraint, scope STRING
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  CALL manifest.addFGLDependencyScoped(m, "tester", "^1.0.0", "dev")
  CALL manifest.addFGLDependencyScoped(m, "telemetry", "^2.0.0", "optional")
  CALL manifest.addFGLDependency(m, "myutils", "^3.0.0")
  TEQ(m.devDependencies.fgl["tester"], "^1.0.0")
  TEQ(m.optionalDependencies.fgl["telemetry"], "^2.0.0")
  TEQ(m.dependencies.fgl["myutils"], "^3.0.0")

  --moving between scopes removes the old declaration
  CALL manifest.addFGLDependencyScoped(m, "tester", "^1.5.0", "prod")
  TEQ(m.dependencies.fgl["tester"], "^1.5.0")
  TOK(NOT m.devDependencies.fgl.contains("tester"))

  --find reports scope
  CALL manifest.findFGLDependency(m, "telemetry") RETURNING constraint, scope
  TEQ(constraint, "^2.0.0")
  TEQ(scope, "optional")
  CALL manifest.findFGLDependency(m, "unknown") RETURNING constraint, scope
  TOK(constraint IS NULL)
  TOK(scope IS NULL)

  --remove finds any scope
  TEQ(manifest.removeFGLDependency(m, "telemetry"), "optional")
  TOK(NOT m.optionalDependencies.fgl.contains("telemetry"))
  TOK(manifest.removeFGLDependency(m, "nope") IS NULL)
END FUNCTION

FUNCTION testJavaDependencies()
  DEFINE m manifest.TManifest
  DEFINE dep manifest.TJavaDependency
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  LET dep.groupId = "org.apache.poi"
  LET dep.artifactId = "poi"
  LET dep.version = "5.2.3"
  CALL manifest.addJavaDependency(m, dep)
  TEQ(m.dependencies.java.getLength(), 1)
  TEQ(manifest.javaKey(m.dependencies.java[1]), "org.apache.poi:poi")

  --replace by key updates in place
  LET dep.version = "5.2.5"
  CALL manifest.addJavaDependency(m, dep)
  TEQ(m.dependencies.java.getLength(), 1)
  TEQ(m.dependencies.java[1].version, "5.2.5")

  --scoped move
  CALL manifest.addJavaDependencyScoped(m, dep, "dev")
  TEQ(m.dependencies.java.getLength(), 0)
  TEQ(m.devDependencies.java.getLength(), 1)

  --remove reports scope
  TEQ(manifest.removeJavaDependency(m, "org.apache.poi:poi"), "dev")
  TOK(manifest.removeJavaDependency(m, "org.apache.poi:poi") IS NULL)
END FUNCTION

FUNCTION vErr(m manifest.TManifest) RETURNS STRING
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL manifest.validate(m) RETURNING ok, err
  TOK(NOT ok)
  RETURN err
END FUNCTION

FUNCTION testValidate()
  DEFINE m manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING

  --missing name / version
  TOK(fglpkgutils.contains(vErr(m), "missing required field: name"))
  LET m.name = "p"
  TOK(fglpkgutils.contains(vErr(m), "missing required field: version"))
  LET m.version = "1.0.0"
  CALL manifest.validate(m) RETURNING ok, err
  TOK(ok)

  --bad genero constraint
  LET m.genero = ">>nope"
  TOK(fglpkgutils.contains(vErr(m), "invalid genero constraint"))
  LET m.genero = "^4.0.0"

  --java dep missing fields
  LET m.dependencies.java[1].groupId = "g"
  TOK(fglpkgutils.contains(vErr(m), "java dependency missing required fields"))
  CALL m.dependencies.java.clear()

  --bin validations
  LET m.bin["do/it"] = "scripts/x.sh"
  TOK(fglpkgutils.contains(vErr(m), "must not contain path separators"))
  CALL m.bin.remove("do/it")
  LET m.bin["run"] = "/abs/path.sh"
  TOK(fglpkgutils.contains(vErr(m), "must be relative"))
  CALL m.bin.remove("run")

  --docs pattern validation
  LET m.docs[1] = "[bad"
  TOK(fglpkgutils.contains(vErr(m), "invalid docs glob pattern"))
  CALL m.docs.clear()

  --hook op validation through validate()
  LET m.hooks["postinstall"][1].op = "copy-files"
  LET m.hooks["postinstall"][1].src = "../evil"
  LET m.hooks["postinstall"][1].dst = "x"
  TOK(fglpkgutils.contains(vErr(m), "must not escape the package root"))
  LET m.hooks["postinstall"][1].src = "ok/*.4gl"
  CALL manifest.validate(m) RETURNING ok, err
  TOK(ok)
  --index in error message is zero based like Go
  LET m.hooks["postinstall"][1].src = NULL
  TOK(fglpkgutils.contains(vErr(m), "hooks.postinstall[0]"))
END FUNCTION

FUNCTION testValidateForPublish()
  DEFINE m manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  CALL manifest.validateForPublish(m) RETURNING ok, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "description is required"))
  TOK(fglpkgutils.contains(err, "repository is required"))
  TOK(fglpkgutils.contains(err, "author is required"))
  --license defaulted to UNLICENSED by newManifest, so not listed
  TOK(NOT fglpkgutils.contains(err, "license is required"))
  LET m.description = "d"
  LET m.repository = "https://github.com/x/y"
  LET m.author = "a"
  CALL manifest.validateForPublish(m) RETURNING ok, err
  TOK(ok)
END FUNCTION

FUNCTION testWebcomponents()
  DEFINE m manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  TOK(NOT manifest.hasWebcomponents(m))
  TOK(NOT manifest.hasBDLContent(m))
  LET m.webcomponents[1] = "3DChart" --digit leading is valid
  TOK(manifest.hasWebcomponents(m))
  CALL manifest.validate(m) RETURNING ok, err
  TOK(ok)
  --invalid name
  LET m.webcomponents[2] = "bad name"
  TOK(fglpkgutils.contains(vErr(m), "invalid COMPONENTTYPE"))
  --duplicate
  LET m.webcomponents[2] = "3DChart"
  TOK(fglpkgutils.contains(vErr(m), "duplicate COMPONENTTYPE"))
  CALL m.webcomponents.deleteElement(2)
  --BDL content detection
  LET m.main = "Main.42m"
  TOK(manifest.hasBDLContent(m))
  LET m.main = NULL
  LET m.dependencies.java[1].groupId = "g"
  TOK(manifest.hasBDLContent(m))
END FUNCTION

FUNCTION testBinDocs()
  DEFINE m manifest.TManifest
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  --deduplication + sorting
  LET m.bin["migrate"] = "scripts/migrate.sh"
  LET m.bin["migrate2"] = "scripts/migrate.sh"
  LET m.bin["build"] = "scripts/build.sh"
  VAR files = manifest.binFiles(m)
  TEQ(files.getLength(), 2)
  TEQ(files[1], "scripts/build.sh")
  TEQ(files[2], "scripts/migrate.sh")
END FUNCTION

FUNCTION testSerialization()
  DEFINE m manifest.TManifest
  LET m = manifest.newManifest("p", "1.0.0", "desc", "me")
  VAR js = manifest.toJSONString(m)
  --canonical key order
  TOK(js.getIndexOf('"name"', 1) < js.getIndexOf('"version"', 1))
  TOK(js.getIndexOf('"version"', 1) < js.getIndexOf('"description"', 1))
  TOK(js.getIndexOf('"description"', 1) < js.getIndexOf('"author"', 1))
  TOK(js.getIndexOf('"author"', 1) < js.getIndexOf('"license"', 1))
  TOK(js.getIndexOf('"license"', 1) < js.getIndexOf('"dependencies"', 1))
  --empty dev/optional deps omitted, dependencies always present
  TOK(fglpkgutils.contains(js, '"dependencies": {}'))
  TOK(NOT fglpkgutils.contains(js, "devDependencies"))
  TOK(NOT fglpkgutils.contains(js, "optionalDependencies"))
  --empty bin/docs omitted
  TOK(NOT fglpkgutils.contains(js, '"bin"'))
  TOK(NOT fglpkgutils.contains(js, '"docs"'))

  --fgl dependency keys are sorted
  CALL manifest.addFGLDependency(m, "zebra", "^1.0.0")
  CALL manifest.addFGLDependency(m, "alpha", "^1.0.0")
  LET js = manifest.toJSONString(m)
  TOK(js.getIndexOf('"alpha"', 1) < js.getIndexOf('"zebra"', 1))

  --dev deps emitted when non-empty
  CALL manifest.addFGLDependencyScoped(m, "tester", "^1.0.0", "dev")
  LET js = manifest.toJSONString(m)
  TOK(fglpkgutils.contains(js, '"devDependencies"'))

  --publishCopy strips them again without touching the original
  VAR pc = manifest.publishCopy(m)
  TOK(NOT fglpkgutils.contains(manifest.toJSONString(pc), "devDependencies"))
  TEQ(m.devDependencies.fgl.getLength(), 1)
END FUNCTION

FUNCTION testSaveLoadRoundTrip()
  DEFINE m, m2 manifest.TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR dir = fglpkgutils.makeTempDir()
  LET m = manifest.newManifest("roundtrip", "1.2.3", "desc", "me")
  LET m.genero = "^4.0.0"
  CALL manifest.addFGLDependency(m, "myutils", "^1.0.0")
  LET m.bin["migrate"] = "scripts/migrate.sh"
  LET m.docs[1] = "README.md"
  LET m.docs[2] = "docs/**/*.md"
  LET m.files[1] = "*.42m"
  LET m.hooks["postinstall"][1].op = "mkdir"
  LET m.hooks["postinstall"][1].path = "gen"
  CALL manifest.save(m, dir) RETURNING ok, err
  TOK(ok)
  CALL manifest.load(dir) RETURNING ok, m2, err
  TOK(ok)
  TEQ(m2.name, "roundtrip")
  TEQ(m2.version, "1.2.3")
  TEQ(m2.genero, "^4.0.0")
  TEQ(m2.dependencies.fgl["myutils"], "^1.0.0")
  TEQ(m2.bin["migrate"], "scripts/migrate.sh")
  TEQ(m2.docs[2], "docs/**/*.md")
  TEQ(m2.hooks["postinstall"][1].path, "gen")
  --stable serialization: saving the reloaded manifest is byte identical
  TEQ(manifest.toJSONString(m2), manifest.toJSONString(m))
  --file ends with newline
  VAR text = fglpkgutils.readTextFile(manifest.manifestPath(dir))
  TOK(fglpkgutils.endsWith(text, "}\n"))
  CALL fglpkgutils.rmrf(dir)

  --loadOrNew on an empty dir gives a blank manifest named after the dir
  VAR dir3 = fglpkgutils.makeTempDir()
  CALL manifest.loadOrNew(dir3) RETURNING ok, m2, err
  TOK(ok)
  TEQ(m2.version, "0.1.0")
  TEQ(m2.license, "UNLICENSED")
  CALL fglpkgutils.rmrf(dir3)
END FUNCTION

FUNCTION testMavenURL()
  DEFINE dep manifest.TJavaDependency
  LET dep.groupId = "com.google.code.gson"
  LET dep.artifactId = "gson"
  LET dep.version = "2.10.1"
  VAR wantURL =
      "https://repo1.maven.org/maven2/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
  TEQ(manifest.mavenURL(dep), wantURL)
  TEQ(manifest.jarFileName(dep), "gson-2.10.1.jar")
  LET dep.jar = "custom.jar"
  TEQ(manifest.jarFileName(dep), "custom.jar")
  TOK(fglpkgutils.endsWith(manifest.mavenURL(dep), "/custom.jar"))
  LET dep.url = "https://example.com/gson.jar"
  TEQ(manifest.mavenURL(dep), "https://example.com/gson.jar")
END FUNCTION
