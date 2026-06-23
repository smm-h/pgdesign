package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// constraintTestSchema builds a schema that exercises all constraint types:
// NOT NULL, enum, unique, FK, CHECK range, CHECK comparison, CHECK length,
// CHECK pattern (like), ON_DELETE (cascade, restrict, set_null), and SM transition.
func constraintTestSchema() *model.Schema {
	schema := &model.Schema{
		Name: "constraint_test",
		Enums: []model.Enum{
			{Name: "priority", Values: []string{"low", "medium", "high"}},
		},
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "email", PGType: "text", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "age", PGType: "integer", NotNull: true},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_email", Columns: []string{"email"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_age", Expr: "age >= 0 AND age <= 200"},
				},
			},
			{
				Name:   "orders",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "user_id", PGType: "uuid", NotNull: true},
					{Name: "status", PGType: "text", NotNull: true, SemanticTypeName: "order_status"},
					{Name: "priority", PGType: "priority", NotNull: true},
					{Name: "total", PGType: "numeric", NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_user", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "RESTRICT"},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_total", Expr: "total > 0"},
				},
			},
			{
				Name:   "order_items",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "order_id", PGType: "integer", NotNull: true},
					{Name: "product", PGType: "text", NotNull: true},
					{Name: "quantity", PGType: "integer", NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_order", Columns: []string{"order_id"}, RefTable: "orders", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_order_product", Columns: []string{"order_id", "product"}},
				},
			},
			{
				Name:   "audit_log",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "user_id", PGType: "uuid", NotNull: false},
					{Name: "action", PGType: "text", NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_user", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "SET NULL"},
				},
			},
		},
		StateMachineTransitions: []model.SMTransitionMap{
			{
				TypeName: "order_status",
				Transitions: map[string][]string{
					"pending":   {"confirmed", "cancelled"},
					"confirmed": {"shipped"},
				},
				States: []string{"pending", "confirmed", "shipped", "cancelled"},
				NamedTransitions: []model.NamedTransition{
					{Name: "confirm", From: []string{"pending"}, To: "confirmed"},
					{Name: "cancel", From: []string{"pending"}, To: "cancelled"},
					{Name: "ship", From: []string{"confirmed"}, To: "shipped"},
				},
			},
		},
	}
	schema.BuildFKGraph()
	return schema
}

func TestConstraints_FileProduced(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, diags := gen.GenerateFiles(schema)

	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if _, ok := files["_constraints.py"]; !ok {
		t.Fatal("_constraints.py not found in generated files")
	}
}

func TestConstraints_EnumAndDataclass(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	// ConstraintKind enum.
	if !strings.Contains(content, "class ConstraintKind(Enum):") {
		t.Error("missing ConstraintKind enum")
	}
	for _, kind := range []string{
		"NOT_NULL", "ENUM", "UNIQUE", "FK",
		"CHECK_RANGE", "CHECK_COMPARISON", "CHECK_LENGTH", "CHECK_PATTERN",
		"ON_DELETE_CASCADE", "ON_DELETE_RESTRICT", "ON_DELETE_SET_NULL",
		"STATE_MACHINE_TRANSITION",
	} {
		if !strings.Contains(content, kind+" = ") {
			t.Errorf("missing ConstraintKind.%s", kind)
		}
	}

	// Constraint dataclass.
	if !strings.Contains(content, "@dataclass(frozen=True)") {
		t.Error("missing frozen dataclass decorator")
	}
	if !strings.Contains(content, "class Constraint:") {
		t.Error("missing Constraint class")
	}
	if !strings.Contains(content, "kind: ConstraintKind") {
		t.Error("missing kind field")
	}
	if !strings.Contains(content, "columns: tuple[str, ...]") {
		t.Error("missing columns field")
	}
	if !strings.Contains(content, "params: dict") {
		t.Error("missing params field")
	}
	if !strings.Contains(content, "message: str") {
		t.Error("missing message field")
	}
}

func TestConstraints_UsersTable(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "USERS_CONSTRAINTS: list[Constraint] = [") {
		t.Fatal("missing USERS_CONSTRAINTS list")
	}

	usersSection := extractBetween(content, "USERS_CONSTRAINTS: list[Constraint] = [", "]\n")

	// NOT_NULL for email, name, age (id excluded as auto PK).
	if !strings.Contains(usersSection, "ConstraintKind.NOT_NULL") {
		t.Error("missing NOT_NULL constraint in users")
	}
	// Should have NOT_NULL for email, name, age.
	for _, col := range []string{"email", "name", "age"} {
		needle := fmt.Sprintf(`columns=("%s",)`, col)
		// Check that the column appears in a NOT_NULL context.
		idx := strings.Index(usersSection, needle)
		if idx < 0 {
			t.Errorf("missing NOT_NULL for column %s", col)
		}
	}

	// UNIQUE for email.
	if !strings.Contains(usersSection, "ConstraintKind.UNIQUE") {
		t.Error("missing UNIQUE constraint in users")
	}

	// CHECK range for age (age >= 0 AND age <= 200).
	if !strings.Contains(usersSection, "ConstraintKind.CHECK_RANGE") {
		t.Error("missing CHECK_RANGE constraint in users")
	}
	if !strings.Contains(usersSection, `"low": 0`) {
		t.Error("missing low bound in age CHECK_RANGE")
	}
	if !strings.Contains(usersSection, `"high": 200`) {
		t.Error("missing high bound in age CHECK_RANGE")
	}

	// ON_DELETE_RESTRICT from orders.user_id and ON_DELETE_SET_NULL from audit_log.user_id.
	if !strings.Contains(usersSection, "ConstraintKind.ON_DELETE_RESTRICT") {
		t.Error("missing ON_DELETE_RESTRICT constraint in users (from orders)")
	}
	if !strings.Contains(usersSection, "ConstraintKind.ON_DELETE_SET_NULL") {
		t.Error("missing ON_DELETE_SET_NULL constraint in users (from audit_log)")
	}
}

func TestConstraints_OrdersTable(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "ORDERS_CONSTRAINTS: list[Constraint] = [") {
		t.Fatal("missing ORDERS_CONSTRAINTS list")
	}

	ordersSection := extractBetween(content, "ORDERS_CONSTRAINTS: list[Constraint] = [", "]\n")

	// ENUM for priority.
	if !strings.Contains(ordersSection, "ConstraintKind.ENUM") {
		t.Error("missing ENUM constraint for priority")
	}
	if !strings.Contains(ordersSection, `"low"`) {
		t.Error("missing enum value 'low'")
	}
	if !strings.Contains(ordersSection, `"medium"`) {
		t.Error("missing enum value 'medium'")
	}
	if !strings.Contains(ordersSection, `"high"`) {
		t.Error("missing enum value 'high'")
	}

	// FK for user_id.
	if !strings.Contains(ordersSection, "ConstraintKind.FK") {
		t.Error("missing FK constraint for user_id")
	}
	if !strings.Contains(ordersSection, `"ref_table": "users"`) {
		t.Error("missing FK ref_table 'users'")
	}

	// CHECK comparison for total > 0.
	if !strings.Contains(ordersSection, "ConstraintKind.CHECK_COMPARISON") {
		t.Error("missing CHECK_COMPARISON constraint for total")
	}

	// STATE_MACHINE_TRANSITION for status.
	if !strings.Contains(ordersSection, "ConstraintKind.STATE_MACHINE_TRANSITION") {
		t.Error("missing STATE_MACHINE_TRANSITION constraint for status")
	}
	if !strings.Contains(ordersSection, `"transitions"`) {
		t.Error("missing transitions param in SM constraint")
	}
	if !strings.Contains(ordersSection, `"pending"`) {
		t.Error("missing 'pending' state in SM transitions")
	}

	// ON_DELETE_CASCADE from order_items.
	if !strings.Contains(ordersSection, "ConstraintKind.ON_DELETE_CASCADE") {
		t.Error("missing ON_DELETE_CASCADE constraint in orders (from order_items)")
	}
}

func TestConstraints_OrderItemsTable(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "ORDER_ITEMS_CONSTRAINTS: list[Constraint] = [") {
		t.Fatal("missing ORDER_ITEMS_CONSTRAINTS list")
	}

	section := extractBetween(content, "ORDER_ITEMS_CONSTRAINTS: list[Constraint] = [", "]\n")

	// UNIQUE for (order_id, product).
	if !strings.Contains(section, "ConstraintKind.UNIQUE") {
		t.Error("missing UNIQUE constraint for order_id, product")
	}
	if !strings.Contains(section, `"order_id", "product"`) {
		t.Error("missing multi-column unique columns")
	}

	// FK for order_id.
	if !strings.Contains(section, "ConstraintKind.FK") {
		t.Error("missing FK constraint for order_id")
	}
	if !strings.Contains(section, `"ref_table": "orders"`) {
		t.Error("missing FK ref_table 'orders'")
	}
}

func TestConstraints_ConstraintEngine(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "class ConstraintEngine:") {
		t.Error("missing ConstraintEngine class")
	}
	if !strings.Contains(content, "def validate_insert(") {
		t.Error("missing validate_insert method")
	}
	if !strings.Contains(content, "def validate_update(") {
		t.Error("missing validate_update method")
	}
	if !strings.Contains(content, "def process_delete(") {
		t.Error("missing process_delete method")
	}

	// Verify method signatures contain expected params.
	if !strings.Contains(content, "constraints: list[Constraint]") {
		t.Error("missing constraints parameter in engine methods")
	}
	if !strings.Contains(content, "stores: dict[str, dict]") {
		t.Error("missing stores parameter in engine methods")
	}
	if !strings.Contains(content, "unique_indexes: dict[str, dict]") {
		t.Error("missing unique_indexes parameter in engine methods")
	}
}

func TestConstraints_EngineInsertValidation(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	// Verify the insert validation handles each constraint kind.
	engineSection := extractAfter(content, "class ConstraintEngine:")

	for _, kind := range []string{
		"ConstraintKind.NOT_NULL",
		"ConstraintKind.ENUM",
		"ConstraintKind.UNIQUE",
		"ConstraintKind.FK",
		"ConstraintKind.CHECK_RANGE",
		"ConstraintKind.CHECK_COMPARISON",
		"ConstraintKind.CHECK_LENGTH",
		"ConstraintKind.CHECK_PATTERN",
	} {
		if !strings.Contains(engineSection, kind) {
			t.Errorf("validate_insert missing handler for %s", kind)
		}
	}
}

func TestConstraints_EngineUpdateSMTransition(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	// validate_update should check STATE_MACHINE_TRANSITION.
	updateSection := extractAfter(content, "def validate_update(")
	if !strings.Contains(updateSection, "STATE_MACHINE_TRANSITION") {
		t.Error("validate_update missing STATE_MACHINE_TRANSITION handler")
	}
	if !strings.Contains(updateSection, "transitions") {
		t.Error("validate_update missing transitions lookup in SM handler")
	}
}

func TestConstraints_EngineDeleteProcessing(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	deleteSection := extractAfter(content, "def process_delete(")

	// Should handle CASCADE, RESTRICT, and SET_NULL.
	if !strings.Contains(deleteSection, "ON_DELETE_RESTRICT") {
		t.Error("process_delete missing ON_DELETE_RESTRICT handler")
	}
	if !strings.Contains(deleteSection, "ON_DELETE_CASCADE") {
		t.Error("process_delete missing ON_DELETE_CASCADE handler")
	}
	if !strings.Contains(deleteSection, "ON_DELETE_SET_NULL") {
		t.Error("process_delete missing ON_DELETE_SET_NULL handler")
	}

	// CASCADE should recursively call process_delete.
	if !strings.Contains(deleteSection, "ConstraintEngine.process_delete(") {
		t.Error("CASCADE handler missing recursive process_delete call")
	}
}

func TestConstraints_EmptySchema(t *testing.T) {
	schema := &model.Schema{Tables: []model.Table{}}
	schema.BuildFKGraph()
	gen := &PythonQueryLayerGenerator{}
	files, diags := gen.GenerateFiles(schema)

	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	content := string(files["_constraints.py"])
	if !strings.Contains(content, "class ConstraintKind(Enum):") {
		t.Error("empty schema should still have ConstraintKind enum")
	}
	if !strings.Contains(content, "class Constraint:") {
		t.Error("empty schema should still have Constraint dataclass")
	}
	// Should NOT have per-table constraints or engine.
	if strings.Contains(content, "_CONSTRAINTS: list[Constraint]") {
		t.Error("empty schema should not have any constraint lists")
	}
}

func TestConstraints_CheckLengthPattern(t *testing.T) {
	schema := &model.Schema{
		Name: "len_test",
		Tables: []model.Table{
			{
				Name:   "profiles",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "bio", PGType: "text", NotNull: false},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_bio_len", Expr: "LENGTH(bio) <= 500"},
				},
			},
		},
	}
	schema.BuildFKGraph()

	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "ConstraintKind.CHECK_LENGTH") {
		t.Error("missing CHECK_LENGTH constraint for bio")
	}
	if !strings.Contains(content, `"op": "<="`) {
		t.Error("missing length op '<='")
	}
	if !strings.Contains(content, `"value": 500`) {
		t.Error("missing length value 500")
	}
}

func TestConstraints_CheckPatternLike(t *testing.T) {
	schema := &model.Schema{
		Name: "pattern_test",
		Tables: []model.Table{
			{
				Name:   "codes",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "code", PGType: "text", NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_code_fmt", Expr: "code LIKE 'ABC-%'"},
				},
			},
		},
	}
	schema.BuildFKGraph()

	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "ConstraintKind.CHECK_PATTERN") {
		t.Error("missing CHECK_PATTERN constraint for code")
	}
	if !strings.Contains(content, `"pattern"`) {
		t.Error("missing pattern param")
	}
}

func TestConstraints_DomainCheck(t *testing.T) {
	schema := &model.Schema{
		Name: "domain_test",
		Domains: []model.Domain{
			{Name: "positive_int", BaseType: "integer", Check: "VALUE > 0"},
		},
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true, Identity: "ALWAYS"},
					{Name: "quantity", PGType: "positive_int", NotNull: true},
				},
			},
		},
	}
	schema.BuildFKGraph()

	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	content := string(files["_constraints.py"])

	if !strings.Contains(content, "ConstraintKind.CHECK_COMPARISON") {
		t.Error("missing CHECK_COMPARISON from domain check for quantity")
	}
}

func TestConstraints_SingleFileContainsBothFiles(t *testing.T) {
	schema := constraintTestSchema()
	gen := &PythonQueryLayerGenerator{}
	out, diags := gen.Generate(schema)

	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	content := string(out)
	if !strings.Contains(content, "_constraints.py") {
		t.Error("single-file output should contain _constraints.py header")
	}
	if !strings.Contains(content, "protocols.py") {
		t.Error("single-file output should contain protocols.py header")
	}
}

// extractBetween returns the text between start marker and end marker.
func extractBetween(content, start, end string) string {
	idx := strings.Index(content, start)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(start):]
	endIdx := strings.Index(rest, end)
	if endIdx < 0 {
		return rest
	}
	return rest[:endIdx]
}
