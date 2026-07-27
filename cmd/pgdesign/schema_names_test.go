package main

import (
	"reflect"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// TestModelSchemaNames verifies that introspection namespaces are derived from
// the BUILT model's table schema= values (defaulting to public), NOT from the
// config's schema FILE basenames. The rehearsal bug was that a multi-file
// project whose tables all live in public was introspected against namespaces
// named after the TOML filenames (trace/dispatch/auth), producing empty
// introspection and total false drift.
func TestModelSchemaNames(t *testing.T) {
	cases := []struct {
		name   string
		schema *model.Schema
		want   []string
	}{
		{
			name:   "empty model defaults to public",
			schema: &model.Schema{},
			want:   []string{"public"},
		},
		{
			name: "all tables in public (multi-file, empty schema.Name)",
			schema: &model.Schema{
				Tables: []model.Table{
					{Name: "spans", Schema: ""},
					{Name: "events", Schema: ""},
					{Name: "dispatch", Schema: "public"},
				},
			},
			want: []string{"public"},
		},
		{
			name: "distinct explicit schemas are sorted and deduplicated",
			schema: &model.Schema{
				Tables: []model.Table{
					{Name: "u", Schema: "auth"},
					{Name: "s", Schema: ""},
					{Name: "d", Schema: "auth"},
					{Name: "t", Schema: "app"},
				},
			},
			want: []string{"app", "auth", "public"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := modelSchemaNames(tc.schema)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("modelSchemaNames() = %v, want %v", got, tc.want)
			}
		})
	}
}
