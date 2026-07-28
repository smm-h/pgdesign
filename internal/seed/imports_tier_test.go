package seed

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// importConsumer builds a consumer schema whose "orders" table has an imported FK
// (RefAlias set) to app.users.id on column user_id. notNull and any extra unique
// constraints are configurable.
func importConsumer(notNull bool, uniques []model.UniqueConstraint, extraCols []model.Column) *model.Schema {
	cols := []model.Column{
		{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true, SemanticTypeName: "id"},
		{Name: "user_id", PGType: typeinfo.T("uuid"), NotNull: notNull},
	}
	cols = append(cols, extraCols...)
	orders := model.Table{
		Name: "orders", Schema: "public", Comment: "orders",
		Columns: cols,
		PK:      []string{"id"},
		Uniques: uniques,
		FKs: []model.FK{{
			Name: "fk_orders_user", Columns: []string{"user_id"},
			RefSchema: "app", RefTable: "users", RefColumns: []string{"id"},
			OnDelete: "CASCADE", RefAlias: "framework",
		}},
	}
	s := &model.Schema{Tables: []model.Table{orders}}
	s.Canonicalize()
	return s
}

func seedHasCode(diags diagnostic.Diagnostics, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Tier 2 offline INSERT: an imported FK yields a count-wrapped ordered-offset
// subquery, NEVER a random UUID (the silent fallback is unreachable).
func TestSeedTier2_OfflineSubquery_NoUUIDFallback(t *testing.T) {
	s := importConsumer(true, nil, nil)
	rng := rand.New(rand.NewSource(1))
	out, diags := Generate(s, 3, rng, &SeedConfig{Format: "insert", DBAvailable: false})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if !strings.Contains(out, "SELECT id FROM app.users ORDER BY id OFFSET") {
		t.Errorf("expected count-wrapped ordered-offset subquery, got:\n%s", out)
	}
	if !strings.Contains(out, "GREATEST((SELECT count(*) FROM app.users), 1)") {
		t.Errorf("expected count-wrapped modulo, got:\n%s", out)
	}
}

// Determinism: same seed + same config => byte-identical output.
func TestSeedTier2_Deterministic(t *testing.T) {
	run := func() string {
		s := importConsumer(true, nil, nil)
		return mustGen(t, s, 5, 7, &SeedConfig{Format: "insert", DBAvailable: false})
	}
	if run() != run() {
		t.Error("tier-2 seed output is not deterministic for a fixed seed")
	}
}

// Offset wrap: the offsets are rowIdx modulo the live count, so a larger row
// count reuses offsets deterministically (0,1,2,... all wrapped by the same
// count subquery).
func TestSeedTier2_OffsetWrap(t *testing.T) {
	s := importConsumer(true, nil, nil)
	rng := rand.New(rand.NewSource(1))
	out, _ := Generate(s, 4, rng, &SeedConfig{Format: "insert", DBAvailable: false})
	for _, n := range []string{"OFFSET (0 %", "OFFSET (1 %", "OFFSET (2 %", "OFFSET (3 %"} {
		if !strings.Contains(out, n) {
			t.Errorf("expected offset %q in output:\n%s", n, out)
		}
	}
}

// Tier-2 rescoped UNIQUE error: a UNIQUE distinguished SOLELY by the imported FK
// column is a hard error offline (S002).
func TestSeedTier2_UniqueSoleImportedFK_HardError(t *testing.T) {
	s := importConsumer(true, []model.UniqueConstraint{{Name: "uq_user", Columns: []string{"user_id"}}}, nil)
	rng := rand.New(rand.NewSource(1))
	_, diags := Generate(s, 3, rng, &SeedConfig{Format: "insert", DBAvailable: false})
	if !seedHasCode(diags, "S002") {
		t.Fatalf("expected S002 (sole-distinguishing imported FK unique), got: %v", diags)
	}
}

// A composite UNIQUE with an offline-distinct local column is FINE.
func TestSeedTier2_CompositeUniqueWithLocalCol_OK(t *testing.T) {
	s := importConsumer(true,
		[]model.UniqueConstraint{{Name: "uq_user_seq", Columns: []string{"user_id", "seq"}}},
		[]model.Column{{Name: "seq", PGType: typeinfo.T("int8"), NotNull: true, SemanticTypeName: "counter"}},
	)
	rng := rand.New(rand.NewSource(1))
	_, diags := Generate(s, 3, rng, &SeedConfig{Format: "insert", DBAvailable: false})
	if diags.HasErrors() {
		t.Fatalf("composite UNIQUE with local column must pass, got: %v", diags)
	}
}

// Tier-3 triple-constraint error: offline + COPY + NOT NULL imported FK (S003).
func TestSeedTier3_OfflineCopyNotNull_HardError(t *testing.T) {
	s := importConsumer(true, nil, nil)
	rng := rand.New(rand.NewSource(1))
	_, diags := Generate(s, 3, rng, &SeedConfig{Format: "copy", DBAvailable: false})
	if !seedHasCode(diags, "S003") {
		t.Fatalf("expected S003 (offline+COPY+NOT NULL), got: %v", diags)
	}
}

// Offline + COPY + NULLABLE imported FK is allowed (emits NULL).
func TestSeedTier3_OfflineCopyNullable_OK(t *testing.T) {
	s := importConsumer(false, nil, nil)
	rng := rand.New(rand.NewSource(1))
	_, diags := Generate(s, 3, rng, &SeedConfig{Format: "copy", DBAvailable: false})
	if diags.HasErrors() {
		t.Fatalf("nullable imported FK in COPY must be allowed, got: %v", diags)
	}
}

// Tier 1 (DB pool supplied): imported FK resolves to a real key from the pool,
// never a subquery or UUID.
func TestSeedTier1_RealKeyPool(t *testing.T) {
	s := importConsumer(true, nil, nil)
	rng := rand.New(rand.NewSource(1))
	pools := map[string][]string{
		"app.users.id": {"'aaaa'", "'bbbb'", "'cccc'"},
	}
	out, diags := Generate(s, 5, rng, &SeedConfig{Format: "insert", DBAvailable: true, ImportedFKPools: pools})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if strings.Contains(out, "SELECT id FROM app.users") {
		t.Errorf("tier 1 must use real-key pool, not a subquery:\n%s", out)
	}
	// Every emitted user_id must be from the pool.
	if !strings.Contains(out, "'aaaa'") && !strings.Contains(out, "'bbbb'") && !strings.Contains(out, "'cccc'") {
		t.Errorf("expected a pool key in output:\n%s", out)
	}
}

// Tier 1 with empty live pool + NOT NULL imported FK is a hard error (S004).
func TestSeedTier1_EmptyPoolNotNull_HardError(t *testing.T) {
	s := importConsumer(true, nil, nil)
	rng := rand.New(rand.NewSource(1))
	_, diags := Generate(s, 3, rng, &SeedConfig{Format: "insert", DBAvailable: true, ImportedFKPools: map[string][]string{}})
	if !seedHasCode(diags, "S004") {
		t.Fatalf("expected S004 (empty live pool + NOT NULL), got: %v", diags)
	}
}

func mustGen(t *testing.T, s *model.Schema, rows int, seed int64, cfg *SeedConfig) string {
	t.Helper()
	out, diags := Generate(s, rows, rand.New(rand.NewSource(seed)), cfg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	return out
}
