package discover

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// ephemeralURL is set by TestMain to point at the ephemeral database.
var ephemeralURL string

func testBaseURL() string {
	if u := os.Getenv("PGDESIGN_DB"); u != "" {
		return u
	}
	return "postgres://localhost:5432/postgres?sslmode=disable"
}

func TestMain(m *testing.M) {
	ctx := context.Background()

	manager, err := testdb.NewManager(testBaseURL())
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipping discover tests: %v\n", err)
		os.Exit(0)
	}

	ddl, err := os.Open("testdata/setup.sql")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open testdata/setup.sql: %v\n", err)
		os.Exit(1)
	}

	db, err := manager.Create(ctx, testdb.CreateOptions{DDL: ddl})
	ddl.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipping discover tests: %v\n", err)
		os.Exit(0)
	}

	ephemeralURL = db.URL

	code := m.Run()

	_ = manager.Drop(ctx, db)
	os.Exit(code)
}

func TestDiscoverBasicFDs(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ephemeralURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	fds, diags, err := Discover(conn, "public", "fd_test", Options{
		SampleSize: 5000,
		MaxColumns: 20,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(diags) > 0 {
		t.Logf("diagnostics: %v", diags)
	}

	// We expect to find A->B and B->C among the discovered FDs.
	// TANE discovers all minimal FDs that hold in the data sample.

	if len(fds) == 0 {
		t.Fatal("expected at least 2 FDs, got 0")
	}

	t.Logf("Discovered %d FDs:", len(fds))
	for _, f := range fds {
		t.Logf("  %s -> %s", strings.Join(f.Determinant, ","), strings.Join(f.Dependent, ","))
	}

	// Check that A->B is present.
	if !hasFD(fds, []string{"a"}, "b") {
		t.Error("expected FD a -> b")
	}

	// Check that B->C is present.
	if !hasFD(fds, []string{"b"}, "c") {
		t.Error("expected FD b -> c")
	}
}

func TestDiscoverMaxColumnsSkip(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ephemeralURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// fd_test has 4 columns; set MaxColumns to 3 to trigger the skip.
	fds, diags, err := Discover(conn, "public", "fd_test", Options{
		SampleSize: 5000,
		MaxColumns: 3,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(fds) != 0 {
		t.Errorf("expected 0 FDs when MaxColumns exceeded, got %d", len(fds))
	}

	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	if !strings.Contains(diags[0].Message, "4 columns") {
		t.Errorf("diagnostic message should mention column count, got: %s", diags[0].Message)
	}
}

func TestDiscoverEmptyTable(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ephemeralURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Create an empty table for this test.
	_, err = conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS empty_test (x int, y int)")
	if err != nil {
		t.Fatalf("create empty_test: %v", err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS empty_test")

	fds, diags, err := Discover(conn, "public", "empty_test", Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(fds) != 0 {
		t.Errorf("expected 0 FDs for empty table, got %d", len(fds))
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for empty table, got %d", len(diags))
	}
}

// --- Unit tests for TANE internals ---

func TestTaneInMemory(t *testing.T) {
	// In-memory data: A->B, B->C hold. D is independent.
	columns := []string{"a", "b", "c", "d"}
	data := [][]string{
		{"1", "10", "100", "1"},
		{"1", "10", "100", "2"},
		{"2", "20", "200", "3"},
		{"2", "20", "200", "4"},
		{"3", "30", "300", "5"},
		{"3", "30", "300", "6"},
	}

	fds := tane(columns, data, 0.0)

	t.Logf("TANE discovered %d FDs:", len(fds))
	for _, f := range fds {
		t.Logf("  %s -> %s", strings.Join(f.Determinant, ","), strings.Join(f.Dependent, ","))
	}

	if !hasFD(fds, []string{"a"}, "b") {
		t.Error("expected FD a -> b")
	}
	if !hasFD(fds, []string{"b"}, "c") {
		t.Error("expected FD b -> c")
	}
}

func TestTaneSuperkey(t *testing.T) {
	// All rows have unique values for column A, so A is a superkey.
	columns := []string{"a", "b"}
	data := [][]string{
		{"1", "10"},
		{"2", "20"},
		{"3", "30"},
	}

	fds := tane(columns, data, 0.0)

	if !hasFD(fds, []string{"a"}, "b") {
		t.Error("expected FD a -> b (a is a key)")
	}
}

func TestTaneBidirectional(t *testing.T) {
	// A and B have a 1:1 mapping: both A->B and B->A should be found.
	columns := []string{"a", "b"}
	data := [][]string{
		{"1", "x"},
		{"2", "y"},
		{"3", "z"},
	}

	fds := tane(columns, data, 0.0)

	if !hasFD(fds, []string{"a"}, "b") {
		t.Error("expected FD a -> b")
	}
	if !hasFD(fds, []string{"b"}, "a") {
		t.Error("expected FD b -> a")
	}
}

func TestPartitionProduct(t *testing.T) {
	// p1: rows {0,1,2} in one class, {3,4} in another.
	p1 := &partition{
		classes: [][]int{{0, 1, 2}, {3, 4}},
		numRows: 5,
	}
	// p2: rows {0,1} in one class, {2,3} in another.
	p2 := &partition{
		classes: [][]int{{0, 1}, {2, 3}},
		numRows: 5,
	}

	result := partitionProduct(p1, p2)

	// Intersection of p1 and p2:
	// Class {0,1,2} from p1 splits by p2:
	//   {0,1} (from p2 class {0,1}) -> kept (size 2)
	//   {2} (from p2 class {2,3}) -> singleton, not stored
	// Class {3,4} from p1 splits by p2:
	//   {3} (from p2 class {2,3}) -> singleton
	//   {4} is not in any p2 non-singleton class -> singleton
	// Result: one class {0,1}.

	if len(result.classes) != 1 {
		t.Fatalf("expected 1 class, got %d: %v", len(result.classes), result.classes)
	}

	sort.Ints(result.classes[0])
	if result.classes[0][0] != 0 || result.classes[0][1] != 1 {
		t.Errorf("expected class {0,1}, got %v", result.classes[0])
	}
}

func TestBuildPartition(t *testing.T) {
	data := [][]string{
		{"a", "x"},
		{"b", "x"},
		{"a", "y"},
		{"b", "x"},
	}

	p := buildPartition(data, 0) // column "a"

	// Rows 0,2 have "a"; rows 1,3 have "b".
	if len(p.classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(p.classes))
	}
	if p.numRows != 4 {
		t.Errorf("numRows = %d, want 4", p.numRows)
	}
}

func TestWideTableCreatedAndSkipped(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ephemeralURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Create a table with 25 columns.
	var colDefs []string
	for i := 0; i < 25; i++ {
		colDefs = append(colDefs, fmt.Sprintf("c%d int", i))
	}
	createSQL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS wide_test (%s)",
		strings.Join(colDefs, ", "),
	)
	_, err = conn.Exec(ctx, createSQL)
	if err != nil {
		t.Fatalf("create wide_test: %v", err)
	}

	fds, diags, err := Discover(conn, "public", "wide_test", Options{
		MaxColumns: 20,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(fds) != 0 {
		t.Errorf("expected 0 FDs, got %d", len(fds))
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Message, "25 columns") {
		t.Errorf("diagnostic should mention 25 columns: %s", diags[0].Message)
	}
}

// hasFD checks if the given determinant -> dependent exists in the FD list.
func hasFD(fds []fd.FuncDep, determinant []string, dependent string) bool {
	sortedDet := make([]string, len(determinant))
	copy(sortedDet, determinant)
	sort.Strings(sortedDet)

	for _, f := range fds {
		sortedFDet := make([]string, len(f.Determinant))
		copy(sortedFDet, f.Determinant)
		sort.Strings(sortedFDet)

		if len(sortedFDet) != len(sortedDet) {
			continue
		}
		match := true
		for i := range sortedDet {
			if sortedFDet[i] != sortedDet[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		for _, d := range f.Dependent {
			if d == dependent {
				return true
			}
		}
	}
	return false
}
