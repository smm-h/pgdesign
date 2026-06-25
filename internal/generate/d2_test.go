package generate

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"github.com/smm-h/pgdesign/internal/semtype"
)

func TestGenerateD2TwoTables(t *testing.T) {
	schema := &model.Schema{
		Name: "blog",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "created_at", PGType: typeinfo.MustParse("timestamptz"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "title", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "author_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_posts_author",
						Columns:    []string{"author_id"},
						RefSchema:  "blog",
						RefTable:   "users",
						RefColumns: []string{"id"},
						OnDelete:   "CASCADE",
					},
				},
			},
		},
	}

	out := GenerateD2(schema, nil)

	// Both table shapes must be present.
	if !strings.Contains(out, "users: {") {
		t.Errorf("expected users table shape, got:\n%s", out)
	}
	if !strings.Contains(out, "posts: {") {
		t.Errorf("expected posts table shape, got:\n%s", out)
	}

	// FK edge must be present.
	if !strings.Contains(out, "posts.author_id -> users.id") {
		t.Errorf("expected FK edge, got:\n%s", out)
	}
}

func TestGenerateD2SQLTableShape(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := GenerateD2(schema, nil)

	if !strings.Contains(out, "shape: sql_table") {
		t.Errorf("expected sql_table shape, got:\n%s", out)
	}
}

func TestGenerateD2FKEdgeLabelOnDelete(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "parents",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "children",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "parent_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_children_parent",
						Columns:    []string{"parent_id"},
						RefSchema:  "app",
						RefTable:   "parents",
						RefColumns: []string{"id"},
						OnDelete:   "SET NULL",
					},
				},
			},
		},
	}

	out := GenerateD2(schema, nil)

	// The edge label must include the ON DELETE action.
	if !strings.Contains(out, "children.parent_id -> parents.id: SET NULL") {
		t.Errorf("expected FK edge with SET NULL label, got:\n%s", out)
	}
}

func TestGenerateD2DefaultOnDelete(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "a",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "b",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
					{Name: "a_id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_b_a",
						Columns:    []string{"a_id"},
						RefSchema:  "app",
						RefTable:   "a",
						RefColumns: []string{"id"},
						OnDelete:   "", // empty = default NO ACTION
					},
				},
			},
		},
	}

	out := GenerateD2(schema, nil)

	// When OnDelete is empty, should default to "NO ACTION".
	if !strings.Contains(out, "b.a_id -> a.id: NO ACTION") {
		t.Errorf("expected FK edge with NO ACTION label, got:\n%s", out)
	}
}

func TestGenerateD2PrimaryKeyConstraint(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "things",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := GenerateD2(schema, nil)

	if !strings.Contains(out, "id: uuid {constraint: primary_key}") {
		t.Errorf("expected primary_key constraint on id, got:\n%s", out)
	}
	// Non-PK column should NOT have a constraint.
	if strings.Contains(out, "name: text {constraint:") {
		t.Errorf("name column should not have a constraint, got:\n%s", out)
	}
}

func TestGenerateD2ForeignKeyConstraint(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "user_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_posts_user",
						Columns:    []string{"user_id"},
						RefSchema:  "app",
						RefTable:   "users",
						RefColumns: []string{"id"},
						OnDelete:   "CASCADE",
					},
				},
			},
		},
	}

	out := GenerateD2(schema, nil)

	if !strings.Contains(out, "user_id: uuid {constraint: foreign_key}") {
		t.Errorf("expected foreign_key constraint on user_id, got:\n%s", out)
	}
}

func TestGenerateD2ViaGenerate(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "test",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "d2"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "shape: sql_table") {
		t.Errorf("Generate with format=d2 should produce D2 output, got:\n%s", out)
	}
}

func TestRenderSVG(t *testing.T) {
	d2Source := `users: {
  shape: sql_table
  id: uuid {constraint: primary_key}
  name: text
}

posts: {
  shape: sql_table
  id: uuid {constraint: primary_key}
  author_id: uuid {constraint: foreign_key}
}

posts.author_id -> users.id: CASCADE
`

	svg, err := RenderSVG(d2Source)
	if err != nil {
		t.Fatalf("RenderSVG failed: %v", err)
	}

	if len(svg) == 0 {
		t.Fatal("RenderSVG returned empty SVG")
	}

	svgStr := string(svg)
	if !strings.Contains(svgStr, "<svg") {
		t.Errorf("expected SVG output, got:\n%s", svgStr[:min(200, len(svgStr))])
	}
}

func TestGenerateD2_Views(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "active", PGType: typeinfo.MustParse("boolean"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		Views: []model.View{
			{
				Name:      "active_users",
				Schema:    "app",
				Query:     "SELECT id, name FROM users WHERE active = true",
				Comment:   "Active users only",
				DependsOn: []string{"users"},
			},
		},
	}

	out := GenerateD2(schema, nil)

	// View shape must be a rectangle, not sql_table.
	if !strings.Contains(out, "active_users: {") {
		t.Errorf("expected active_users view shape, got:\n%s", out)
	}
	if !strings.Contains(out, "shape: rectangle") {
		t.Errorf("expected rectangle shape for view, got:\n%s", out)
	}
	if !strings.Contains(out, `label: "<<view>>\nactive_users"`) {
		t.Errorf("expected view label, got:\n%s", out)
	}
	if !strings.Contains(out, `style.fill: "#e8f4fd"`) {
		t.Errorf("expected view fill style, got:\n%s", out)
	}

	// Edge from view to dependency table.
	if !strings.Contains(out, "active_users -> users") {
		t.Errorf("expected edge from view to dependency table, got:\n%s", out)
	}
}

func TestGenerateSVGFormat(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "test",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "svg"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "<svg") {
		t.Errorf("Generate with format=svg should produce SVG output, got prefix:\n%s", out[:min(200, len(out))])
	}
}

func TestGenerateD2_StateMachine(t *testing.T) {
	reg := semtype.NewRegistry()
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{
		{
			Name: "order_status",
			Kind: "state_machine",
			States: []semtype.UserSMState{
				{Name: "pending"},
				{Name: "active"},
				{Name: "closed", Terminal: true},
			},
			Transitions: []semtype.UserSMTransition{
				{Name: "activate", From: []string{"pending"}, To: "active"},
				{Name: "close", From: []string{"active"}, To: "closed"},
			},
			InitialState:   "pending",
			EnforceTrigger: boolPtr(true),
		},
	})
	if diags.HasErrors() {
		t.Fatalf("LoadUserTypes errors: %v", diags)
	}

	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "status", PGType: typeinfo.MustParse("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				},
				PK: []string{"id"},
			},
		},
	}

	out := GenerateD2(schema, reg)

	// Check the SM container.
	if !strings.Contains(out, "order_status: {") {
		t.Errorf("expected order_status container, got:\n%s", out)
	}
	if !strings.Contains(out, `label: "<<state machine>>\norder_status"`) {
		t.Errorf("expected state machine label, got:\n%s", out)
	}

	// Check states.
	if !strings.Contains(out, "pending: {") {
		t.Errorf("expected pending state, got:\n%s", out)
	}
	if !strings.Contains(out, "shape: oval") {
		t.Errorf("expected oval shape for states, got:\n%s", out)
	}

	// Check initial state has bold.
	if !strings.Contains(out, "style.bold: true") {
		t.Errorf("expected bold style for initial state, got:\n%s", out)
	}

	// Check terminal state has thick stroke.
	if !strings.Contains(out, "style.stroke-width: 3") {
		t.Errorf("expected thick stroke for terminal state, got:\n%s", out)
	}

	// Check transitions.
	if !strings.Contains(out, "pending -> active: activate") {
		t.Errorf("expected activate transition edge, got:\n%s", out)
	}
	if !strings.Contains(out, "active -> closed: close") {
		t.Errorf("expected close transition edge, got:\n%s", out)
	}
}

func TestGenerateD2_NilRegistry(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	// Should not panic with nil registry.
	out := GenerateD2(schema, nil)
	if !strings.Contains(out, "items: {") {
		t.Errorf("expected items table, got:\n%s", out)
	}
}
