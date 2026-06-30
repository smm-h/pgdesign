package codegen

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// smTestSchema returns a schema with a state machine type for testing.
func smTestSchema() *model.Schema {
	return &model.Schema{
		Name: "app",
		Enums: []model.Enum{
			{
				Schema: "app",
				Name:   "order_status",
				Values: []string{"pending", "active", "shipped", "cancelled"},
			},
		},
		StateMachineTransitions: []model.SMTransitionMap{
			{
				TypeName: "order_status",
				States:   []string{"pending", "active", "shipped", "cancelled"},
				Transitions: map[string][]string{
					"pending": {"active", "cancelled"},
					"active":  {"cancelled", "shipped"},
				},
			},
		},
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "status", PGType: typeinfo.MustParse("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				},
			},
		},
	}
}

// --- Types output tests ---

func TestGoTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &GoTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum type should be present.
	if !strings.Contains(result, "type OrderStatus string") {
		t.Error("missing OrderStatus enum type")
	}

	// Transition map should be present.
	if !strings.Contains(result, "var OrderStatusTransitions = map[OrderStatus][]OrderStatus{") {
		t.Error("missing OrderStatusTransitions map declaration")
	}

	// Check specific entries (targets are sorted alphabetically).
	if !strings.Contains(result, "OrderStatusActive: {OrderStatusCancelled, OrderStatusShipped},") {
		t.Errorf("missing or incorrect active transitions, got:\n%s", result)
	}
	if !strings.Contains(result, "OrderStatusPending: {OrderStatusActive, OrderStatusCancelled},") {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

func TestTSTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &TSTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum type (TS string union) should be present.
	if !strings.Contains(result, `export type OrderStatus =`) {
		t.Error("missing OrderStatus type")
	}

	// Transition map should be present.
	if !strings.Contains(result, "export const orderStatusTransitions: Record<OrderStatus, OrderStatus[]>") {
		t.Errorf("missing orderStatusTransitions declaration, got:\n%s", result)
	}

	// Check specific entries (targets are sorted alphabetically).
	if !strings.Contains(result, `"active": ["cancelled", "shipped"]`) {
		t.Errorf("missing or incorrect active transitions, got:\n%s", result)
	}
	if !strings.Contains(result, `"pending": ["active", "cancelled"]`) {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

func TestPythonTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &PythonTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum class should be present.
	if !strings.Contains(result, "class OrderStatus(StrEnum):") {
		t.Error("missing OrderStatus enum class")
	}

	// Enum import should be present.
	if !strings.Contains(result, "from enum import StrEnum") {
		t.Error("missing enum import")
	}

	// Transition map should be present.
	if !strings.Contains(result, "ORDER_STATUS_TRANSITIONS: dict[OrderStatus, list[OrderStatus]]") {
		t.Errorf("missing ORDER_STATUS_TRANSITIONS declaration, got:\n%s", result)
	}

	// Check specific entries (targets are sorted alphabetically).
	if !strings.Contains(result, "OrderStatus.ACTIVE: [OrderStatus.CANCELLED, OrderStatus.SHIPPED]") {
		t.Errorf("missing or incorrect active transitions, got:\n%s", result)
	}
	if !strings.Contains(result, "OrderStatus.PENDING: [OrderStatus.ACTIVE, OrderStatus.CANCELLED]") {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

func TestJavaTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &JavaTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum should be present.
	if !strings.Contains(result, "public enum OrderStatus {") {
		t.Error("missing OrderStatus enum")
	}

	// Transition map should be present.
	if !strings.Contains(result, "public static final Map<OrderStatus, Set<OrderStatus>> ORDER_STATUS_TRANSITIONS = Map.ofEntries(") {
		t.Errorf("missing ORDER_STATUS_TRANSITIONS declaration, got:\n%s", result)
	}

	// Check Map/Set imports.
	if !strings.Contains(result, "import java.util.Map;") {
		t.Error("missing java.util.Map import")
	}
	if !strings.Contains(result, "import java.util.Set;") {
		t.Error("missing java.util.Set import")
	}
}

func TestKotlinTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &KotlinTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum class should be present.
	if !strings.Contains(result, "enum class OrderStatus(val value: String) {") {
		t.Error("missing OrderStatus enum class")
	}

	// Transition map should be present.
	if !strings.Contains(result, "val ORDER_STATUS_TRANSITIONS: Map<OrderStatus, Set<OrderStatus>> = mapOf(") {
		t.Errorf("missing ORDER_STATUS_TRANSITIONS declaration, got:\n%s", result)
	}
}

func TestZigTypesGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &ZigTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Enum constants should be present.
	if !strings.Contains(result, `pub const order_status_pending = "pending";`) {
		t.Error("missing order_status_pending constant")
	}

	// Transition map should be present.
	if !strings.Contains(result, "pub const order_status_transitions = struct {") {
		t.Errorf("missing order_status_transitions declaration, got:\n%s", result)
	}

	// Check specific entries.
	if !strings.Contains(result, `pub const pending = [_][]const u8{ "active", "cancelled" };`) {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

// --- Constants output tests ---

func TestGoConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &GoConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Transition map should use string keys/values.
	if !strings.Contains(result, "var OrderStatusTransitions = map[string][]string{") {
		t.Errorf("missing OrderStatusTransitions const map, got:\n%s", result)
	}

	if !strings.Contains(result, `"pending": {"active", "cancelled"}`) {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
	if !strings.Contains(result, `"active": {"cancelled", "shipped"}`) {
		t.Errorf("missing or incorrect active transitions, got:\n%s", result)
	}
}

func TestPythonConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &PythonConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	// Transition map should use string keys/values.
	if !strings.Contains(result, "TRANSITIONS_ORDER_STATUS: dict[str, list[str]]") {
		t.Errorf("missing TRANSITIONS_ORDER_STATUS declaration, got:\n%s", result)
	}

	if !strings.Contains(result, `"pending": ["active", "cancelled"]`) {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

func TestTSConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &TSConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	if !strings.Contains(result, "export const TRANSITIONS_ORDER_STATUS: Record<string, string[]>") {
		t.Errorf("missing TRANSITIONS_ORDER_STATUS declaration, got:\n%s", result)
	}

	if !strings.Contains(result, `"pending": ["active", "cancelled"]`) {
		t.Errorf("missing or incorrect pending transitions, got:\n%s", result)
	}
}

func TestJavaConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &JavaConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	if !strings.Contains(result, "public static final Map<String, Set<String>> TRANSITIONS_ORDER_STATUS = Map.ofEntries(") {
		t.Errorf("missing TRANSITIONS_ORDER_STATUS declaration, got:\n%s", result)
	}
}

func TestKotlinConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &KotlinConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	if !strings.Contains(result, "val TRANSITIONS_ORDER_STATUS: Map<String, Set<String>> = mapOf(") {
		t.Errorf("missing TRANSITIONS_ORDER_STATUS declaration, got:\n%s", result)
	}
}

func TestZigConstantsGenerator_TransitionMap(t *testing.T) {
	schema := smTestSchema()
	gen := &ZigConstantsGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)

	if !strings.Contains(result, "pub const transitions_order_status = struct {") {
		t.Errorf("missing transitions_order_status declaration, got:\n%s", result)
	}
}

// --- Edge cases ---

func TestTransitionMaps_NoStateMachines(t *testing.T) {
	// Schema with no SM types should produce no transition maps.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
			},
		},
	}

	gen := &GoTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)
	if strings.Contains(result, "Transitions") {
		t.Error("should not contain transition maps when no state machines")
	}
}

func TestTransitionMaps_EmptyTransitions(t *testing.T) {
	// SM type with states but no transitions should produce an empty map.
	schema := &model.Schema{
		Enums: []model.Enum{
			{Name: "simple_status", Values: []string{"on", "off"}},
		},
		StateMachineTransitions: []model.SMTransitionMap{
			{
				TypeName:    "simple_status",
				States:      []string{"on", "off"},
				Transitions: map[string][]string{},
			},
		},
		Tables: []model.Table{
			{
				Name: "devices",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
			},
		},
	}

	gen := &GoTypesGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	result := string(out)
	// Should have the map declaration but with no entries.
	if !strings.Contains(result, "var SimpleStatusTransitions = map[SimpleStatus][]SimpleStatus{") {
		t.Errorf("missing SimpleStatusTransitions declaration, got:\n%s", result)
	}
}
