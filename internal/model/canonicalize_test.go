package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/parse"
)

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
	if schema.FKGraph.FanIn["users"] != 1 {
		t.Fatalf("precondition: parent FanIn[users] = %d, want 1", schema.FKGraph.FanIn["users"])
	}

	filtered := schema.FilterByGroups([]string{"onlyusers"})

	if filtered.FKGraph == schema.FKGraph {
		t.Fatalf("filtered schema shares the parent FKGraph pointer (stale graph)")
	}
	if got := filtered.FKGraph.FanIn["users"]; got != 0 {
		t.Errorf("filtered FanIn[users] = %d, want 0 (posts was filtered out)", got)
	}
	if got := len(filtered.FKGraph.Reverse["users"]); got != 0 {
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
	if got := filtered.FKGraph.FanIn["users"]; got != 0 {
		t.Errorf("filtered FanIn[users] = %d, want 0 (posts was filtered out)", got)
	}
}
