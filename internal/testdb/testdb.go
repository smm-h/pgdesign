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
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/dbutil"
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
	pgVersion      int // detected lazily, 0 = not yet detected
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

// EphemeralDB represents a created ephemeral test database.
type EphemeralDB struct {
	Name      string
	URL       string
	CreatedAt time.Time
	manager   *Manager
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
	panic("not implemented")
}

// Drop destroys an ephemeral test database and closes tracked connections.
func (m *Manager) Drop(ctx context.Context, db *EphemeralDB) error {
	panic("not implemented")
}

// Connect opens a tracked connection to the ephemeral database.
func (db *EphemeralDB) Connect(ctx context.Context) (*pgx.Conn, error) {
	panic("not implemented")
}

// Pool opens a tracked connection pool to the ephemeral database.
func (db *EphemeralDB) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	panic("not implemented")
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
