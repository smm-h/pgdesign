package main

// resolvePGVersion returns the first non-zero value in priority order:
// live (from introspect) > config (from pgdesign.toml [database]) > toml (from schema TOML).
// Returns 0 if all sources are zero.
func resolvePGVersion(live, config, toml int) int {
	if live != 0 {
		return live
	}
	if config != 0 {
		return config
	}
	return toml
}
