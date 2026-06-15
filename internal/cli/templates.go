package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// projectTemplate scaffolds a starter project layout for `fglpkg init
// --template`. Templates set up a pre-filled fglpkg.json plus a small
// directory structure; they intentionally declare NO external dependencies,
// so `fglpkg install` immediately after `init` never references a package
// that may not exist. Author/branding are left to the init prompts rather
// than baked in.
type projectTemplate struct {
	name    string
	summary string
	// apply customizes the base manifest (from manifest.New) for this
	// template — setting fields like genero/root/programs that make sense
	// for the project kind. It must not add dependencies.
	apply func(m *manifest.Manifest)
	// files are written alongside fglpkg.json. The token {{NAME}} in any
	// content is replaced with the package name. Existing files are never
	// overwritten.
	files []templateFile
}

type templateFile struct {
	path    string
	content string
}

// templates is the closed set of project templates, in display order.
var templates = []projectTemplate{
	{
		name:    "library",
		summary: "publishable BDL package (reusable modules, published to the registry)",
		apply: func(m *manifest.Manifest) {
			m.GeneroConstraint = "*"
			m.Root = "."
			m.Programs = []string{}
			m.Docs = []string{"README.md"}
		},
		files: []templateFile{
			{path: "README.md", content: libraryReadme},
			{path: ".gitignore", content: gitignoreContent},
			{path: "Lib.4gl", content: librarySource},
		},
	},
	{
		name:    "app",
		summary: "application project that consumes packages (not published)",
		apply: func(m *manifest.Manifest) {
			// An app keeps the empty dependency buckets from manifest.New;
			// no publish-only fields (root/programs/docs) are set.
		},
		files: []templateFile{
			{path: "README.md", content: appReadme},
			{path: ".gitignore", content: gitignoreContent},
			{path: "Main.4gl", content: appSource},
		},
	},
}

// findTemplate returns the template with the given name, or nil.
func findTemplate(name string) *projectTemplate {
	for i := range templates {
		if templates[i].name == name {
			return &templates[i]
		}
	}
	return nil
}

// templateList renders the available templates for help and error messages.
func templateList() string {
	var b strings.Builder
	for _, t := range templates {
		fmt.Fprintf(&b, "  %-10s %s\n", t.name, t.summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeFiles materializes the template's extra files under dir, substituting
// the package name. Existing files are skipped (never overwritten) and
// reported so the user knows what was left untouched.
func (t *projectTemplate) writeFiles(dir, name string) error {
	for _, f := range t.files {
		dest := filepath.Join(dir, f.path)
		if _, err := os.Stat(dest); err == nil {
			fmt.Printf("  • %s already exists — left unchanged\n", f.path)
			continue
		}
		if parent := filepath.Dir(dest); parent != "." {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return fmt.Errorf("cannot create %s: %w", parent, err)
			}
		}
		content := strings.ReplaceAll(f.content, "{{NAME}}", name)
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return fmt.Errorf("cannot write %s: %w", f.path, err)
		}
		fmt.Printf("  ✓ %s\n", f.path)
	}
	return nil
}

const gitignoreContent = `# fglpkg local package install
.fglpkg/

# Compiled Genero artifacts
*.42m
*.42f
*.42r
`

const libraryReadme = `# {{NAME}}

A reusable Genero BDL package.

## Install

` + "```" + `bash
fglpkg install {{NAME}}
` + "```" + `

## Usage

Describe the functions or modules this package exposes.

## Publishing

` + "```" + `bash
fglpkg publish
` + "```" + `
`

const appReadme = `# {{NAME}}

A Genero BDL application.

## Setup

` + "```" + `bash
fglpkg install            # install dependencies into .fglpkg/
eval "$(fglpkg env)"      # add packages to FGLLDPATH / CLASSPATH
` + "```" + `

## Build & run

Compile and run your sources with the Genero toolchain once the environment
is set up.
`

const librarySource = `# {{NAME}} — library module
#
# Functions defined here are compiled to Lib.42m and published with the
# package. Callers IMPORT them after installing {{NAME}}.

FUNCTION hello(name STRING) RETURNS STRING
    RETURN SFMT("Hello, %1!", name)
END FUNCTION
`

const appSource = `# {{NAME}} — application entry point

MAIN
    DISPLAY "Hello from {{NAME}}"
END MAIN
`
