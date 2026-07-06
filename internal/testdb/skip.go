package testdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/dbutil"
)

const defaultPostgresURL = "postgres://localhost:5432/postgres?sslmode=disable"

// SkipIfNoPostgres skips the test if no PostgreSQL server is available.
// It probes connectivity using the PGDESIGN_DB environment variable,
// or falls back to "postgres://localhost:5432/postgres?sslmode=disable".
//
// When the PGDESIGN_REQUIRE_DB=1 environment variable is set, the test
// fails instead of skipping. This converts a silent skip into a hard
// failure, useful in CI lanes that are known to have Postgres available.
func SkipIfNoPostgres(t testing.TB) {
	t.Helper()

	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = defaultPostgresURL
	}

	requireDB := os.Getenv("PGDESIGN_REQUIRE_DB") == "1"

	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		if requireDB {
			t.Fatalf("PostgreSQL required (PGDESIGN_REQUIRE_DB=1) but not available: %v", err)
		}
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		if requireDB {
			t.Fatalf("PostgreSQL required (PGDESIGN_REQUIRE_DB=1) but not available: %v", err)
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
// When the PGDESIGN_REQUIRE_PARTMAN=1 environment variable is set, the
// test fails instead of skipping.
//
// SkipIfNoPartman does NOT call SkipIfNoPostgres internally -- callers
// should call both guards if they need both checks.
func SkipIfNoPartman(t testing.TB) *PartmanInfo {
	t.Helper()

	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = defaultPostgresURL
	}

	requirePartman := os.Getenv("PGDESIGN_REQUIRE_PARTMAN") == "1"

	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		if requirePartman {
			t.Fatalf("pg_partman required (PGDESIGN_REQUIRE_PARTMAN=1) but PostgreSQL not available: %v", err)
		}
		t.Skipf("pg_partman not available (no PostgreSQL): %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		if requirePartman {
			t.Fatalf("pg_partman required (PGDESIGN_REQUIRE_PARTMAN=1) but PostgreSQL not available: %v", err)
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
			t.Fatalf("pg_partman required (PGDESIGN_REQUIRE_PARTMAN=1) but %s", msg)
		}
		t.Skipf("%s", msg)
		return nil
	}

	info := &PartmanInfo{Version: *version}
	t.Logf("pg_partman available: version %s", info.Version)
	return info
}
