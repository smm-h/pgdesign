// Package serve provides an HTTP API server for pgdesign schema inspection,
// validation, and database statistics.
package serve

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server is the HTTP API server for pgdesign.
type Server struct {
	pool          *pgxpool.Pool
	schemas       []string
	migrationsDir string
	mux           *http.ServeMux
}

// New creates a new Server with a pgxpool connection and sets up routes.
func New(connStr string, schemas []string, migrationsDir string) (*Server, error) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
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

// NewFromPool creates a Server from an existing pgxpool.Pool (useful for tests).
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

// routes registers all API endpoints on the mux.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/schema", s.handleSchema)
	s.mux.HandleFunc("GET /api/schema/d2", s.handleSchemaD2)
	s.mux.HandleFunc("GET /api/schema/svg", s.handleSchemaSVG)
	s.mux.HandleFunc("GET /api/migrations", s.handleMigrations)
	s.mux.HandleFunc("GET /api/migrations/{version}", s.handleMigrationVersion)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/stats/{table}", s.handleTableStats)
	s.mux.HandleFunc("GET /api/extensions", s.handleExtensions)
	s.mux.HandleFunc("POST /api/validate", s.handleValidate)
	s.mux.HandleFunc("POST /api/diff", s.handleDiff)
	s.mux.HandleFunc("GET /api/audit", s.handleAudit)
}

// ListenAndServe starts the HTTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.mux)
}

// ServeHTTP implements http.Handler so the server can be used with httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Close shuts down the connection pool.
func (s *Server) Close() {
	s.pool.Close()
}
