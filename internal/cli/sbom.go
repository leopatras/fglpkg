package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/sbom"
)

// sbomFlags holds the parsed flags specific to `fglpkg sbom`.
type sbomFlags struct {
	output     string
	format     string
	production bool
	pretty     bool
	help       bool
}

// cmdSbom emits a Software Bill of Materials (CycloneDX 1.5 JSON) for
// the current project's fglpkg.lock.
//
//	fglpkg sbom                                 Emit to stdout
//	fglpkg sbom -o sbom.json                    Write to file
//	fglpkg sbom --pretty                        Indented JSON
//	fglpkg sbom --production                    Skip dev-scoped JARs
//	fglpkg sbom --format=cyclonedx              Default (only supported in v1)
func cmdSbom(args []string) error {
	flags, err := parseSbomFlags(args)
	if err != nil {
		return err
	}
	if flags.help {
		printSbomUsage()
		return nil
	}
	if flags.format != "" && flags.format != "cyclonedx" {
		return fmt.Errorf("%s format not supported in v1 (use --format=cyclonedx)", flags.format)
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	if !lockfile.Exists(projectDir) {
		return fmt.Errorf("no %s in current directory; run `fglpkg install` first", lockfile.Filename)
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)
	}

	doc := sbom.Build(lf, sbom.Options{
		Production:  flags.production,
		ToolVersion: Version,
	})

	out, closeOut, err := openSbomOutput(flags.output)
	if err != nil {
		return err
	}
	defer closeOut()

	if err := writeSbom(out, doc, flags.pretty); err != nil {
		return err
	}
	return nil
}

func parseSbomFlags(args []string) (sbomFlags, error) {
	f := sbomFlags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			// Accepted silently — JSON is always the format, the flag
			// is a no-op for parity with other commands.
		case a == "--pretty":
			f.pretty = true
		case a == "--production", a == "--prod":
			f.production = true
		case a == "--help", a == "-h":
			f.help = true
		case a == "-o", a == "--output":
			if i+1 >= len(args) {
				return f, fmt.Errorf("%s requires a file path", a)
			}
			i++
			f.output = args[i]
		case strings.HasPrefix(a, "--output="):
			f.output = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "--format="):
			f.format = strings.TrimPrefix(a, "--format=")
		default:
			return f, fmt.Errorf("unknown argument %q", a)
		}
	}
	return f, nil
}

// openSbomOutput returns the destination writer, plus a closer the
// caller must defer. When the user didn't pass -o, output goes to
// os.Stdout (with a no-op closer).
func openSbomOutput(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

func writeSbom(w io.Writer, doc *sbom.Document, pretty bool) error {
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("failed to write SBOM: %w", err)
	}
	return nil
}

func printSbomUsage() {
	fmt.Print(`fglpkg sbom - Emit a Software Bill of Materials for the current project

USAGE:
  fglpkg sbom [flags]

FLAGS:
  -o, --output <path>             Write to file instead of stdout
  --pretty                        Indented JSON (default: compact)
  --production, --prod            Skip dev-scoped JARs
  --format=<cyclonedx|spdx>       Output format. Default: cyclonedx
                                  (spdx is reserved for a future release)
  --help, -h                      Show this help

NOTES:
  v1 emits CycloneDX 1.5 JSON, generated from fglpkg.lock. No network
  calls — output is deterministic given the lockfile.

  License / supplier metadata is not currently in the lockfile and is
  therefore omitted from per-component entries. A future --enrich flag
  will fetch these from the registry.
`)
}
