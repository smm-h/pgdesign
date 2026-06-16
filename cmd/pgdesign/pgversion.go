package main

// resolvePGVersion determines the PostgreSQL major version to use for
// version-sensitive operations (DDL generation, risk classification, etc.).
//
// Resolution order (first non-zero wins):
//   - live: actual PostgreSQL major version from a live database connection
//     (obtained via introspect).
//   - config: pg_version field from the [database] section of pgdesign.toml.
//   - toml: version field from the [meta] section of the schema TOML file.
//
// Returns 0 if all sources are zero, meaning no version information is
// available. Consumers should use conservative defaults: risk classification
// assumes the oldest supported PostgreSQL version, and generated DDL avoids
// version-specific features.
func resolvePGVersion(live, config, toml int) int {
	if live != 0 {
		return live
	}
	if config != 0 {
		return config
	}
	return toml
}
