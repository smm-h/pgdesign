// Package testenv binds the suite-wide test-environment floor.
//
// It is a thin wrapper over stricttest's hygiene module, plus the toolchain
// caches this suite must keep across the HOME repoint. One wrapper rather than
// a literal hygiene.Isolate call per test means the preserve list is decided in
// exactly one place.
//
// The Python entry is not optional. internal/test's codegen compile gate shells
// out to `mypy --strict`, and mypy is installed into the user site
// (~/.local/lib/pythonX.Y/site-packages), which CPython resolves from HOME. A
// bare Isolate moves HOME and the interpreter then cannot import mypy at all --
// the gate fails with ModuleNotFoundError instead of type-checking anything.
// The npm entry is the same story for the TypeScript compile gate.
package testenv

import (
	"testing"

	"github.com/smm-h/stricttest/go/hygiene"
)

// Isolate binds the environment floor for the duration of t: a throwaway HOME
// and XDG base directories, an emptied git global/system config with a
// throwaway identity, transports locked to file://, and every ambient
// credential variable removed. The Go, Python and npm toolchain caches survive
// the repoint.
func Isolate(t testing.TB) {
	t.Helper()
	hygiene.Isolate(t, hygiene.Preserve(
		hygiene.GoPath, hygiene.GoModCache, hygiene.GoCache,
		hygiene.PythonUserBase, hygiene.NpmCache,
	))
}
