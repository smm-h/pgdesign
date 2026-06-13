package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/validate"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// connStr returns the connection string from the pool config.
func (s *Server) connStr() string {
	return s.pool.Config().ConnString()
}

// handleSchema introspects the DB and returns the full IR as JSON.
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	schema, diags, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	type schemaResponse struct {
		Schema      *model.Schema            `json:"schema"`
		Diagnostics []diagnostic.Diagnostic  `json:"diagnostics,omitempty"`
	}
	writeJSON(w, http.StatusOK, schemaResponse{Schema: schema, Diagnostics: diags})
}

// handleSchemaD2 introspects the DB and returns D2 text.
func (s *Server) handleSchemaD2(w http.ResponseWriter, r *http.Request) {
	schema, _, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	d2 := generate.GenerateD2(schema)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(d2))
}

// handleSchemaSVG introspects the DB, generates D2, and renders SVG.
func (s *Server) handleSchemaSVG(w http.ResponseWriter, r *http.Request) {
	schema, _, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	d2Source := generate.GenerateD2(schema)
	svg, err := generate.RenderSVG(d2Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("render SVG: %v", err))
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	w.Write(svg)
}

// handleMigrations queries the pgdesign_migrations table.
func (s *Server) handleMigrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type migration struct {
		Version     string    `json:"version"`
		AppliedAt   time.Time `json:"applied_at"`
		Description string    `json:"description"`
		Checksum    string    `json:"checksum"`
	}

	// Check if the migrations table exists.
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'pgdesign_migrations'
		)`).Scan(&exists)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("check migrations table: %v", err))
		return
	}
	if !exists {
		writeJSON(w, http.StatusOK, []migration{})
		return
	}

	rows, err := s.pool.Query(ctx,
		`SELECT version, applied_at, description, checksum
		FROM pgdesign_migrations
		ORDER BY applied_at`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query migrations: %v", err))
		return
	}
	defer rows.Close()

	var migrations []migration
	for rows.Next() {
		var m migration
		if err := rows.Scan(&m.Version, &m.AppliedAt, &m.Description, &m.Checksum); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan migration row: %v", err))
			return
		}
		migrations = append(migrations, m)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("iterate migrations: %v", err))
		return
	}

	if migrations == nil {
		migrations = []migration{}
	}
	writeJSON(w, http.StatusOK, migrations)
}

// handleStats returns database statistics for all tables in the configured schemas.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type tableStat struct {
		SchemaName string `json:"schema_name"`
		TableName  string `json:"table_name"`
		LiveTuples int64  `json:"live_tuples"`
		DeadTuples int64  `json:"dead_tuples"`
		SeqScan    int64  `json:"seq_scan"`
		TotalBytes int64  `json:"total_bytes"`
	}

	rows, err := s.pool.Query(ctx,
		`SELECT schemaname, relname, n_live_tup, n_dead_tup, seq_scan,
			pg_total_relation_size(schemaname||'.'||relname) as total_bytes
		FROM pg_stat_user_tables
		WHERE schemaname = ANY($1)
		ORDER BY pg_total_relation_size(schemaname||'.'||relname) DESC`,
		s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query stats: %v", err))
		return
	}
	defer rows.Close()

	var stats []tableStat
	for rows.Next() {
		var st tableStat
		if err := rows.Scan(&st.SchemaName, &st.TableName, &st.LiveTuples, &st.DeadTuples, &st.SeqScan, &st.TotalBytes); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan stat row: %v", err))
			return
		}
		stats = append(stats, st)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("iterate stats: %v", err))
		return
	}

	if stats == nil {
		stats = []tableStat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": stats})
}

// handleTableStats returns per-table stats including column info and index usage.
func (s *Server) handleTableStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	table := r.PathValue("table")

	type indexStat struct {
		IndexName string `json:"index_name"`
		IdxScan   int64  `json:"idx_scan"`
		SizeBytes int64  `json:"size_bytes"`
	}

	rows, err := s.pool.Query(ctx,
		`SELECT indexrelname, idx_scan, pg_relation_size(indexrelid) as size_bytes
		FROM pg_stat_user_indexes
		WHERE schemaname||'.'||relname = $1`,
		table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query table stats: %v", err))
		return
	}
	defer rows.Close()

	var indexes []indexStat
	for rows.Next() {
		var idx indexStat
		if err := rows.Scan(&idx.IndexName, &idx.IdxScan, &idx.SizeBytes); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan index row: %v", err))
			return
		}
		indexes = append(indexes, idx)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("iterate indexes: %v", err))
		return
	}

	if indexes == nil {
		indexes = []indexStat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"table":   table,
		"indexes": indexes,
	})
}

// handleExtensions returns installed PostgreSQL extensions.
func (s *Server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type extension struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}

	rows, err := s.pool.Query(ctx,
		`SELECT extname, extversion FROM pg_extension WHERE extname != 'plpgsql'`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query extensions: %v", err))
		return
	}
	defer rows.Close()

	var extensions []extension
	for rows.Next() {
		var ext extension
		if err := rows.Scan(&ext.Name, &ext.Version); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan extension row: %v", err))
			return
		}
		extensions = append(extensions, ext)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("iterate extensions: %v", err))
		return
	}

	if extensions == nil {
		extensions = []extension{}
	}
	writeJSON(w, http.StatusOK, extensions)
}

// handleValidate accepts a TOML body, parses+builds+validates, and returns diagnostics.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read body: %v", err))
		return
	}

	schema, diags := parseAndBuild(body)
	if schema == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":       false,
			"diagnostics": diagsToJSON(diags),
		})
		return
	}

	config := &validate.Config{
		NamingPattern: "snake_case",
		MaxColumns:    30,
		Extensions:    schema.Extensions,
		ExtRegistry:   extregistry.NewBuiltinRegistry(),
	}

	valDiags, _ := validate.Validate(schema, config)
	allDiags := append(diags, valDiags...)

	writeJSON(w, http.StatusOK, map[string]any{
		"valid":       !diagnostic.Diagnostics(allDiags).HasErrors(),
		"diagnostics": diagsToJSON(allDiags),
	})
}

// handleDiff accepts a TOML body, parses+builds, introspects live DB, diffs,
// and returns the SchemaDiff as JSON.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read body: %v", err))
		return
	}

	desired, diags := parseAndBuild(body)
	if desired == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"error":       "parse/build failed",
			"diagnostics": diagsToJSON(diags),
		})
		return
	}

	actual, _, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	d := diff.Diff(desired, actual)
	writeJSON(w, http.StatusOK, d)
}

// handleAudit introspects the live DB, runs discover (TANE) + audit, returns diagnostics.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	schema, _, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	var allDiags []diagnostic.Diagnostic

	// Discover FDs from live data for tables without declared FDs.
	ctx := r.Context()
	conn, err := s.pool.Acquire(ctx)
	if err == nil {
		pgxConn := conn.Conn()
		opts := discover.Options{}
		for i := range schema.Tables {
			tbl := &schema.Tables[i]
			if len(tbl.Dependencies) > 0 {
				continue
			}
			schemaName := tbl.Schema
			if schemaName == "" {
				schemaName = "public"
			}
			fds, discDiags, discErr := discover.Discover(pgxConn, schemaName, tbl.Name, opts)
			allDiags = append(allDiags, discDiags...)
			if discErr != nil {
				allDiags = append(allDiags, diagnostic.Diagnostic{
					Severity: diagnostic.Warning,
					Table:    tbl.Name,
					Message:  fmt.Sprintf("FD discovery failed: %v", discErr),
				})
				continue
			}
			if len(fds) > 0 {
				tbl.Dependencies = fds
				allDiags = append(allDiags, diagnostic.Diagnostic{
					Severity: diagnostic.Info,
					Table:    tbl.Name,
					Message:  fmt.Sprintf("Discovered %d FD(s) from data sample.", len(fds)),
				})
			}
		}
		conn.Release()
	}

	allDiags = append(allDiags, audit.Audit(schema)...)
	writeJSON(w, http.StatusOK, map[string]any{
		"diagnostics": diagsToJSON(allDiags),
	})
}

// parseAndBuild parses TOML bytes and builds a resolved schema.
func parseAndBuild(data []byte) (*model.Schema, []diagnostic.Diagnostic) {
	raw, parseDiags := parse.Bytes(data)
	if raw == nil {
		return nil, parseDiags
	}

	reg := semtype.NewBuiltinRegistry()

	var userTypes []semtype.UserTypeDef
	for _, rt := range raw.Types {
		ut := semtype.UserTypeDef{
			Name:   rt.Name,
			Kind:   rt.Kind,
			Base:   rt.BaseType,
			Values: rt.Values,
		}
		if rt.NotNull != nil {
			ut.NotNull = rt.NotNull
		}
		if rt.Default != nil {
			ut.Default = *rt.Default
		}
		if rt.DefaultExpr != nil {
			ut.DefaultExpr = *rt.DefaultExpr
		}
		if rt.Check != nil {
			ut.Check = *rt.Check
		}
		if rt.Unique != nil {
			ut.Unique = *rt.Unique
		}
		if rt.Comment != nil {
			ut.Comment = *rt.Comment
		}
		userTypes = append(userTypes, ut)
	}
	if len(userTypes) > 0 {
		loadDiags := reg.LoadUserTypes(userTypes)
		parseDiags = append(parseDiags, loadDiags...)
		if loadDiags.HasErrors() {
			return nil, parseDiags
		}
	}

	schema, buildDiags := model.Build(raw, reg)
	allDiags := append(parseDiags, buildDiags...)
	if buildDiags.HasErrors() {
		return nil, allDiags
	}

	return schema, allDiags
}

// diagsToJSON converts diagnostics to a JSON-friendly slice of maps.
func diagsToJSON(diags []diagnostic.Diagnostic) []map[string]string {
	result := make([]map[string]string, len(diags))
	for i, d := range diags {
		m := map[string]string{
			"severity": d.Severity.String(),
			"message":  d.Message,
		}
		if d.Code != "" {
			m["code"] = d.Code
		}
		if d.Table != "" {
			m["table"] = d.Table
		}
		if d.Column != "" {
			m["column"] = d.Column
		}
		if d.Suggestion != "" {
			m["suggestion"] = d.Suggestion
		}
		result[i] = m
	}
	return result
}
