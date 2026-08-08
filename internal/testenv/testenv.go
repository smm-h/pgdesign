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
	"os"
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

// Unset removes name from the environment for the duration of t, restoring
// whatever it held when t finishes.
//
// TB has no Unsetenv, so this goes through TB.Setenv first -- which is what
// registers the restore -- and then unsets the now-empty variable outright.
// A bare os.Unsetenv registers nothing: the variable stays gone for every test
// that runs after it in the same binary. That is not hypothetical. A test here
// unset PGDESIGN_DB that way, and once the suite began booting a cluster and
// exporting the DSN under it, every later database-backed test in the binary
// found no database.
func Unset(t testing.TB, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("testenv: unsetting %s: %v", name, err)
	}
}
