package extregistry

// NewBuiltinRegistry returns a registry pre-loaded with common PostgreSQL extensions.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()

	r.Register(&Extension{
		Name:      "pgcrypto",
		Functions: []string{"gen_random_uuid", "crypt", "digest"},
	})

	r.Register(&Extension{
		Name:      "pg_trgm",
		Opclasses: []string{"gin_trgm_ops", "gist_trgm_ops"},
		Functions: []string{"similarity", "word_similarity", "strict_word_similarity"},
	})

	r.Register(&Extension{
		Name:      "btree_gin",
		Opclasses: []string{"int4_ops", "text_ops", "timestamp_ops"},
	})

	r.Register(&Extension{
		Name:      "btree_gist",
		Opclasses: []string{"gist_int4_ops", "gist_text_ops", "gist_timestamp_ops"},
	})

	r.Register(&Extension{
		Name:      "postgis",
		Types:     []string{"geometry", "geography"},
		Opclasses: []string{"gist_geometry_ops_2d", "gist_geography_ops"},
		Functions: []string{"ST_Distance", "ST_Within", "ST_Contains"},
	})

	r.Register(&Extension{
		Name:      "hstore",
		Types:     []string{"hstore"},
		Opclasses: []string{"gin_hstore_ops", "gist_hstore_ops"},
	})

	r.Register(&Extension{
		Name:      "pg_partman",
		Functions: []string{"create_parent", "run_maintenance_proc"},
	})

	r.Register(&Extension{
		Name:      "pg_cron",
		Functions: []string{"schedule", "unschedule"},
	})

	r.Register(&Extension{
		Name: "pg_stat_statements",
	})

	r.Register(&Extension{
		Name:      "uuid-ossp",
		Functions: []string{"uuid_generate_v4", "uuid_generate_v1"},
	})

	r.Register(&Extension{
		Name:  "citext",
		Types: []string{"citext"},
	})

	r.Register(&Extension{
		Name:      "ltree",
		Types:     []string{"ltree"},
		Opclasses: []string{"gist_ltree_ops"},
	})

	r.Register(&Extension{
		Name:      "intarray",
		Opclasses: []string{"gin__int_ops"},
	})

	return r
}
