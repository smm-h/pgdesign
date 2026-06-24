package testdb

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/dbutil"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

const (
	// NamePrefix is inserted between the base name and the timestamp.
	NamePrefix = "_test_"
	// RandLen is the number of random characters in the suffix.
	RandLen = 8
	// RandCharset is the character set for the random suffix.
	RandCharset = "abcdefghijklmnopqrstuvwxyz0123456789"
	// MaxNameLen is PostgreSQL's maximum identifier length.
	MaxNameLen = 63
	// SuffixLen is the fixed-length suffix: _test_ (6) + timestamp (10) + _ (1) + random (8) = 25
	SuffixLen = 25
)

// nameRegex matches the full ephemeral database name format.
var nameRegex = regexp.MustCompile(`^(.+)_test_(\d{10,})_([a-z0-9]{8})$`)

// Manager manages ephemeral test database lifecycles.
type Manager struct {
	maintenanceURL string
	baseName       string
	pgVersion      int   // detected lazily, 0 = not yet detected
	pgVersionErr   error // non-nil if version detection failed
	pgVersionOnce  sync.Once
}

// NewManager creates a Manager from a base database URL.
// The base URL identifies the Postgres server and provides credentials.
// The database name from the URL becomes the base for ephemeral DB names.
func NewManager(baseURL string) (*Manager, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	baseName := strings.TrimPrefix(u.Path, "/")
	if baseName == "" {
		return nil, fmt.Errorf("base URL has no database name")
	}

	maintenanceURL, err := dbutil.MaintenanceURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("derive maintenance URL: %w", err)
	}

	return &Manager{
		maintenanceURL: maintenanceURL,
		baseName:       baseName,
	}, nil
}

// connectMaintenance opens a connection to the maintenance database.
func (m *Manager) connectMaintenance(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, m.maintenanceURL)
}

// EphemeralDB represents a created ephemeral test database.
type EphemeralDB struct {
	Name              string
	URL               string
	CreatedAt         time.Time
	ActiveConnections *int // nil = not queried, non-nil = count from pg_stat_activity
	manager           *Manager
	// mu protects conns and pools.
	mu    sync.Mutex
	conns []*pgx.Conn
	pools []*pgxpool.Pool
}

// CreateOptions configures how an ephemeral database is created.
type CreateOptions struct {
	DDL        io.Reader // SQL DDL to apply after creation. Mutually exclusive with TemplateDB.
	TemplateDB string    // Template database name. Unimplemented.
}

// Validate checks that CreateOptions is valid.
func (o CreateOptions) Validate() error {
	if o.DDL != nil && o.TemplateDB != "" {
		return fmt.Errorf("DDL and TemplateDB are mutually exclusive")
	}
	if o.TemplateDB != "" {
		return fmt.Errorf("template databases not yet supported")
	}
	return nil
}

// GenerateName creates an ephemeral database name from a base name.
func GenerateName(baseName string) string {
	truncated := truncateRuneSafe(baseName, MaxNameLen-SuffixLen)
	random := randomString(RandLen)
	return fmt.Sprintf("%s_test_%d_%s", truncated, time.Now().Unix(), random)
}

// ParseName extracts the base name, timestamp, and random suffix from an
// ephemeral DB name.
func ParseName(name string) (baseName string, created time.Time, random string, ok bool) {
	m := nameRegex.FindStringSubmatch(name)
	if m == nil {
		return "", time.Time{}, "", false
	}
	ts, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", time.Time{}, "", false
	}
	return m[1], time.Unix(ts, 0), m[3], true
}

// Create creates a new ephemeral test database.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*EphemeralDB, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	conn, err := pgx.Connect(ctx, m.maintenanceURL)
	if err != nil {
		return nil, fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer conn.Close(ctx)

	// Detect PG version on first call.
	m.pgVersionOnce.Do(func() {
		var versionStr string
		err := conn.QueryRow(ctx, "SHOW server_version_num").Scan(&versionStr)
		if err != nil {
			m.pgVersionErr = fmt.Errorf("detect PG version: %w", err)
			return
		}
		v, err := strconv.Atoi(versionStr)
		if err != nil {
			m.pgVersionErr = fmt.Errorf("parse PG version %q: %w", versionStr, err)
			return
		}
		m.pgVersion = v
	})
	if m.pgVersionErr != nil {
		return nil, m.pgVersionErr
	}

	name := GenerateName(m.baseName)
	sanitized := pgx.Identifier{name}.Sanitize()

	_, err = conn.Exec(ctx, "CREATE DATABASE "+sanitized)
	if err != nil {
		return nil, fmt.Errorf("create database %s: %w", name, err)
	}

	if opts.DDL != nil {
		if err := m.ApplyDDL(ctx, name, opts.DDL); err != nil {
			// Best-effort cleanup: drop the database we just created.
			dropConn, dropErr := pgx.Connect(ctx, m.maintenanceURL)
			if dropErr == nil {
				_, _ = dropConn.Exec(ctx, "DROP DATABASE IF EXISTS "+sanitized)
				dropConn.Close(ctx)
			}
			return nil, fmt.Errorf("apply DDL: %w", err)
		}
	}

	dbURL, err := dbutil.SwapDatabase(m.maintenanceURL, name)
	if err != nil {
		return nil, fmt.Errorf("derive ephemeral DB URL: %w", err)
	}

	return &EphemeralDB{
		Name:      name,
		URL:       dbURL,
		CreatedAt: time.Now(),
		manager:   m,
	}, nil
}

// Drop destroys an ephemeral test database and closes tracked connections.
func (m *Manager) Drop(ctx context.Context, db *EphemeralDB) error {
	// Guard: refuse to drop anything that doesn't match the ephemeral name pattern.
	// This check MUST be before any connection cleanup or database operations.
	if _, _, _, ok := ParseName(db.Name); !ok {
		return fmt.Errorf("refusing to drop database %q: name does not match ephemeral test database pattern. Only databases created by pgdesign testdb setup can be dropped", db.Name)
	}

	// Close all tracked connections and pools.
	db.mu.Lock()
	for i := len(db.pools) - 1; i >= 0; i-- {
		db.pools[i].Close()
	}
	db.pools = nil
	for i := len(db.conns) - 1; i >= 0; i-- {
		db.conns[i].Close(ctx)
	}
	db.conns = nil
	db.mu.Unlock()

	conn, err := pgx.Connect(ctx, m.maintenanceURL)
	if err != nil {
		return fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer conn.Close(ctx)

	sanitized := pgx.Identifier{db.Name}.Sanitize()

	if m.pgVersion >= 130000 {
		_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+sanitized+" WITH (FORCE)")
		if err != nil {
			return fmt.Errorf("drop database %s: %w", db.Name, err)
		}
		return nil
	}

	// Pre-PG13: terminate backends and retry.
	for attempt := 0; attempt < 3; attempt++ {
		_, _ = conn.Exec(ctx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid != pg_backend_pid()",
			db.Name,
		)
		_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+sanitized)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "is being accessed by other users") {
			return fmt.Errorf("drop database %s: %w", db.Name, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("drop database %s after 3 retries: %w", db.Name, err)
}

// DropByName drops an ephemeral database by name without requiring a full
// EphemeralDB struct. This is useful for CLI teardown and GC operations where
// the database was not created by this Manager instance.
func (m *Manager) DropByName(ctx context.Context, name string) error {
	return m.Drop(ctx, &EphemeralDB{Name: name})
}

// Connect opens a tracked connection to the ephemeral database.
func (db *EphemeralDB) Connect(ctx context.Context) (*pgx.Conn, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	conn, err := pgx.Connect(ctx, db.URL)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", db.Name, err)
	}
	db.conns = append(db.conns, conn)
	return conn, nil
}

// Pool opens a tracked connection pool to the ephemeral database.
func (db *EphemeralDB) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	pool, err := pgxpool.New(ctx, db.URL)
	if err != nil {
		return nil, fmt.Errorf("create pool for %s: %w", db.Name, err)
	}
	db.pools = append(db.pools, pool)
	return pool, nil
}

// ApplyDDL connects to the named database and executes DDL statements from the reader.
func (m *Manager) ApplyDDL(ctx context.Context, dbName string, ddl io.Reader) error {
	dbURL, err := dbutil.SwapDatabase(m.maintenanceURL, dbName)
	if err != nil {
		return fmt.Errorf("derive database URL: %w", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	data, err := io.ReadAll(ddl)
	if err != nil {
		return fmt.Errorf("read DDL: %w", err)
	}

	stmts, err := sqlparse.SplitStatements(string(data))
	if err != nil {
		return fmt.Errorf("split DDL statements: %w", err)
	}

	for i, stmt := range stmts {
		_, err := conn.Exec(ctx, stmt)
		if err != nil {
			preview := stmt
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			errMsg := fmt.Sprintf("statement %d failed: %s\n  SQL: %s", i+1, err, preview)
			upper := strings.ToUpper(strings.TrimSpace(stmt))
			if strings.HasPrefix(upper, "CREATE EXTENSION") {
				errMsg += "\n  Note: CREATE EXTENSION requires superuser privileges or the extension must be available"
			}
			return fmt.Errorf("%s", errMsg)
		}
	}
	return nil
}

// ListOrphans finds ephemeral databases that were created longer than olderThan ago.
func (m *Manager) ListOrphans(ctx context.Context, olderThan time.Duration) ([]*EphemeralDB, error) {
	conn, err := pgx.Connect(ctx, m.maintenanceURL)
	if err != nil {
		return nil, fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer conn.Close(ctx)

	pattern := m.baseName + NamePrefix + "%"
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1", pattern)
	if err != nil {
		return nil, fmt.Errorf("query pg_database: %w", err)
	}
	defer rows.Close()

	var orphans []*EphemeralDB
	for rows.Next() {
		var datname string
		if err := rows.Scan(&datname); err != nil {
			return nil, fmt.Errorf("scan datname: %w", err)
		}

		_, created, _, ok := ParseName(datname)
		if !ok {
			continue
		}

		if time.Since(created) <= olderThan {
			continue
		}

		var connCount int
		err := conn.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE datname = $1",
			datname,
		).Scan(&connCount)
		if err != nil {
			return nil, fmt.Errorf("count connections to %s: %w", datname, err)
		}

		dbURL, err := dbutil.SwapDatabase(m.maintenanceURL, datname)
		if err != nil {
			return nil, fmt.Errorf("derive URL for %s: %w", datname, err)
		}

		orphans = append(orphans, &EphemeralDB{
			Name:              datname,
			URL:               dbURL,
			CreatedAt:         created,
			ActiveConnections: &connCount,
			manager:           m,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pg_database: %w", err)
	}

	return orphans, nil
}

// SetupForTest creates an ephemeral database for a test and registers cleanup.
func (m *Manager) SetupForTest(t testing.TB, opts CreateOptions) *EphemeralDB {
	t.Helper()
	SkipIfNoPostgres(t)

	db, err := m.Create(context.Background(), opts)
	if err != nil {
		t.Fatalf("create ephemeral database: %v", err)
	}

	t.Cleanup(func() {
		db.mu.Lock()
		for i := len(db.pools) - 1; i >= 0; i-- {
			db.pools[i].Close()
		}
		db.pools = nil
		for i := len(db.conns) - 1; i >= 0; i-- {
			db.conns[i].Close(context.Background())
		}
		db.conns = nil
		db.mu.Unlock()

		if err := m.Drop(context.Background(), db); err != nil {
			t.Logf("cleanup: drop ephemeral database %s: %v", db.Name, err)
		}
	})

	return db
}

// truncateRuneSafe truncates s to at most maxBytes bytes without splitting
// multi-byte runes.
func truncateRuneSafe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backward from maxBytes to find a valid rune boundary.
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// randomString generates a cryptographically random string of length n
// from RandCharset.
func randomString(n int) string {
	charsetLen := big.NewInt(int64(len(RandCharset)))
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			// crypto/rand failure is not recoverable.
			panic(fmt.Sprintf("crypto/rand: %v", err))
		}
		b.WriteByte(RandCharset[idx.Int64()])
	}
	return b.String()
}
