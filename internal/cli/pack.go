package cli

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// cmdPack produces the publishable zip without uploading it anywhere, so
// publishers can inspect exactly what `fglpkg publish` would send.
//
//	fglpkg pack                      → writes <name>-<version>-genero<major>.zip
//	fglpkg pack -o custom.zip        → writes to custom.zip
//	fglpkg pack --list               → print contents + metadata, no file written
func cmdPack(args []string) error {
	var (
		listOnly bool
		outPath  string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--list", "-l":
			listOnly = true
		case "-o", "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("flag %s requires a filename", a)
			}
			outPath = args[i+1]
			i++
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}

	m, err := manifest.Load(".")
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in current directory — run 'fglpkg init' first", manifest.Filename)
		}
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("manifest is invalid: %w", err)
	}
	built, err := enforceLint(m, ".")
	if err != nil {
		return err
	}

	// Pure-WC packages are genero-version-agnostic, so skip the
	// (potentially expensive) runtime detection in that case. Any BDL
	// content (including a webcomponent that's paired with a BDL wrapper)
	// still needs the detected major for the variant tag.
	var generoMajor string
	if m.HasBDLContent() || !m.HasWebcomponents() {
		gv, err := genero.Detect()
		if err != nil {
			return fmt.Errorf("cannot detect Genero version: %w", err)
		}
		generoMajor = gv.MajorString()
	}
	variant := artifactVariant(m, generoMajor)

	// Reuse the package built during enforceLint above rather than staging +
	// zipping a second time.
	zipData, checksum, entries := built.zip, built.checksum, built.entries

	fmt.Printf("Package:  %s@%s (%s)\n", m.Name, m.Version, variantDescription(variant))
	fmt.Printf("Size:     %d bytes\n", len(zipData))
	fmt.Printf("SHA256:   %s\n", checksum)
	fmt.Printf("Files:    %d\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  %8d  %s\n", e.size, e.name)
	}

	if listOnly {
		return nil
	}

	if outPath == "" {
		outPath = artifactFilename(m.Name, m.Version, variant)
	}
	if parent := filepath.Dir(outPath); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("cannot create output directory %s: %w", parent, err)
		}
	}
	if err := os.WriteFile(outPath, zipData, 0644); err != nil {
		return fmt.Errorf("cannot write %s: %w", outPath, err)
	}
	abs, _ := filepath.Abs(outPath)
	fmt.Printf("\nWrote %s\n", abs)
	return nil
}

type zipEntry struct {
	name string
	size int64
}

// listZipEntries reads zip bytes and returns entries sorted by name, so
// `fglpkg pack` output is stable across runs.
func listZipEntries(data []byte) ([]zipEntry, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	entries := make([]zipEntry, 0, len(r.File))
	for _, f := range r.File {
		entries = append(entries, zipEntry{name: f.Name, size: int64(f.UncompressedSize64)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}
