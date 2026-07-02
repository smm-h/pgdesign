package main

// Globals holds the app-wide global flags. Struct-based handlers read them via
// strictcli.Globals[Globals](ctx); kwargs-style handlers still receive them as
// kwargs["quiet"] / kwargs["config"] because strictcli.RegisterGlobals routes
// through the same GlobalFlag registration path.
type Globals struct {
	Quiet  bool    `cli:"quiet" help:"Suppress non-error output" default:"false"`
	Config *string `cli:"config" help:"Path to pgdesign.toml (bypasses directory search)"`
}
