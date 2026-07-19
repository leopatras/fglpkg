package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// cmdInfo fetches registry metadata for a package (or a specific version)
// and prints a human-readable summary. Use --json to emit the raw
// PackageInfo JSON instead, for piping into jq / scripts.
//
//	fglpkg info <name>                → latest version, pretty-printed
//	fglpkg info <name>@<version>      → specific version, pretty-printed
//	fglpkg info <name> --json         → raw PackageInfo JSON
func cmdInfo(args []string) error {
	var (
		jsonOut bool
		target  string
	)
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag %q", a)
			}
			if target != "" {
				return fmt.Errorf("too many arguments: %q", a)
			}
			target = a
		}
	}
	if target == "" {
		return fmt.Errorf("usage: fglpkg info <package>[@<version>] [--json]")
	}

	name, version, err := parsePackageArg(target)
	if err != nil {
		return err
	}

	// Route through the multi-provider set when secondary repositories are
	// configured, so an Artifactory-sourced package is queried against its own
	// repository instead of GI (which would 404). Falls back to the GI-only
	// client otherwise (byte-identical legacy behaviour).
	home, _ := fglpkgHome()
	var m *manifest.Manifest
	if mm, mErr := manifest.Load("."); mErr == nil {
		m = mm
	}
	rs, _, _, rsErr := buildRepositorySet(home, m)
	if rsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring registries config: %v\n", rsErr)
	}

	versions, err := infoVersionList(rs, name)
	if err != nil {
		return privateHint(err, name)
	}
	if len(versions.Versions) == 0 {
		return fmt.Errorf("package %q has no published versions", name)
	}

	// Resolve "latest" or any constraint-ish input to a concrete version.
	resolvedVersion := version
	if version == "" || version == "latest" || version == "*" {
		resolvedVersion = latestVersion(versions.Versions)
	}

	info, err := infoFetch(rs, name, resolvedVersion)
	if err != nil {
		return err
	}
	// Some registry payloads omit the name (it's in the URL). Fall back
	// to what the user asked for so the header and install hint render.
	if info.Name == "" {
		info.Name = name
	}
	if info.Version == "" {
		info.Version = resolvedVersion
	}

	if jsonOut {
		out, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	printInfo(info, versions, resolvedVersion == latestVersion(versions.Versions))
	return nil
}

// infoVersionList lists a package's versions, routing through the
// multi-provider set when one is configured (rs != nil), else the GI client.
func infoVersionList(rs *provider.RepositorySet, name string) (*registry.VersionList, error) {
	if rs == nil {
		return registry.FetchVersionList(name)
	}
	cvs, err := rs.Versions(name)
	if err != nil {
		return nil, err
	}
	vs := make([]string, 0, len(cvs))
	for _, cv := range cvs {
		vs = append(vs, cv.Version.String())
	}
	return &registry.VersionList{Versions: vs}, nil
}

// infoFetch returns full metadata for name@version, routing through the
// multi-provider set when one is configured (rs != nil), else the GI client.
func infoFetch(rs *provider.RepositorySet, name, version string) (*registry.PackageInfo, error) {
	if rs == nil {
		return registry.FetchInfo(name, version)
	}
	generoMajor := ""
	if gv, err := genero.Detect(); err == nil {
		generoMajor = gv.MajorString()
	}
	return rs.Info(name, version, generoMajor)
}

// latestVersion picks the newest entry from a version list using the
// project's semver ordering (so prereleases sort below their release).
// Falls back to lexical ordering for any strings that fail to parse.
func latestVersion(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	sorted := make([]string, len(vs))
	copy(sorted, vs)
	sort.Slice(sorted, func(i, j int) bool {
		av, aerr := semver.Parse(sorted[i])
		bv, berr := semver.Parse(sorted[j])
		switch {
		case aerr != nil && berr != nil:
			return sorted[i] < sorted[j]
		case aerr != nil:
			return true
		case berr != nil:
			return false
		}
		return av.LessThan(bv)
	})
	return sorted[len(sorted)-1]
}

func printInfo(info *registry.PackageInfo, versions *registry.VersionList, isLatest bool) {
	header := fmt.Sprintf("%s@%s", info.Name, info.Version)
	if isLatest {
		header += " (latest)"
	}
	fmt.Println(header)
	fmt.Println(strings.Repeat("─", len(header)))
	fmt.Println()

	// npm-style deprecation block — placed right under the header so it's the
	// first thing seen. Advisory only; the version is still installable.
	if info.Deprecated {
		val := "yes"
		if info.DeprecationMessage != "" {
			val = "yes — " + info.DeprecationMessage
		}
		printField("Deprecated", val)
		printField("Moved to", info.MovedTo)
		fmt.Println()
	}

	printField("Description", info.Description)
	printField("Author", info.Author)
	// Source is the owning repository ("gi", "acme-internal"); populated only
	// when multi-provider routing is active, so single-registry output is
	// unchanged.
	printField("Source", info.Source)
	printField("License", info.License)
	printField("Genero", info.GeneroConstraint)
	printField("Published", info.PublishedAt)
	if info.Checksum != "" {
		printField("Checksum", "sha256:"+info.Checksum)
	}
	printField("Download", info.DownloadURL)

	if len(info.Variants) > 0 {
		majors := make([]string, 0, len(info.Variants))
		for _, v := range info.Variants {
			majors = append(majors, v.GeneroMajor)
		}
		printField("Variants", strings.Join(majors, ", "))
	}

	if len(info.FGLDeps) > 0 {
		fmt.Println("\nFGL dependencies:")
		names := make([]string, 0, len(info.FGLDeps))
		for n := range info.FGLDeps {
			names = append(names, n)
		}
		sort.Strings(names)
		width := longestName(names)
		for _, n := range names {
			fmt.Printf("  %-*s  %s\n", width, n, info.FGLDeps[n])
		}
	}

	if len(info.JavaDeps) > 0 {
		fmt.Println("\nJava dependencies:")
		for _, d := range info.JavaDeps {
			fmt.Printf("  %s:%s:%s\n", d.GroupID, d.ArtifactID, d.Version)
		}
	}

	if len(versions.Versions) > 0 {
		fmt.Printf("\nVersions (%d): %s\n", len(versions.Versions), strings.Join(versions.Versions, ", "))
	}

	fmt.Printf("\nInstall: fglpkg install %s@%s\n", info.Name, info.Version)
}

func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(os.Stdout, "  %-12s %s\n", label+":", value)
}

func longestName(names []string) int {
	max := 0
	for _, n := range names {
		if len(n) > max {
			max = len(n)
		}
	}
	return max
}
