package extregistry

// NewBuiltinRegistry returns a registry pre-loaded with common PostgreSQL extensions.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()

	// Core PostgreSQL built-in index methods and their storage parameters.
	r.Register(&Extension{
		Name: "core",
		IndexParams: map[string][]string{
			"btree": {"fillfactor", "deduplicate_items"},
			"gin":   {"fastupdate", "gin_pending_list_limit"},
			"gist":  {"fillfactor", "buffering"},
			"brin":  {"pages_per_range", "autosummarize"},
			"hash":  {"fillfactor"},
		},
	})

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
		Opclasses: []string{
			"int2_ops", "int4_ops", "int8_ops",
			"float4_ops", "float8_ops", "numeric_ops",
			"timestamp_ops", "timestamptz_ops", "time_ops", "timetz_ops",
			"date_ops", "interval_ops", "oid_ops", "money_ops",
			"char_ops", "varchar_ops", "text_ops", "bytea_ops",
			"bit_ops", "varbit_ops",
			"macaddr_ops", "macaddr8_ops", "inet_ops", "cidr_ops",
			"uuid_ops", "name_ops", "bool_ops", "bpchar_ops", "enum_ops",
		},
	})

	r.Register(&Extension{
		Name:      "btree_gist",
		Opclasses: []string{
			"gist_int2_ops", "gist_int4_ops", "gist_int8_ops",
			"gist_float4_ops", "gist_float8_ops", "gist_numeric_ops",
			"gist_timestamp_ops", "gist_timestamptz_ops", "gist_time_ops", "gist_timetz_ops",
			"gist_date_ops", "gist_interval_ops", "gist_oid_ops", "gist_money_ops",
			"gist_macaddr_ops", "gist_macaddr8_ops",
			"gist_uuid_ops", "gist_text_ops", "gist_bpchar_ops",
			"gist_inet_ops", "gist_cidr_ops", "gist_bool_ops", "gist_enum_ops",
		},
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
		Functions: []string{"partman.create_parent", "partman.run_maintenance_proc"},
	})

	r.Register(&Extension{
		Name:      "pg_cron",
		Functions: []string{"cron.schedule", "cron.unschedule"},
	})

	r.Register(&Extension{
		Name: "pg_stat_statements",
	})

	r.Register(&Extension{
		Name:      "uuid-ossp",
		Functions: []string{"uuid_generate_v4", "uuid_generate_v1"},
	})

	r.Register(&Extension{
		Name:      "pg_uuidv7",
		Functions: []string{"uuid_generate_v7"},
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

	r.Register(&Extension{
		Name:  "pgvector",
		Types: []string{"vector", "halfvec", "sparsevec"},
		Opclasses: []string{
			"vector_l2_ops", "vector_ip_ops", "vector_cosine_ops", "vector_l1_ops",
			"halfvec_l2_ops", "halfvec_ip_ops", "halfvec_cosine_ops",
			"sparsevec_l2_ops", "sparsevec_ip_ops", "sparsevec_cosine_ops",
			"bit_hamming_ops", "bit_jaccard_ops",
		},
		Functions:    []string{"l2_distance", "inner_product", "cosine_distance", "l1_distance"},
		IndexMethods: []string{"hnsw", "ivfflat"},
		IndexParams: map[string][]string{
			"hnsw":    {"m", "ef_construction"},
			"ivfflat": {"lists"},
		},
	})

	return r
}
