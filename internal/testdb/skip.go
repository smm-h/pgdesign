package testdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/dbutil"
)

// SkipIfNoPostgres skips the test unless a PostgreSQL server has been named.
//
// The connection string comes from PGDESIGN_DB and from nowhere else: there is
// no default target. A test binary that wants a database of its own boots one
// in TestMain through [RunWithCluster], which exports the ephemeral cluster's
// DSN under the same variable. With neither, the test skips -- it does not go
// looking for a server on localhost, so this suite can never connect to, create
// databases in, or drop databases from whatever PostgreSQL a developer happens
// to be running.
//
// When the PGDESIGN_REQUIRE_DB=1 environment variable is set, the test fails
// instead of skipping. This converts a silent skip into a hard failure, which
// is what CI lanes that provision PostgreSQL declare.
func SkipIfNoPostgres(t testing.TB) {
	t.Helper()

	dbURL, ok := DatabaseURL()
	if !ok {
		if RequireDB() {
			t.Fatalf("PostgreSQL required (%s=1) but %s", requireDBEnv, noDatabaseMessage)
			return
		}
		t.Skipf("%s", noDatabaseMessage)
		return
	}

	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		if RequireDB() {
			t.Fatalf("PostgreSQL required (%s=1) but not available: %v", requireDBEnv, err)
			return
		}
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		if RequireDB() {
			t.Fatalf("PostgreSQL required (%s=1) but not available: %v", requireDBEnv, err)
			return
		}
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}
	conn.Close(ctx)
}

// PartmanInfo holds metadata about a detected pg_partman installation.
type PartmanInfo struct {
	Version string // installed or available version (e.g. "5.2.4")
}

// SkipIfNoPartman skips the test if pg_partman is not available in the
// PostgreSQL server. It probes pg_available_extensions for the pg_partman
// extension and records the detected version. This is separate from
// SkipIfNoPostgres: a CI lane can have Postgres without partman.
//
// The DSN is resolved exactly as [SkipIfNoPostgres] resolves it: PGDESIGN_DB or
// nothing. An ephemeral cluster inherits the host's extension library, so
// pg_partman is available to it precisely when the machine has the package
// installed -- which is what the CI lane provisions on the runner host.
//
// When the PGDESIGN_REQUIRE_PARTMAN=1 environment variable is set, the
// test fails instead of skipping.
//
// SkipIfNoPartman does NOT call SkipIfNoPostgres internally -- callers
// should call both guards if they need both checks.
func SkipIfNoPartman(t testing.TB) *PartmanInfo {
	t.Helper()

	requirePartman := RequirePartman()

	dbURL, ok := DatabaseURL()
	if !ok {
		if requirePartman {
			t.Fatalf("pg_partman required (%s=1) but %s", requirePartmanEnv, noDatabaseMessage)
			return nil
		}
		t.Skipf("pg_partman not available: %s", noDatabaseMessage)
		return nil
	}

	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		if requirePartman {
			t.Fatalf("pg_partman required (%s=1) but PostgreSQL not available: %v", requirePartmanEnv, err)
			return nil
		}
		t.Skipf("pg_partman not available (no PostgreSQL): %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		if requirePartman {
			t.Fatalf("pg_partman required (%s=1) but PostgreSQL not available: %v", requirePartmanEnv, err)
			return nil
		}
		t.Skipf("pg_partman not available (no PostgreSQL): %v", err)
		return nil
	}
	defer conn.Close(ctx)

	// Probe pg_available_extensions for pg_partman. This catalog lists
	// extensions that are installable (present in the extension directory)
	// regardless of whether CREATE EXTENSION has been run.
	var version *string
	err = conn.QueryRow(ctx,
		"SELECT default_version FROM pg_available_extensions WHERE name = 'pg_partman'",
	).Scan(&version)
	if err != nil || version == nil {
		msg := "pg_partman extension not found in pg_available_extensions"
		if err != nil {
			msg = fmt.Sprintf("pg_partman not available: %v", err)
		}
		if requirePartman {
			t.Fatalf("pg_partman required (%s=1) but %s", requirePartmanEnv, msg)
			return nil
		}
		t.Skipf("%s", msg)
		return nil
	}

	info := &PartmanInfo{Version: *version}
	t.Logf("pg_partman available: version %s", info.Version)
	return info
}
