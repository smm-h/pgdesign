package codegen

// Execution-backed DDL matrix for the Python DDL tuple generator.
//
// This file lives in package codegen (not internal/test) because the matrix
// executes the raw ddlTuple stream produced by the unexported buildTuples /
// selfContainedTupleGroups functions; only an in-package test can reach them.
// The DB lifecycle reuses the internal/testdb ephemeral-database machinery
// (same precedent as internal/test/testdb_conformance_test.go).
//
// The fixture testdata/exec_matrix.toml covers every tuple kind that is
// executable on a vanilla PostgreSQL server. Kinds intentionally NOT covered:
//   - "extension": CREATE EXTENSION depends on server-side availability
//     (contrib packages), which would make the matrix environment-dependent.
//   - "partition"/"partman": pg_partman is not part of vanilla PostgreSQL,
//     and the partman create_parent call has no idempotent form (known gap,
//     see the buildTuples contract comment).
//   - "owner": ALTER TABLE ... OWNER TO requires a pre-existing cluster-wide
//     role, and table owner is not expressible in schema TOML (it only
//     appears via introspection).

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// inherentlyIdempotentKinds are the tuple kinds whose plain SQL is safe to
// re-run as-is, so buildTuples intentionally leaves IdempotentSQL empty.
// This mirrors the contract documented on buildTuples.
var inherentlyIdempotentKinds = map[string]bool{
	"comment":    true, // COMMENT ON overwrites the existing comment
	"statistics": true, // SET STATISTICS re-sets the same value
	"owner":      true, // OWNER TO re-assigns the same owner
	"rls_enable": true, // ENABLE ROW LEVEL SECURITY is a no-op if enabled
	"rls_force":  true, // FORCE ROW LEVEL SECURITY is a no-op if forced
}

// knownNonIdempotentKinds are tuple kinds with a KNOWN gap: no idempotent
// form exists and the plain SQL errors on re-run. Only pg_partman's
// create_parent call falls in this bucket (the "<table>_config" UPDATE tuple
// shares the kind but is a plain, inherently re-runnable UPDATE).
var knownNonIdempotentKinds = map[string]bool{
	"partman": true,
}

// loadExecMatrixSchema parses and builds the exec matrix fixture. It fails
// on ANY parse diagnostic (warnings indicate fixture key typos) and on build
// errors. Returns the schema and the type registry (needed by generate for
// state machine trigger emission).
func loadExecMatrixSchema(t *testing.T) (*model.Schema, *semtype.Registry) {
	t.Helper()
	raw, parseDiags := parse.File("testdata/exec_matrix.toml")
	if raw == nil {
		t.Fatalf("parse failed: %v", parseDiags)
	}
	for _, d := range parseDiags {
		t.Fatalf("fixture must parse cleanly, got diagnostic: [%s] %s", d.Code, d.Message)
	}
	reg := semtype.NewBuiltinRegistry()
	if userTypes := parse.CollectUserTypes(raw); len(userTypes) > 0 {
		loadDiags := reg.LoadUserTypes(userTypes)
		if loadDiags.HasErrors() {
			t.Fatalf("LoadUserTypes errors: %v", loadDiags)
		}
	}
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}
	for _, d := range buildDiags {
		if d.Severity == diagnostic.Warning {
			t.Logf("build warning: [%s] %s", d.Code, d.Message)
		}
	}
	return schema, reg
}

// execMatrixTuples builds the tuple stream and fails the test on diagnostics.
func execMatrixTuples(t *testing.T, schema *model.Schema) []ddlTuple {
	t.Helper()
	tuples, _, diags := buildTuples(schema)
	if len(diags) > 0 {
		t.Fatalf("buildTuples diagnostics: %v", diags)
	}
	return tuples
}

// TestExecMatrix_FixtureKindCoverage asserts the fixture exercises every
// tuple kind the matrix claims to cover. If buildTuples grows a new kind,
// either the fixture must exercise it or the exclusion list at the top of
// this file must document why not.
func TestExecMatrix_FixtureKindCoverage(t *testing.T) {
	schema, _ := loadExecMatrixSchema(t)
	tuples := execMatrixTuples(t, schema)

	expected := []string{
		"schema", "sequence", "enum", "domain", "composite", "table",
		"fk", "unique", "check", "exclusion", "index",
		"append_only_trigger", "comment", "statistics",
		"rls_enable", "rls_force", "policy",
		"view", "materialized_view", "function", "trigger",
	}
	got := make(map[string]bool)
	for _, tu := range tuples {
		got[tu.Kind] = true
	}
	for _, kind := range expected {
		if !got[kind] {
			t.Errorf("fixture produces no tuple of kind %q", kind)
		}
	}
	documentedAbsent := map[string]bool{
		"extension": true, "partition": true, "partman": true, "owner": true,
	}
	for kind := range got {
		found := false
		for _, e := range expected {
			if e == kind {
				found = true
				break
			}
		}
		if !found && !documentedAbsent[kind] {
			t.Errorf("fixture produces undocumented tuple kind %q; add it to the matrix expectations", kind)
		}
	}
}

// TestBuildTuples_IdempotentSQLContract enforces the buildTuples contract:
// every tuple either carries IdempotentSQL, or its kind is documented as
// inherently re-runnable (or a known gap). An empty IdempotentSQL on any
// other kind means the generated executor would silently re-run
// non-idempotent SQL under idempotent=True -- a real bug.
func TestBuildTuples_IdempotentSQLContract(t *testing.T) {
	schema, _ := loadExecMatrixSchema(t)
	tuples := execMatrixTuples(t, schema)

	for _, tu := range tuples {
		if tu.IdempotentSQL != "" {
			if inherentlyIdempotentKinds[tu.Kind] {
				t.Errorf("kind %q (name=%q) is documented as inherently idempotent but populates IdempotentSQL; update the contract", tu.Kind, tu.Name)
			}
			continue
		}
		if inherentlyIdempotentKinds[tu.Kind] || knownNonIdempotentKinds[tu.Kind] {
			continue
		}
		t.Errorf("tuple kind %q (name=%q) has empty IdempotentSQL but is not documented as inherently re-runnable; SQL:\n%s", tu.Kind, tu.Name, tu.SQL)
	}
}

// -- Live execution matrix (requires PostgreSQL) --

func execMatrixBaseURL() string {
	if u := os.Getenv("PGDESIGN_DB"); u != "" {
		return u
	}
	return "postgres://localhost:5432/postgres?sslmode=disable"
}

func execMatrixManager(t *testing.T) *testdb.Manager {
	t.Helper()
	m, err := testdb.NewManager(execMatrixBaseURL())
	if err != nil {
		t.Fatalf("create testdb manager: %v", err)
	}
	return m
}

// execSQLBlock splits a (possibly multi-statement) SQL string and executes
// each statement, failing the test with the offending statement text.
func execSQLBlock(t *testing.T, ctx context.Context, conn *pgx.Conn, label, sqlBlock string) {
	t.Helper()
	stmts, err := sqlparse.SplitStatements(sqlBlock)
	if err != nil {
		t.Fatalf("%s: split failed: %v\nSQL:\n%s", label, err, sqlBlock)
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("%s: execution failed: %v\nstatement:\n%s", label, err, s)
		}
	}
}

// applyTupleStream executes every tuple in order. When useIdempotent is
// true it prefers IdempotentSQL and falls back to SQL for the documented
// inherently re-runnable kinds (mirroring the generated executor).
func applyTupleStream(t *testing.T, ctx context.Context, conn *pgx.Conn, label string, tuples []ddlTuple, useIdempotent bool) {
	t.Helper()
	for i, tu := range tuples {
		stmtSQL := tu.SQL
		if useIdempotent && tu.IdempotentSQL != "" {
			stmtSQL = tu.IdempotentSQL
		}
		tupleLabel := fmt.Sprintf("%s: tuple %d (kind=%s name=%s)", label, i, tu.Kind, tu.Name)
		execSQLBlock(t, ctx, conn, tupleLabel, stmtSQL)
	}
}

// verifySchemaObjects runs sanity probes proving the DDL actually created
// the fixture's objects.
func verifySchemaObjects(t *testing.T, ctx context.Context, conn *pgx.Conn, label string) {
	t.Helper()
	probes := []struct {
		desc  string
		query string
		want  int
	}{
		{"tables", "SELECT count(*) FROM pg_tables WHERE schemaname = 'exec_matrix'", 3},
		{"views", "SELECT count(*) FROM pg_views WHERE schemaname = 'exec_matrix'", 1},
		{"matviews", "SELECT count(*) FROM pg_matviews WHERE schemaname = 'exec_matrix'", 1},
		{"sequences", "SELECT count(*) FROM pg_sequences WHERE schemaname = 'exec_matrix' AND sequencename = 'invoice_seq'", 1},
		{"policies", "SELECT count(*) FROM pg_policies WHERE schemaname = 'exec_matrix'", 1},
	}
	for _, p := range probes {
		var got int
		if err := conn.QueryRow(ctx, p.query).Scan(&got); err != nil {
			t.Fatalf("%s: probe %s failed: %v", label, p.desc, err)
		}
		if got != p.want {
			t.Errorf("%s: probe %s = %d, want %d", label, p.desc, got, p.want)
		}
	}
}

// TestDDLExecutionMatrix_GenerateSQL verifies the generate package output:
// non-idempotent DDL applies once on a fresh database; idempotent DDL
// applies twice on another fresh database.
func TestDDLExecutionMatrix_GenerateSQL(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	schema, reg := loadExecMatrixSchema(t)
	ctx := context.Background()
	m := execMatrixManager(t)

	t.Run("non_idempotent_once", func(t *testing.T) {
		out, diags, err := generate.Generate(schema, generate.Options{
			Idempotent:      false,
			IncludeComments: true,
			Format:          "sql",
			PGVersion:       schema.PGVersion,
			TypeRegistry:    reg,
		})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		for _, d := range diags {
			if d.Severity == diagnostic.Error {
				t.Fatalf("generate error: [%s] %s", d.Code, d.Message)
			}
		}
		db := m.SetupForTest(t, testdb.CreateOptions{})
		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		execSQLBlock(t, ctx, conn, "non-idempotent pass 1", out)
		verifySchemaObjects(t, ctx, conn, "non-idempotent")
	})

	t.Run("idempotent_twice", func(t *testing.T) {
		out, diags, err := generate.Generate(schema, generate.Options{
			Idempotent:      true,
			IncludeComments: true,
			Format:          "sql",
			PGVersion:       schema.PGVersion,
			TypeRegistry:    reg,
		})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		for _, d := range diags {
			if d.Severity == diagnostic.Error {
				t.Fatalf("generate error: [%s] %s", d.Code, d.Message)
			}
		}
		db := m.SetupForTest(t, testdb.CreateOptions{})
		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		execSQLBlock(t, ctx, conn, "idempotent pass 1", out)
		execSQLBlock(t, ctx, conn, "idempotent pass 2", out)
		verifySchemaObjects(t, ctx, conn, "idempotent")
	})
}

// TestDDLExecutionMatrix_Tuples executes the Python-DDL tuple streams for all
// three split modes against live PostgreSQL. For each mode: every tuple's
// plain SQL runs once in order on a fresh database, and on another fresh
// database the idempotent stream (IdempotentSQL, or SQL for the documented
// inherently re-runnable kinds) runs TWICE in order.
//
// Mode notes:
//   - SplitModeNone and SplitModeFaceted execute the identical buildTuples
//     stream: faceted output only redistributes tuples across files, and its
//     generated executor concatenates them back in the original phase order
//     (buildSections preserves tuple order). The "flat" subtest covers both.
//   - SplitModeSelfContained rewrites preamble tuples (schemas, extensions,
//     types, sequences) to their idempotent SQL and prefixes them to every
//     per-source group; each group must be independently executable on a
//     fresh database. The "self_contained" subtest executes each group
//     exactly as generated, via selfContainedTupleGroups (the same code path
//     the file generator renders).
func TestDDLExecutionMatrix_Tuples(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	schema, _ := loadExecMatrixSchema(t)
	ctx := context.Background()
	m := execMatrixManager(t)

	t.Run("flat_sql_once", func(t *testing.T) {
		tuples := execMatrixTuples(t, schema)
		db := m.SetupForTest(t, testdb.CreateOptions{})
		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		applyTupleStream(t, ctx, conn, "flat pass 1", tuples, false)
		verifySchemaObjects(t, ctx, conn, "flat")
	})

	t.Run("flat_idempotent_twice", func(t *testing.T) {
		tuples := execMatrixTuples(t, schema)
		db := m.SetupForTest(t, testdb.CreateOptions{})
		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		applyTupleStream(t, ctx, conn, "flat idempotent pass 1", tuples, true)
		applyTupleStream(t, ctx, conn, "flat idempotent pass 2", tuples, true)
		verifySchemaObjects(t, ctx, conn, "flat idempotent")
	})

	t.Run("faceted_sections_order", func(t *testing.T) {
		// Prove the faceted/monolithic executor section stream preserves the
		// buildTuples order, which is what makes flat_* cover faceted mode.
		tuples := execMatrixTuples(t, schema)
		sections := buildSections(tuples)
		var flattened []ddlTuple
		for _, sec := range sections {
			flattened = append(flattened, sec.tuples...)
		}
		if len(flattened) != len(tuples) {
			t.Fatalf("section flattening changed tuple count: %d != %d", len(flattened), len(tuples))
		}
		for i := range tuples {
			if flattened[i].SQL != tuples[i].SQL {
				t.Fatalf("section flattening reordered tuple %d (kind=%q name=%q)", i, tuples[i].Kind, tuples[i].Name)
			}
		}
	})

	t.Run("self_contained", func(t *testing.T) {
		groups, _, diags := selfContainedTupleGroups(schema)
		if groups == nil {
			t.Fatalf("selfContainedTupleGroups diagnostics: %v", diags)
		}
		for _, grp := range groups {
			// Each group must be independently executable on a fresh DB.
			db := m.SetupForTest(t, testdb.CreateOptions{})
			conn, err := db.Connect(ctx)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			applyTupleStream(t, ctx, conn, "self-contained "+grp.base+" pass 1", grp.tuples, false)

			// And re-runnable via the idempotent stream on another fresh DB.
			db2 := m.SetupForTest(t, testdb.CreateOptions{})
			conn2, err := db2.Connect(ctx)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			applyTupleStream(t, ctx, conn2, "self-contained "+grp.base+" idempotent pass 1", grp.tuples, true)
			applyTupleStream(t, ctx, conn2, "self-contained "+grp.base+" idempotent pass 2", grp.tuples, true)
		}
	})
}
