package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/validate"
	"github.com/smm-h/pgdesign/internal/workload"
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

// handleSchema introspects the DB and returns the canonical whole-model JSON
// envelope {format_version, revision, model, diagnostics?}. The model is
// introspected, so it lacks a type registry: registry-absent class (L7). This
// routes through the SAME whole-model serializer as `generate json`
// (internal/rev), so their bodies are byte-identical for the same model. The
// old {schema, diagnostics} shape is WRAPPED, not dropped — diagnostics survive
// under the envelope's diagnostics key; the payload-key change is
// consumer-visible.
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	if s.projectMode() {
		// Project mode: serve the compiled model verbatim through THE canonical
		// envelope serializer with the registry-present class (L7). rev.Marshal is
		// the exact function `generate json` calls, so this body is byte-identical to
		// `generate json` for the same model (and diagnostics). Build diagnostics are
		// wrapped under the envelope's diagnostics key.
		body, err := rev.Marshal(s.project.schema, rev.RegistryPresent, s.project.diags)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("envelope: %v", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}

	schema, diags, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("introspect: %v", err))
		return
	}

	body, err := rev.Marshal(schema, rev.RegistryAbsent, diags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("envelope: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// handleSchemaGraph returns the deterministic FK-graph projection (nodes with
// fan-in/out, sorted edges) of the served model. The projection is derived and
// excluded from schema identity, so it is served alongside the envelope rather
// than inside it. Project mode uses the compiled model; database mode introspects.
func (s *Server) handleSchemaGraph(w http.ResponseWriter, r *http.Request) {
	schema, err := s.resolveSchema(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve schema: %v", err))
		return
	}
	if schema.FKGraph == nil {
		schema.BuildFKGraph()
	}
	writeJSON(w, http.StatusOK, schema.FKGraph.Project())
}

// handleSchemaD2 returns D2 diagram text for the served model. In project mode the
// real semtype registry is passed so state-machine state diagrams render (the
// database path has no registry — registry-absent — and cannot draw them).
func (s *Server) handleSchemaD2(w http.ResponseWriter, r *http.Request) {
	schema, err := s.resolveSchema(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve schema: %v", err))
		return
	}

	d2 := generate.GenerateD2(schema, s.registry())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(d2))
}

// handleSchemaSVG renders the D2 diagram of the served model to SVG. Project mode
// passes the real registry so state-machine diagrams render.
func (s *Server) handleSchemaSVG(w http.ResponseWriter, r *http.Request) {
	schema, err := s.resolveSchema(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve schema: %v", err))
		return
	}

	d2Source := generate.GenerateD2(schema, s.registry())
	svg, err := generate.RenderSVG(d2Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("render SVG: %v", err))
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	w.Write(svg)
}

// handleSchemaDoc returns human-readable schema documentation (the `generate doc`
// format) for the served model.
func (s *Server) handleSchemaDoc(w http.ResponseWriter, r *http.Request) {
	schema, err := s.resolveSchema(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve schema: %v", err))
		return
	}
	out, _, genErr := generate.Generate(schema, generate.Options{Format: "doc"})
	if genErr != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate doc: %v", genErr))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
}

// resolveSchema returns the model to serve for a read-only schema endpoint: the
// compiled project model in project mode, or a fresh introspection in database
// mode. It is the shared entry point for the d2/svg/graph/doc endpoints.
func (s *Server) resolveSchema(r *http.Request) (*model.Schema, error) {
	if s.projectMode() {
		return s.project.schema, nil
	}
	schema, _, err := introspect.Introspect(r.Context(), s.connStr(), s.schemas)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// registry returns the semtype registry to pass to D2 generation: the project's
// registry in project mode (so state-machine diagrams render), nil in database
// mode (an introspected model is registry-absent).
func (s *Server) registry() *semtype.Registry {
	if s.projectMode() {
		return s.project.registry
	}
	return nil
}

// handleMigrations returns applied migrations. PRECEDENCE (serve is read-only, so
// it serves whichever tracking surface a database presents): the chain-era
// pgdesign_applied_migrations VIEW when it exists (post-upgrade databases); else
// the legacy pgdesign_migrations table when only it exists (pre-upgrade
// databases the migrate subcommands force through `migrate upgrade`, but serve
// keeps reading); else 200 [] when neither exists. Both surfaces expose the same
// (version, applied_at, description, checksum) shape.
func (s *Server) handleMigrations(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	ctx := r.Context()

	type migration struct {
		Version     string    `json:"version"`
		AppliedAt   time.Time `json:"applied_at"`
		Description string    `json:"description"`
		Checksum    string    `json:"checksum"`
	}

	relExists := func(rel string) (bool, error) {
		var exists bool
		err := s.pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&exists)
		return exists, err
	}

	viewExists, err := relExists("pgdesign_applied_migrations")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("check migrations view: %v", err))
		return
	}
	legacyExists, err := relExists("pgdesign_migrations")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("check migrations table: %v", err))
		return
	}

	// version_label is served as `version` from the view (the preserved semver for
	// prefix rows, the edge_id for post-upgrade edges); applied_at orders both.
	var query string
	switch {
	case viewExists:
		query = `SELECT version, applied_at, COALESCE(description, ''), checksum
			FROM pgdesign_applied_migrations
			ORDER BY applied_at`
	case legacyExists:
		query = `SELECT version, applied_at, COALESCE(description, ''), checksum
			FROM pgdesign_migrations
			ORDER BY applied_at`
	default:
		writeJSON(w, http.StatusOK, []migration{})
		return
	}

	rows, err := s.pool.Query(ctx, query)
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

// migrationVersionPath resolves the on-disk path for a migration version,
// enforcing that it stays within migrationsDir. It returns ok=false for any
// version containing a path separator or ".." segment, or whose resolved path
// escapes migrationsDir -- defeating path traversal (e.g. "../../etc/passwd").
func migrationVersionPath(migrationsDir, version string) (string, bool) {
	if version == "" || strings.ContainsAny(version, `/\`) || strings.Contains(version, "..") {
		return "", false
	}
	path := filepath.Join(migrationsDir, version+".toml")
	cleanDir := filepath.Clean(migrationsDir)
	rel, err := filepath.Rel(cleanDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

// handleMigrationVersion serves a single migration by name. For a chain-mode
// project (migrations/chain/ present) it returns the raw JSON edge artifact keyed
// by its content-derived filename (or the 12-hex edge-id prefix); for a legacy
// project it parses and returns the semver .toml file. Path traversal is defeated
// in both modes (no separators / ".." segments; resolved path must stay within
// the store directory).
func (s *Server) handleMigrationVersion(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")

	if migrate.IsChainMode(s.migrationsDir) {
		s.serveChainEdge(w, version)
		return
	}

	path, ok := migrationVersionPath(s.migrationsDir, version)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid migration version %q", version))
		return
	}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("migration %q not found", version))
		return
	}

	m, err := migrate.ParseMigrationFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("parse migration: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// serveChainEdge returns the raw JSON edge artifact for a chain-mode project. ref
// is either the exact edge filename or the 12-hex edge-id prefix; it must contain
// no path separator or ".." segment (traversal defense preserved from the legacy
// path). The edge is looked up in migrations/chain/ (live edges only).
func (s *Server) serveChainEdge(w http.ResponseWriter, ref string) {
	if ref == "" || strings.ContainsAny(ref, `/\`) || strings.Contains(ref, "..") {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid edge reference %q", ref))
		return
	}
	chainDir := filepath.Join(s.migrationsDir, "chain")
	entries, err := os.ReadDir(chainDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read chain dir: %v", err))
		return
	}
	var match string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "edge-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Exact filename, or the content-hash prefix (edge-<12hex>-...).
		if e.Name() == ref || strings.HasPrefix(e.Name(), "edge-"+ref+"-") {
			match = e.Name()
			break
		}
	}
	if match == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("edge %q not found", ref))
		return
	}
	data, err := os.ReadFile(filepath.Join(chainDir, match))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read edge: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleStats returns database statistics for all tables in the configured schemas.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
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

	var hitRatio *float64
	err = s.pool.QueryRow(ctx,
		`SELECT blks_hit::float / NULLIF(blks_hit + blks_read, 0) AS hit_ratio FROM pg_stat_database WHERE datname = current_database()`).Scan(&hitRatio)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query hit ratio: %v", err))
		return
	}

	resp := map[string]any{"tables": stats}
	if hitRatio != nil {
		resp["hit_ratio"] = *hitRatio
	}
	writeJSON(w, http.StatusOK, resp)
}

// indexStat holds per-index statistics.
type indexStat struct {
	IndexName string   `json:"index_name"`
	IdxScan   int64    `json:"idx_scan"`
	SizeBytes int64    `json:"size_bytes"`
	Unused    bool     `json:"unused"`
	Columns   []string `json:"columns"`
}

// handleTableStats returns per-table stats including column info and index usage.
func (s *Server) handleTableStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	ctx := r.Context()
	table := r.PathValue("table")

	rows, err := s.pool.Query(ctx,
		`SELECT
			sui.indexrelname,
			sui.idx_scan,
			pg_relation_size(sui.indexrelid) as size_bytes,
			array_to_string(ARRAY(
				SELECT pg_get_indexdef(sui.indexrelid, k + 1, true)
				FROM generate_subscripts(ix.indkey, 1) AS k
				ORDER BY k
			), ',') AS columns
		FROM pg_stat_user_indexes sui
		JOIN pg_index ix ON ix.indexrelid = sui.indexrelid
		WHERE sui.schemaname||'.'||sui.relname = $1`,
		table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query table stats: %v", err))
		return
	}
	defer rows.Close()

	var indexes []indexStat
	for rows.Next() {
		var idx indexStat
		var cols string
		if err := rows.Scan(&idx.IndexName, &idx.IdxScan, &idx.SizeBytes, &cols); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan index row: %v", err))
			return
		}
		idx.Unused = (idx.IdxScan == 0)
		if cols != "" {
			idx.Columns = strings.Split(cols, ",")
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
	// Convert to workload.IndexInfo for consolidated duplicate detection.
	var wkInfos []workload.IndexInfo
	parts := strings.SplitN(table, ".", 2)
	schemaName, tableName := parts[0], parts[1]
	for _, idx := range indexes {
		wkInfos = append(wkInfos, workload.IndexInfo{
			Schema:  schemaName,
			Table:   tableName,
			Name:    idx.IndexName,
			Columns: idx.Columns,
		})
	}
	wkDups := workload.FindDuplicateIndexes(wkInfos)
	type duplicateIndexPair struct {
		Redundant string `json:"redundant"`
		CoveredBy string `json:"covered_by"`
	}
	duplicates := make([]duplicateIndexPair, len(wkDups))
	for i, d := range wkDups {
		duplicates[i] = duplicateIndexPair{
			Redundant: d.Index,
			CoveredBy: d.SupersetIndex,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"table":      table,
		"indexes":    indexes,
		"duplicates": duplicates,
	})
}

// handleExtensions returns installed PostgreSQL extensions.
func (s *Server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
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
	if !s.requireDB(w) {
		return
	}
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

	if collErr := diff.CheckTruncationCollisions(desired); collErr != nil {
		writeError(w, http.StatusBadRequest, collErr.Error())
		return
	}
	// actual is introspected (registry-absent); use the introspected diff path so
	// class-aware fields (semantic type names) do not false-drift.
	d := diff.DiffLive(desired, actual, nil)
	writeJSON(w, http.StatusOK, d)
}

// handleAuditStart launches an asynchronous audit job (introspect + TANE FD
// discovery + audit) and returns 202 with the job id. TANE is unbounded work, so
// running it synchronously per request was a self-DoS button; the job is
// cancellable and the manager bounds concurrency. Poll GET /api/audit/jobs/{id}.
func (s *Server) handleAuditStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	job, err := s.auditJobs.start(s.runAudit)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "audit job manager at capacity; retry after a running job completes")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":     job.ID,
		"status": job.Status,
	})
}

// handleAuditPoll returns the current state of an audit job. A finished job
// (status done) carries its diagnostics.
func (s *Server) handleAuditPoll(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.auditJobs.snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("audit job %q not found", r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// handleAuditCancel requests cancellation of a running audit job. The job
// transitions to cancelled once its worker observes the cancelled context.
func (s *Server) handleAuditCancel(w http.ResponseWriter, r *http.Request) {
	if !s.auditJobs.cancel(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("audit job %q not found", r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "cancelling"})
}

// runAudit is the audit job body: introspect the live DB, discover FDs from live
// data (TANE) for tables without declared FDs, then audit. It honors ctx
// cancellation at every DB step so a cancelled or timed-out job stops promptly.
func (s *Server) runAudit(ctx context.Context) ([]diagnostic.Diagnostic, error) {
	schema, _, err := introspect.Introspect(ctx, s.connStr(), s.schemas)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	var allDiags []diagnostic.Diagnostic

	conn, err := s.pool.Acquire(ctx)
	if err == nil {
		pgxConn := conn.Conn()
		opts := discover.Options{}
		for i := range schema.Tables {
			if ctx.Err() != nil {
				conn.Release()
				return nil, ctx.Err()
			}
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

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	allDiags = append(allDiags, audit.Audit(schema)...)
	return allDiags, nil
}

// parseAndBuild parses TOML bytes and builds a resolved schema.
func parseAndBuild(data []byte) (*model.Schema, []diagnostic.Diagnostic) {
	raw, parseDiags := parse.Bytes(data)
	if raw == nil {
		return nil, parseDiags
	}

	reg := semtype.NewBuiltinRegistry()

	userTypes := parse.CollectUserTypes(raw)
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
