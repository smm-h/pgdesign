# Declare PGDESIGN_DB via InfraEnv primitive

Declare PGDESIGN_DB via the InfraEnv primitive as a check-context accessor. Currently read via raw os.Getenv in cmd/pgdesign/checks.go resolveDBURL (~:129).

## Scope

- cmd/pgdesign/checks.go resolveDBURL: replace raw os.Getenv with ctx.InfraValue accessor
- Test-harness env reads (internal/testdb/skip.go) deliberately stay raw (test infrastructure, not CLI surface)

## Prerequisites

Requires go-strictcli v0.22.0 (already in go.mod).

## Effort

Small.
