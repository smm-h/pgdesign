package testdb

import (
	"context"
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
func SkipIfNoPostgres(t testing.TB) {
	t.Helper()

	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = defaultPostgresURL
	}

	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}
	conn.Close(ctx)
}
