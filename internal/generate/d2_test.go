package generate

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

func TestGenerateD2TwoTables(t *testing.T) {
	schema := &model.Schema{
		Name: "blog",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "email", PGType: "text", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "title", PGType: "text", NotNull: true},
					{Name: "author_id", PGType: "uuid", NotNull: true},
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

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "children",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "parent_id", PGType: "uuid", NotNull: true},
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

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "b",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "a_id", PGType: "integer", NotNull: true},
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

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "user_id", PGType: "uuid", NotNull: true},
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

	out := GenerateD2(schema)

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
					{Name: "id", PGType: "integer", NotNull: true},
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

func TestGenerateSVGFormat(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "test",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
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
