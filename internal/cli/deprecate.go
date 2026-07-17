package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

const deprecateUsage = `USAGE:
  fglpkg deprecate <pkg>[@<version>] [<message>] [--moved-to <newpkg>[@<version>]]
  fglpkg deprecate <pkg>[@<version>] --message <text> [--moved-to <newpkg>]
  fglpkg deprecate <pkg>[@<version>] --undo`

// deprecateArgs is the parsed, validated form of a `deprecate` invocation.
// It is produced by parseDeprecateArgs with no network access, so the
// validation matrix is unit-testable in isolation.
type deprecateArgs struct {
	slug    string // package slug (bare pkg → whole-package deprecation)
	version string // "" = whole-package; else the exact version to deprecate
	message string // final message (may be auto-filled from movedTo)
	movedTo string // raw --moved-to value ("slug" or "slug@version"); "" = none
	undo    bool
	jsonOut bool
}

// parseDeprecateArgs validates and parses `deprecate` arguments. It performs
// every check locally (no network call) so an invalid invocation fails fast
// with an actionable message and never touches the registry.
func parseDeprecateArgs(args []string) (deprecateArgs, error) {
	var (
		da          deprecateArgs
		positionals []string
		msgFlag     string
		haveMsgFlag bool
	)

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--undo":
			da.undo = true
		case a == "--json":
			da.jsonOut = true
		case a == "--message":
			if i+1 >= len(args) {
				return deprecateArgs{}, fmt.Errorf("%s requires a value", a)
			}
			i++
			msgFlag = args[i]
			haveMsgFlag = true
		case strings.HasPrefix(a, "--message="):
			msgFlag = strings.TrimPrefix(a, "--message=")
			haveMsgFlag = true
		case a == "--moved-to":
			if i+1 >= len(args) {
				return deprecateArgs{}, fmt.Errorf("%s requires a value", a)
			}
			i++
			da.movedTo = args[i]
		case strings.HasPrefix(a, "--moved-to="):
			da.movedTo = strings.TrimPrefix(a, "--moved-to=")
		case strings.HasPrefix(a, "--"):
			return deprecateArgs{}, fmt.Errorf("unknown flag %q\n%s", a, deprecateUsage)
		default:
			positionals = append(positionals, a)
		}
	}

	// Rule 1: exactly one <pkg>[@version] positional (plus an optional message).
	if len(positionals) == 0 {
		return deprecateArgs{}, fmt.Errorf("a package is required\n%s", deprecateUsage)
	}
	if len(positionals) > 2 {
		return deprecateArgs{}, fmt.Errorf("too many arguments\n%s", deprecateUsage)
	}
	posMsg := ""
	if len(positionals) == 2 {
		posMsg = positionals[1]
	}

	// Rule 6: split the pkg spec on the first '@'; reject a leading '@'
	// (would-be scoped name — not supported yet).
	pkgSpec := positionals[0]
	if strings.HasPrefix(pkgSpec, "@") {
		return deprecateArgs{}, fmt.Errorf("scoped names are not supported: %q", pkgSpec)
	}
	if idx := strings.Index(pkgSpec, "@"); idx >= 0 {
		da.slug = pkgSpec[:idx]
		da.version = pkgSpec[idx+1:]
		if da.version == "" {
			return deprecateArgs{}, fmt.Errorf("missing version after '@' in %q", pkgSpec)
		}
	} else {
		da.slug = pkgSpec
	}

	// Rule 3: --undo forbids a message and --moved-to.
	if da.undo {
		if posMsg != "" || haveMsgFlag || da.movedTo != "" {
			return deprecateArgs{}, fmt.Errorf("--undo does not take a message or --moved-to")
		}
		return da, nil
	}

	// Rule 4: a positional message and --message are mutually exclusive.
	if posMsg != "" && haveMsgFlag {
		return deprecateArgs{}, fmt.Errorf("pass either a positional message or --message, not both")
	}
	switch {
	case posMsg != "":
		da.message = posMsg
	case haveMsgFlag:
		da.message = msgFlag
	}

	// Rule 5: --moved-to must be a well-formed slug (strip any @version first).
	if da.movedTo != "" {
		targetSlug := da.movedTo
		if idx := strings.Index(targetSlug, "@"); idx >= 0 {
			targetSlug = targetSlug[:idx]
		}
		if !slugutil.IsValid(targetSlug) {
			return deprecateArgs{}, fmt.Errorf("--moved-to: '%s' is not a valid package name", targetSlug)
		}
	}

	// Rule 2: a message is required — supplied directly, or auto-filled from
	// --moved-to. None of the three ⇒ error.
	if da.message == "" && da.movedTo == "" {
		return deprecateArgs{}, fmt.Errorf("a deprecation message is required (pass a message, or --moved-to <pkg>)")
	}
	if da.message == "" {
		da.message = fmt.Sprintf("%s has moved to %s", da.slug, da.movedTo)
	}

	return da, nil
}

// cmdDeprecate marks a published version (or whole package) as deprecated —
// npm-style: it stays fully installable and listed, consumers just see a
// warning. --moved-to records a successor (the rename/relocation case) and
// --undo lifts a deprecation. Owner-only; reuses the publisher auth path.
func cmdDeprecate(args []string) error {
	da, err := parseDeprecateArgs(args)
	if err != nil {
		return err
	}

	if da.version != "" {
		err = registry.PublishDeprecateVersion(da.slug, da.version, da.message, da.movedTo, da.undo)
	} else {
		err = registry.PublishDeprecatePackage(da.slug, da.message, da.movedTo, da.undo)
	}
	if err != nil {
		return mapDeprecateError(err, da)
	}

	printDeprecateResult(da)
	return nil
}

// mapDeprecateError turns the registry client's typed status errors into the
// actionable, spec-mandated messages.
func mapDeprecateError(err error, da deprecateArgs) error {
	switch {
	case errors.Is(err, registry.ErrUnauthorized):
		return fmt.Errorf("you must be logged in to deprecate a package — run 'fglpkg login'")
	case errors.Is(err, registry.ErrForbidden):
		return fmt.Errorf("only the owning partner can deprecate %s", da.slug)
	case errors.Is(err, registry.ErrNotFound):
		if da.version != "" {
			return fmt.Errorf("%s has no published version %s", da.slug, da.version)
		}
		return fmt.Errorf("no such package '%s'", da.slug)
	case errors.Is(err, registry.ErrMessageTooLong):
		return fmt.Errorf("deprecation message exceeds the 512-byte limit")
	default:
		return err
	}
}

// printDeprecateResult writes the human (or --json) confirmation.
func printDeprecateResult(da deprecateArgs) {
	target := da.slug
	if da.version != "" {
		target = da.slug + "@" + da.version
	}

	if da.jsonOut {
		out := map[string]any{
			"slug":       da.slug,
			"deprecated": !da.undo,
		}
		if da.version != "" {
			out["version"] = da.version
		}
		if !da.undo && da.movedTo != "" {
			out["movedTo"] = da.movedTo
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}

	if da.undo {
		fmt.Printf("✓ Cleared deprecation on %s\n", target)
		return
	}

	fmt.Printf("✓ Deprecated %s\n", target)
	fmt.Printf("  message:  %s\n", da.message)
	if da.movedTo != "" {
		fmt.Printf("  moved to: %s\n", da.movedTo)
	}
	fmt.Println("  Consumers can still install it; they'll see a deprecation warning.")
}
