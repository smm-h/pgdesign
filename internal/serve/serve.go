// Package serve provides an HTTP API server and web UI for pgdesign schema inspection, validation, database statistics, and interactive exploration.
package serve

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// PoolConfig holds connection pool tuning parameters.
// Zero values mean pgxpool uses its built-in defaults.
type PoolConfig struct {
	MaxConns int32
	MinConns int32
}

// projectState is the in-memory project a DB-free server serves: the resolved
// model, the semtype registry that produced it (registry-present type information,
// so state-machine D2 diagrams render), and the build diagnostics wrapped into the
// /schema envelope. It is nil in database (introspect) mode.
type projectState struct {
	schema   *model.Schema
	registry *semtype.Registry
	diags    []diagnostic.Diagnostic
}

// Server is the HTTP API server for pgdesign. It runs in one of two explicit
// modes, chosen at construction (never a silent runtime fallback):
//
//   - database mode: pool is non-nil, project is nil. /schema introspects the
//     live database (registry-absent class); the stats/migrations/extensions/
//     diff/audit endpoints read the database.
//   - project mode: project is non-nil, pool is nil. /schema serves the compiled
//     project model (registry-present class) with no database; database-only
//     endpoints degrade with an explicit 503, never a nil-pool panic.
type Server struct {
	pool          *pgxpool.Pool
	project       *projectState
	schemas       []string
	migrationsDir string
	mux           *http.ServeMux
	// requestTimeout enforces a per-request deadline (0 disables). It wraps the mux
	// in an http.TimeoutHandler, which cancels the request context on expiry so
	// well-behaved handlers stop early and the client gets an explicit 503.
	requestTimeout time.Duration
	// auditJobs manages asynchronous audit jobs (job-start/poll, cancellable and
	// bounded) so a slow TANE run can never block a request goroutine indefinitely.
	auditJobs *auditJobManager
}

// SetRequestTimeout sets the per-request deadline enforced by the server. A
// non-positive duration disables enforcement. It must be called before serving.
func (s *Server) SetRequestTimeout(d time.Duration) { s.requestTimeout = d }

// withTimeout wraps h with the per-request timeout, if one is configured. It is
// the single place ServeHTTP and ListenAndServe apply the deadline, so both the
// httptest and network paths enforce it identically.
func (s *Server) withTimeout(h http.Handler) http.Handler {
	if s.requestTimeout <= 0 {
		return h
	}
	return http.TimeoutHandler(h, s.requestTimeout, `{"error":"request timeout exceeded"}`)
}

// New creates a new database-mode Server with a pgxpool connection and sets up
// routes. Pool parameters in poolCfg override pgxpool defaults when non-zero.
func New(connStr string, schemas []string, migrationsDir string, poolCfg PoolConfig) (*Server, error) {
	ctx := context.Background()

	pgxCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse connection config: %w", err)
	}
	if poolCfg.MaxConns > 0 {
		pgxCfg.MaxConns = poolCfg.MaxConns
	}
	if poolCfg.MinConns > 0 {
		pgxCfg.MinConns = poolCfg.MinConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	// Verify connectivity.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if len(schemas) == 0 {
		schemas = []string{"public"}
	}

	s := &Server{
		pool:          pool,
		schemas:       schemas,
		migrationsDir: migrationsDir,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// NewFromPool creates a database-mode Server from an existing pgxpool.Pool (useful for tests).
func NewFromPool(pool *pgxpool.Pool, schemas []string, migrationsDir string) *Server {
	if len(schemas) == 0 {
		schemas = []string{"public"}
	}
	s := &Server{
		pool:          pool,
		schemas:       schemas,
		migrationsDir: migrationsDir,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s
}

// NewProject creates a DB-free project-mode Server that serves a pre-compiled
// model. schema is the resolved model, registry is the semtype registry that
// produced it (enables state-machine D2 diagrams), and diags are the build
// diagnostics wrapped into the /schema envelope. There is no database: the
// stats/migrations/extensions/diff/audit endpoints return an explicit 503.
func NewProject(schema *model.Schema, registry *semtype.Registry, diags []diagnostic.Diagnostic, schemas []string, migrationsDir string) *Server {
	if len(schemas) == 0 {
		schemas = []string{"public"}
	}
	s := &Server{
		project: &projectState{
			schema:   schema,
			registry: registry,
			diags:    diags,
		},
		schemas:       schemas,
		migrationsDir: migrationsDir,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s
}

// projectMode reports whether the server serves a compiled project without a
// database.
func (s *Server) projectMode() bool { return s.project != nil }

// requireDB writes an explicit 503 and returns false when the server has no
// database (project mode). It is the single guard every database-only endpoint
// calls before touching s.pool, so a DB-free server degrades with a clear message
// instead of a nil-pool panic.
func (s *Server) requireDB(w http.ResponseWriter) bool {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable,
			"endpoint unavailable in project mode: this server was started without --db; database-backed endpoints require a database connection")
		return false
	}
	return true
}

// routes registers all API endpoints on the mux and initializes the audit job
// manager.
func (s *Server) routes() {
	s.auditJobs = newAuditJobManager()
	s.mux.HandleFunc("GET /api/schema", s.handleSchema)
	s.mux.HandleFunc("GET /api/schema/d2", s.handleSchemaD2)
	s.mux.HandleFunc("GET /api/schema/svg", s.handleSchemaSVG)
	s.mux.HandleFunc("GET /api/schema/graph", s.handleSchemaGraph)
	s.mux.HandleFunc("GET /api/schema/doc", s.handleSchemaDoc)
	s.mux.HandleFunc("GET /api/migrations", s.handleMigrations)
	s.mux.HandleFunc("GET /api/migrations/{version}", s.handleMigrationVersion)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/stats/{table}", s.handleTableStats)
	s.mux.HandleFunc("GET /api/extensions", s.handleExtensions)
	s.mux.HandleFunc("POST /api/validate", s.handleValidate)
	s.mux.HandleFunc("POST /api/diff", s.handleDiff)
	// Audit is asynchronous (job-start/poll): the synchronous GET /api/audit was a
	// self-DoS button (unbounded TANE per request). Start a job, then poll it.
	s.mux.HandleFunc("POST /api/audit/jobs", s.handleAuditStart)
	s.mux.HandleFunc("GET /api/audit/jobs/{id}", s.handleAuditPoll)
	s.mux.HandleFunc("DELETE /api/audit/jobs/{id}", s.handleAuditCancel)
}

// ListenAndServe starts the HTTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.withTimeout(s.mux))
}

// ServeHTTP implements http.Handler so the server can be used with httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.withTimeout(s.mux).ServeHTTP(w, r)
}

// Close shuts down the connection pool. It is a no-op in project mode.
func (s *Server) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}
