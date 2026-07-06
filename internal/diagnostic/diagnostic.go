// Package diagnostic re-exports shared diagnostic types from pkg/diagnostic
// for use by internal pgdesign packages. PG-specific diagnostic codes are
// documented in codes.go.
package diagnostic

import (
	pkgdiag "github.com/smm-h/pgdesign/pkg/diagnostic"
)

// Type aliases re-export the public API so all existing internal/ imports
// continue to work without modification.
type Severity = pkgdiag.Severity

const (
	Error   = pkgdiag.Error
	Warning = pkgdiag.Warning
	Info    = pkgdiag.Info
	Hint    = pkgdiag.Hint
)

type Diagnostic = pkgdiag.Diagnostic
type Diagnostics = pkgdiag.Diagnostics

// RenderTerminal delegates to pkg/diagnostic.RenderTerminal.
func RenderTerminal(diags Diagnostics, color bool) string {
	return pkgdiag.RenderTerminal(diags, color)
}

// RenderJSON delegates to pkg/diagnostic.RenderJSON.
func RenderJSON(diags Diagnostics) string {
	return pkgdiag.RenderJSON(diags)
}
