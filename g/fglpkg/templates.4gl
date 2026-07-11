#+ project templates for `fglpkg init --template`
#+ port of internal/cli/templates.go — templates set up a pre-filled
#+ fglpkg.json plus a small directory structure and never declare
#+ external dependencies
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
&include "myassert.inc"

PRIVATE TYPE TTemplateFile RECORD
  path STRING,
  content STRING
END RECORD

#+whether name is a known template
FUNCTION templateExists(name STRING) RETURNS BOOLEAN
  RETURN name == "library" OR name == "app" OR name == "webcomponent"
END FUNCTION

#+the available templates for help and error messages
FUNCTION templateList() RETURNS STRING
  RETURN "  library    publishable BDL package (reusable modules, published to the registry)\n"
      || "  app        application project that consumes packages (not published)\n"
      || "  webcomponent Genero webcomponent package (html/css/js bundle published as a COMPONENTTYPE)"
END FUNCTION

#+customizes the base manifest for the template (never adds dependencies)
FUNCTION applyTemplate(m manifest.TManifest INOUT, name STRING)
  CASE name
    WHEN "library"
      LET m.genero = "*"
      LET m.root = "."
      CALL m.programs.clear()
      LET m.docs[1] = "README.md"
    WHEN "app"
      --an app keeps the empty dependency buckets; no publish-only fields
    WHEN "webcomponent"
      LET m.webcomponents[1] = "MyWidget"
      LET m.docs[1] = "README.md"
  END CASE
END FUNCTION

#+writes the template's files under dir, substituting {{NAME}};
#+existing files are never overwritten (reported instead)
FUNCTION writeTemplateFiles(name STRING, dir STRING, pkgName STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE files DYNAMIC ARRAY OF TTemplateFile
  DEFINE i INT
  CALL templateFiles(name, files)
  FOR i = 1 TO files.getLength()
    VAR dest = os.Path.join(dir, files[i].path)
    IF os.Path.exists(dest) THEN
      DISPLAY SFMT("  %1 %2 already exists — left unchanged",
          fglpkgutils.C_BULLET, files[i].path)
      CONTINUE FOR
    END IF
    VAR parent = os.Path.dirName(dest)
    IF parent != "." THEN
      CALL fglpkgutils.mkdirp(parent)
    END IF
    TRY
      CALL fglpkgutils.writeStringToFile(dest,
          fglpkgutils.replace(files[i].content, "{{NAME}}", pkgName))
    CATCH
      RETURN FALSE, SFMT("cannot write %1", files[i].path)
    END TRY
    DISPLAY SFMT("  %1 %2", fglpkgutils.C_CHECK, files[i].path)
  END FOR
  RETURN TRUE, NULL
END FUNCTION

PRIVATE FUNCTION addFile(
    files DYNAMIC ARRAY OF TTemplateFile, path STRING, content STRING)
  LET files[files.getLength() + 1].path = path
  LET files[files.getLength()].content = content
END FUNCTION

PRIVATE FUNCTION templateFiles(
    name STRING, files DYNAMIC ARRAY OF TTemplateFile)
  CALL files.clear()
  CASE name
    WHEN "library"
      CALL addFile(files, "README.md", libraryReadme())
      CALL addFile(files, ".gitignore", gitignoreContent())
      CALL addFile(files, "Lib.4gl", librarySource())
    WHEN "app"
      CALL addFile(files, "README.md", appReadme())
      CALL addFile(files, ".gitignore", gitignoreContent())
      CALL addFile(files, "Main.4gl", appSource())
    WHEN "webcomponent"
      CALL addFile(files, "README.md", webcomponentReadme())
      CALL addFile(files, ".gitignore", gitignoreContent())
      CALL addFile(files, "webcomponents/MyWidget/MyWidget.html",
          webcomponentHTML())
      CALL addFile(files, "webcomponents/MyWidget/MyWidget.css",
          webcomponentCSS())
      CALL addFile(files, "webcomponents/MyWidget/MyWidget.js",
          webcomponentJS())
  END CASE
END FUNCTION

--─── template contents ──────────────────────────────────────────────────────

PRIVATE FUNCTION gitignoreContent() RETURNS STRING
  RETURN "# fglpkg local package install\n"
      || ".fglpkg/\n"
      || "\n"
      || "# Compiled Genero artifacts\n"
      || "*.42m\n"
      || "*.42f\n"
      || "*.42r\n"
END FUNCTION

PRIVATE FUNCTION libraryReadme() RETURNS STRING
  RETURN "# {{NAME}}\n"
      || "\n"
      || "A reusable Genero BDL package.\n"
      || "\n"
      || "## Install\n"
      || "\n"
      || "```bash\n"
      || "fglpkg install {{NAME}}\n"
      || "```\n"
      || "\n"
      || "## Usage\n"
      || "\n"
      || "Describe the functions or modules this package exposes.\n"
      || "\n"
      || "## Publishing\n"
      || "\n"
      || "```bash\n"
      || "fglpkg publish\n"
      || "```\n"
END FUNCTION

PRIVATE FUNCTION appReadme() RETURNS STRING
  RETURN "# {{NAME}}\n"
      || "\n"
      || "A Genero BDL application.\n"
      || "\n"
      || "## Setup\n"
      || "\n"
      || "```bash\n"
      || "fglpkg install            # install dependencies into .fglpkg/\n"
      || 'eval "$(fglpkg env)"      # add packages to FGLLDPATH / CLASSPATH\n'
      || "```\n"
      || "\n"
      || "## Build & run\n"
      || "\n"
      || "Compile and run your sources with the Genero toolchain once the environment\n"
      || "is set up.\n"
END FUNCTION

PRIVATE FUNCTION librarySource() RETURNS STRING
  RETURN "# {{NAME}} — library module\n"
      || "#\n"
      || "# Functions defined here are compiled to Lib.42m and published with the\n"
      || "# package. Callers IMPORT them after installing {{NAME}}.\n"
      || "\n"
      || "FUNCTION hello(name STRING) RETURNS STRING\n"
      || '    RETURN SFMT("Hello, %1!", name)\n'
      || "END FUNCTION\n"
END FUNCTION

PRIVATE FUNCTION appSource() RETURNS STRING
  RETURN "# {{NAME}} — application entry point\n"
      || "\n"
      || "MAIN\n"
      || '    DISPLAY "Hello from {{NAME}}"\n'
      || "END MAIN\n"
END FUNCTION

PRIVATE FUNCTION webcomponentReadme() RETURNS STRING
  RETURN "# {{NAME}}\n"
      || "\n"
      || "A Genero webcomponent package — html/css/js bundles published as `COMPONENTTYPE` names\n"
      || "and consumed by `WEBCOMPONENT` form fields.\n"
      || "\n"
      || "## Layout\n"
      || "\n"
      || "```\n"
      || "webcomponents/\n"
      || "  MyWidget/            # one directory per COMPONENTTYPE\n"
      || "    MyWidget.html      # required entry point\n"
      || "    MyWidget.css\n"
      || "    MyWidget.js\n"
      || "```\n"
      || "\n"
      || "Rename `MyWidget` to your COMPONENTTYPE and update the `webcomponents` array\n"
      || "in `fglpkg.json` to match. You may ship multiple components in one package by\n"
      || "adding more directories and listing each name in `webcomponents`.\n"
      || "\n"
      || "## Use it in a form\n"
      || "\n"
      || "```\n"
      || "WEBCOMPONENT wc = FORMONLY.mywidget,\n"
      || '   COMPONENTTYPE = "MyWidget";\n'
      || "```\n"
      || "\n"
      || "## Install + publish\n"
      || "\n"
      || "```bash\n"
      || "fglpkg install {{NAME}}     # consumer side\n"
      || "fglpkg publish              # publisher side\n"
      || "```\n"
END FUNCTION

PRIVATE FUNCTION webcomponentHTML() RETURNS STRING
  RETURN "<!DOCTYPE html>\n"
      || "<html>\n"
      || "<head>\n"
      || '    <meta charset="utf-8">\n'
      || "    <title>{{NAME}}</title>\n"
      || '    <link rel="stylesheet" href="MyWidget.css">\n'
      || '    <script src="MyWidget.js"></script>\n'
      || "</head>\n"
      || "<body>\n"
      || '    <div id="root">Hello from {{NAME}}</div>\n'
      || "</body>\n"
      || "</html>\n"
END FUNCTION

PRIVATE FUNCTION webcomponentCSS() RETURNS STRING
  RETURN "#root {\n"
      || "    font-family: system-ui, sans-serif;\n"
      || "    padding: 1rem;\n"
      || "}\n"
END FUNCTION

PRIVATE FUNCTION webcomponentJS() RETURNS STRING
  RETURN "// {{NAME}} — Genero webcomponent\n"
      || "//\n"
      || "// Uses the gICAPI protocol (window.parent.postMessage) to interact with the\n"
      || "// Genero front-end. The two messages every component should know about:\n"
      || '//   - "init"    sent by the front-end once on load\n'
      || '//   - "setData" sent whenever the bound field\'s value changes\n'
      || "//\n"
      || "// Replace the stub below with your component's behavior.\n"
      || "\n"
      || "(function () {\n"
      || "    function onMessage(event) {\n"
      || "        var msg = event.data;\n"
      || '        if (!msg || typeof msg !== "object") return;\n'
      || "        // Handle inbound messages from the Genero front-end here.\n"
      || '        // Example: if (msg.name === "setData") { ... }\n'
      || "    }\n"
      || '    window.addEventListener("message", onMessage);\n'
      || "    // Signal readiness to the front-end.\n"
      || "    if (window.parent && window.parent !== window) {\n"
      || '        window.parent.postMessage({ name: "ready" }, "*");\n'
      || "    }\n"
      || "})();\n"
END FUNCTION
