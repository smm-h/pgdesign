package main

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/model"
)

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

// requirePGVersion resolves the PostgreSQL version and returns an error if
// no version is available. Commands that generate DDL or perform
// version-dependent validation must use this instead of resolvePGVersion.
func requirePGVersion(live, config, toml int) (int, error) {
	v := resolvePGVersion(live, config, toml)
	if v == 0 {
		return 0, fmt.Errorf("pg_version is required: set [database].pg_version in pgdesign.toml or [meta].version in your schema")
	}
	return v, nil
}

// applyLivePGVersion is the single post-build seam for the live PG-version tier.
// The config and toml tiers are folded into schema.PGVersion at build time (see
// parseAndBuild); this override is used only where a live database connection
// yields an actual server version that must win over the declared version.
// A zero live version leaves schema.PGVersion untouched.
func applyLivePGVersion(schema *model.Schema, live int) {
	if live != 0 {
		schema.PGVersion = live
	}
}

// migrationDiff is the migrate plan/generate/test diff seam: it resolves the
// live PG version onto the desired schema (so an unpinned pg_version does not
// register as a spurious PGVersionChanged) and THEN diffs against the
// introspected actual. actual is always introspected (registry-absent) on these
// paths, so DiffLive is used — class-aware fields (semantic type names) do not
// false-drift. Extracted as a helper so the ordering (apply-then-diff) is
// unit-testable without a live database.
func migrationDiff(desired, actual *model.Schema) *diff.SchemaDiff {
	applyLivePGVersion(desired, actual.PGVersion)
	return diff.DiffLive(desired, actual, nil)
}

// requireSchemaPGVersion returns the schema's resolved PG version (already
// folded from the config and toml tiers at build time), or an error if no
// version is available from any tier.
func requireSchemaPGVersion(schema *model.Schema) (int, error) {
	if schema.PGVersion == 0 {
		return 0, fmt.Errorf("pg_version is required: set [database].pg_version in pgdesign.toml or [meta].version in your schema")
	}
	return schema.PGVersion, nil
}
