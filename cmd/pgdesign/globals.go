package main

// Globals holds the app-wide global flags. Handlers read them via
// strictcli.Globals[Globals](ctx).
type Globals struct {
	Quiet  bool    `cli:"quiet" help:"Suppress non-error output" default:"false"`
	Config *string `cli:"config" help:"Path to pgdesign.toml (bypasses directory search)"`
}
