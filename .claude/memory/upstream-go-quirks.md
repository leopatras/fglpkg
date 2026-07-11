---
name: upstream-go-quirks
description: Known bugs/quirks in the Go implementation that the 4GL port replicates on purpose (byte-parity)
type: project
last-updated: 2026-07-11
---

# Upstream Go quirks replicated for parity

- **pluralY bug:** `pluralY()` in `internal/cli/outdated.go` returns
  `"ie"` for n != 1 (not `"ies"`). `cmdOutdated` appends the missing
  `"s"` itself, but `cmdAudit` uses it bare, so the binary prints
  "2 vulnerabilitie found in 1 package" / "2 vulnerabilitie found at
  severity >= medium". `g/fglpkg/audit.4gl` reproduces this deliberately
  (both sites marked with a comment). **When fixing the Go side, change
  the two `IIF(... "y", "ie")` sites in audit.4gl in the same commit.**

- `fglpkg run` does NOT propagate the child's exit code (any failure →
  exit 1), while `fglpkg bdl` propagates the exact code. Looks
  inconsistent but is faithful to the Go sources — don't "fix" one side
  alone.

- `--severity` for audit is accepted in the joined form only
  (`--severity=high`); the two-token form `--severity high` is an
  unknown-argument error in Go, hence also in the port.

**Why:** byte-parity with `bin/fglpkg-go` is the port's acceptance
criterion; deviations, even bug fixes, must be made on both sides
together (see `g/PORTING.md`).
