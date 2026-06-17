package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

func TestGraphQLBasicTable(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "email", PGType: "varchar", NotNull: true},
					{Name: "age", PGType: "integer", NotNull: false},
					{Name: "active", PGType: "boolean", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	checks := []string{
		"scalar DateTime",
		"scalar JSON",
		"type Users {",
		"id: ID!",
		"name: String!",
		"email: String!",
		"active: Boolean!",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in output, got:\n%s", c, out)
		}
	}

	// age is nullable: must appear without trailing !
	if !strings.Contains(out, "age: Int\n") {
		t.Errorf("expected nullable age: Int (no !), got:\n%s", out)
	}
}

func TestGraphQLEnums(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Enums: []model.Enum{
			{Schema: "app", Name: "status", Values: []string{"active", "inactive", "banned"}},
		},
		Tables: []model.Table{
			{
				Name:   "accounts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "status", PGType: "status", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	checks := []string{
		"enum Status {",
		"  ACTIVE",
		"  INACTIVE",
		"  BANNED",
		"status: Status!",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in output, got:\n%s", c, out)
		}
	}
}

func TestGraphQLForeignKeys(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "user_id", PGType: "uuid", NotNull: true},
					{Name: "total", PGType: "numeric", NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_orders_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	// Orders type should have a FK relation field to Users (NOT NULL because user_id is NOT NULL).
	if !strings.Contains(out, "users: Users!") {
		t.Errorf("expected FK relation users: Users! on Orders, got:\n%s", out)
	}

	// Users type should have a reverse relation to Orders.
	if !strings.Contains(out, "orders: [Orders!]!") {
		t.Errorf("expected reverse relation orders: [Orders!]! on Users, got:\n%s", out)
	}
}

func TestGraphQLNullableFK(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "categories",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "products",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "category_id", PGType: "integer", NotNull: false},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_products_category", Columns: []string{"category_id"}, RefSchema: "app", RefTable: "categories", RefColumns: []string{"id"}, OnDelete: "SET NULL"},
				},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	// Nullable FK: categories field should not have trailing !
	if !strings.Contains(out, "categories: Categories\n") {
		t.Errorf("expected nullable FK categories: Categories (no !), got:\n%s", out)
	}
}

func TestGraphQLArrayColumns(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "documents",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "tags", PGType: "text", NotNull: true, Array: true},
					{Name: "scores", PGType: "float8", NotNull: false, Array: true},
				},
				PK: []string{"id"},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	// NOT NULL array
	if !strings.Contains(out, "tags: [String!]!") {
		t.Errorf("expected tags: [String!]! for NOT NULL array, got:\n%s", out)
	}

	// Nullable array (no trailing !)
	if !strings.Contains(out, "scores: [Float!]\n") {
		t.Errorf("expected scores: [Float!] (no trailing !) for nullable array, got:\n%s", out)
	}
}

func TestGraphQLAllTypes(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "all_types",
				Schema: "app",
				Columns: []model.Column{
					{Name: "pk_id", PGType: "uuid", NotNull: true},
					{Name: "int_col", PGType: "integer", NotNull: true},
					{Name: "bigint_col", PGType: "bigint", NotNull: true},
					{Name: "text_col", PGType: "text", NotNull: true},
					{Name: "bool_col", PGType: "boolean", NotNull: true},
					{Name: "uuid_col", PGType: "uuid", NotNull: true},
					{Name: "float_col", PGType: "float8", NotNull: true},
					{Name: "numeric_col", PGType: "numeric", NotNull: true},
					{Name: "ts_col", PGType: "timestamptz", NotNull: true},
					{Name: "json_col", PGType: "jsonb", NotNull: true},
					{Name: "bytea_col", PGType: "bytea", NotNull: true},
					{Name: "date_col", PGType: "date", NotNull: true},
					{Name: "smallint_col", PGType: "smallint", NotNull: true},
					{Name: "real_col", PGType: "real", NotNull: true},
				},
				PK: []string{"pk_id"},
			},
		},
	}

	out := mustGenerate(t, schema, Options{Format: "graphql"})

	checks := map[string]string{
		"pkId: ID!":         "PK uuid -> ID",
		"intCol: Int!":      "integer -> Int",
		"bigintCol: Int!":   "bigint -> Int",
		"textCol: String!":  "text -> String",
		"boolCol: Boolean!": "boolean -> Boolean",
		"uuidCol: String!":  "non-PK uuid -> String",
		"floatCol: Float!":  "float8 -> Float",
		"numericCol: Float!": "numeric -> Float",
		"tsCol: DateTime!":  "timestamptz -> DateTime",
		"jsonCol: JSON!":    "jsonb -> JSON",
		"byteaCol: String!": "bytea -> String",
		"dateCol: DateTime!": "date -> DateTime",
		"smallintCol: Int!": "smallint -> Int",
		"realCol: Float!":   "real -> Float",
	}
	for expected, desc := range checks {
		if !strings.Contains(out, expected) {
			t.Errorf("type mapping %s: expected %q in output, got:\n%s", desc, expected, out)
		}
	}
}

func TestGraphQLGoldenFile(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Enums: []model.Enum{
			{Schema: "app", Name: "user_role", Values: []string{"admin", "editor", "viewer"}},
			{Schema: "app", Name: "order_status", Values: []string{"pending", "shipped", "delivered"}},
		},
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "email", PGType: "varchar", NotNull: true},
					{Name: "role", PGType: "user_role", NotNull: true},
					{Name: "metadata", PGType: "jsonb", NotNull: false},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "user_id", PGType: "uuid", NotNull: true},
					{Name: "status", PGType: "order_status", NotNull: true},
					{Name: "total", PGType: "numeric", NotNull: true},
					{Name: "tags", PGType: "text", NotNull: true, Array: true},
					{Name: "notes", PGType: "text", NotNull: false},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_orders_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
			{
				Name:   "order_items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "order_id", PGType: "integer", NotNull: true},
					{Name: "product_name", PGType: "text", NotNull: true},
					{Name: "quantity", PGType: "integer", NotNull: true},
					{Name: "price", PGType: "numeric", NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_order_items_order", Columns: []string{"order_id"}, RefSchema: "app", RefTable: "orders", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	got := mustGenerate(t, schema, Options{Format: "graphql"})

	expectedPath := filepath.Join("testdata", "graphql_expected.graphql")
	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("cannot read expected file: %v", err)
	}
	expected := string(expectedBytes)

	if got != expected {
		t.Errorf("golden file mismatch.\n--- got ---\n%s\n--- expected ---\n%s", got, expected)
	}
}
