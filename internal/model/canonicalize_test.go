package model

import (
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestCanonicalize_SortsUnsortedSchema mimics the introspect path: a schema
// assembled from structs (not from Build) with collections in arbitrary order.
// Canonicalize must alphabetize every map-sourced collection, topologically
// order the tables, and rebuild the derived structures — the exact postcondition
// introspected schemas rely on (Introspect calls Canonicalize before returning).
func TestCanonicalize_SortsUnsortedSchema(t *testing.T) {
	schema := &Schema{
		Name:       "shop",
		Extensions: []string{"pgcrypto", "btree_gist"},
		Enums: []Enum{
			{Name: "user_role", Values: []string{"admin", "customer"}},
			{Name: "order_status", Values: []string{"pending", "shipped"}},
		},
		Tables: []Table{
			{
				Name: "orders", Schema: "shop", Comment: "Orders.",
				Columns: []Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "user_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []FK{
					{Name: "fk_orders_user", Columns: []string{"user_id"}, RefSchema: "shop", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				Checks: []CheckConstraint{
					{Name: "ck_orders_z", Expr: "user_id IS NOT NULL"},
					{Name: "ck_orders_a", Expr: "id IS NOT NULL"},
				},
				Indexes: []Index{
					{Name: "idx_orders_z", Columns: []string{"user_id"}},
					{Name: "idx_orders_a", Columns: []string{"id"}},
				},
			},
			{
				Name: "users", Schema: "shop", Comment: "Users.",
				Columns: []Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	schema.Canonicalize()

	// Tables topo-ordered: users (referenced) before orders.
	if schema.Tables[0].Name != "users" || schema.Tables[1].Name != "orders" {
		t.Errorf("tables not dependency-ordered: %s, %s", schema.Tables[0].Name, schema.Tables[1].Name)
	}
	// Extensions and enums alphabetized.
	if !sort.StringsAreSorted(schema.Extensions) {
		t.Errorf("Extensions not sorted: %v", schema.Extensions)
	}
	if schema.Enums[0].Name != "order_status" || schema.Enums[1].Name != "user_role" {
		t.Errorf("Enums not sorted: %v", schema.Enums)
	}
	// enum VALUES must be left untouched (semantic order).
	if got := schema.Enums[1].Values; got[0] != "admin" || got[1] != "customer" {
		t.Errorf("enum values must not be reordered, got %v", got)
	}
	// orders collections alphabetized.
	orders := schema.TableByName("shop", "orders")
	if orders == nil {
		t.Fatal("orders not found via TablesByName (derived structure not rebuilt)")
	}
	if orders.Checks[0].Name != "ck_orders_a" || orders.Indexes[0].Name != "idx_orders_a" {
		t.Errorf("orders collections not alphabetized: checks=%v indexes=%v", orders.Checks, orders.Indexes)
	}
	// Derived FK graph rebuilt.
	if schema.FKGraph == nil || schema.FKGraph.FanIn["shop.users"] != 1 {
		t.Errorf("FKGraph not rebuilt: %+v", schema.FKGraph)
	}
}

// twoTableRawWithGroups builds a raw schema with a "users" table and a "posts"
// table whose FK references "users", plus groups isolating each table.
func twoTableRawWithGroups() *parse.RawSchema {
	return &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "users",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
				PK:      []string{"id"},
			},
			{
				Name:    "posts",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				PK:      []string{"id"},
				FKs: map[string]parse.RawFK{
					"fk_posts_user_id": {Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
		Groups: map[string][]string{
			"onlyusers": {"users"},
			"onlyposts": {"posts"},
		},
	}
}

// TestFilterByGroups_RebuildsFKGraph is the red test for the live filter bug:
// FilterByGroups historically copied the parent schema's FKGraph pointer
// verbatim, so a filtered schema carried stale edges pointing at (or from)
// tables that were filtered out.
func TestFilterByGroups_RebuildsFKGraph(t *testing.T) {
	schema, diags := Build(twoTableRawWithGroups(), testRegistry())
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Parent graph carries the posts->users edge.
	if schema.FKGraph.FanIn["public.users"] != 1 {
		t.Fatalf("precondition: parent FanIn[users] = %d, want 1", schema.FKGraph.FanIn["public.users"])
	}

	filtered := schema.FilterByGroups([]string{"onlyusers"})

	if filtered.FKGraph == schema.FKGraph {
		t.Fatalf("filtered schema shares the parent FKGraph pointer (stale graph)")
	}
	if got := filtered.FKGraph.FanIn["public.users"]; got != 0 {
		t.Errorf("filtered FanIn[users] = %d, want 0 (posts was filtered out)", got)
	}
	if got := len(filtered.FKGraph.Reverse["public.users"]); got != 0 {
		t.Errorf("filtered Reverse[users] has %d stale edges, want 0", got)
	}
}

// TestFilterBySource_RebuildsFKGraph is the analogous red test for the
// source-file filter path. Each table lives in its own source file so the
// filter can isolate one.
func TestFilterBySource_RebuildsFKGraph(t *testing.T) {
	usersRaw := &parse.RawSchema{
		Meta:       parse.RawMeta{Schema: "public"},
		SourceFile: "users.toml",
		Tables: []parse.RawTable{{
			Name:    "users",
			Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			PK:      []string{"id"},
		}},
	}
	postsRaw := &parse.RawSchema{
		Meta:       parse.RawMeta{Schema: "public"},
		SourceFile: "posts.toml",
		Tables: []parse.RawTable{{
			Name:    "posts",
			Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
			PK:      []string{"id"},
			FKs: map[string]parse.RawFK{
				"fk_posts_user_id": {Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			},
		}},
	}

	schema, diags := BuildMulti([]*parse.RawSchema{usersRaw, postsRaw}, testRegistry())
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	filtered := schema.FilterBySource([]string{"users.toml"})

	if filtered.FKGraph == schema.FKGraph {
		t.Fatalf("filtered schema shares the parent FKGraph pointer (stale graph)")
	}
	if got := filtered.FKGraph.FanIn["public.users"]; got != 0 {
		t.Errorf("filtered FanIn[users] = %d, want 0 (posts was filtered out)", got)
	}
}
