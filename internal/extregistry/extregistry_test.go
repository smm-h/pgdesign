package extregistry

import "testing"

func TestRequiredExtension_GinTrgmOps(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtension("gin_trgm_ops")
	if !ok {
		t.Fatal("expected gin_trgm_ops to be found")
	}
	if ext != "pg_trgm" {
		t.Fatalf("expected pg_trgm, got %s", ext)
	}
}

func TestRequiredExtensionForType_Geometry(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtensionForType("geometry")
	if !ok {
		t.Fatal("expected geometry to be found")
	}
	if ext != "postgis" {
		t.Fatalf("expected postgis, got %s", ext)
	}
}

func TestRequiredExtensionForFunction_GenRandomUUID(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtensionForFunction("gen_random_uuid")
	if !ok {
		t.Fatal("expected gen_random_uuid to be found")
	}
	if ext != "pgcrypto" {
		t.Fatalf("expected pgcrypto, got %s", ext)
	}
}

func TestRequiredExtensionForFunction_UuidGenerateV7(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtensionForFunction("uuid_generate_v7")
	if !ok {
		t.Fatal("expected uuid_generate_v7 to be found")
	}
	if ext != "pg_uuidv7" {
		t.Fatalf("expected pg_uuidv7, got %s", ext)
	}
}

func TestRequiredExtension_Unknown(t *testing.T) {
	r := NewBuiltinRegistry()
	_, ok := r.RequiredExtension("nonexistent_ops")
	if ok {
		t.Fatal("expected unknown opclass to return false")
	}
}

func TestRequiredExtension_BtreeGinAllOpclasses(t *testing.T) {
	r := NewBuiltinRegistry()
	opclasses := []string{
		"int2_ops", "int4_ops", "int8_ops",
		"float4_ops", "float8_ops", "numeric_ops",
		"timestamp_ops", "timestamptz_ops", "time_ops", "timetz_ops",
		"date_ops", "interval_ops", "oid_ops", "money_ops",
		"char_ops", "varchar_ops", "text_ops", "bytea_ops",
		"bit_ops", "varbit_ops",
		"macaddr_ops", "macaddr8_ops", "inet_ops", "cidr_ops",
		"uuid_ops", "name_ops", "bool_ops", "bpchar_ops", "enum_ops",
	}
	for _, oc := range opclasses {
		ext, ok := r.RequiredExtension(oc)
		if !ok {
			t.Errorf("expected opclass %s to be found for btree_gin", oc)
			continue
		}
		if ext != "btree_gin" {
			t.Errorf("expected btree_gin for opclass %s, got %s", oc, ext)
		}
	}
}

func TestRequiredExtension_BtreeGistAllOpclasses(t *testing.T) {
	r := NewBuiltinRegistry()
	opclasses := []string{
		"gist_int2_ops", "gist_int4_ops", "gist_int8_ops",
		"gist_float4_ops", "gist_float8_ops", "gist_numeric_ops",
		"gist_timestamp_ops", "gist_timestamptz_ops", "gist_time_ops", "gist_timetz_ops",
		"gist_date_ops", "gist_interval_ops", "gist_oid_ops", "gist_money_ops",
		"gist_macaddr_ops", "gist_macaddr8_ops",
		"gist_uuid_ops", "gist_text_ops", "gist_bpchar_ops",
		"gist_inet_ops", "gist_cidr_ops", "gist_bool_ops", "gist_enum_ops",
	}
	for _, oc := range opclasses {
		ext, ok := r.RequiredExtension(oc)
		if !ok {
			t.Errorf("expected opclass %s to be found for btree_gist", oc)
			continue
		}
		if ext != "btree_gist" {
			t.Errorf("expected btree_gist for opclass %s, got %s", oc, ext)
		}
	}
}

func TestRequiredExtensionForFunction_SchemaQualified(t *testing.T) {
	r := NewBuiltinRegistry()

	// pg_partman functions are schema-qualified with partman.
	for _, fn := range []string{"partman.create_parent", "partman.run_maintenance_proc"} {
		ext, ok := r.RequiredExtensionForFunction(fn)
		if !ok {
			t.Errorf("expected function %s to be found for pg_partman", fn)
			continue
		}
		if ext != "pg_partman" {
			t.Errorf("expected pg_partman for function %s, got %s", fn, ext)
		}
	}

	// pg_cron functions are schema-qualified with cron.
	for _, fn := range []string{"cron.schedule", "cron.unschedule"} {
		ext, ok := r.RequiredExtensionForFunction(fn)
		if !ok {
			t.Errorf("expected function %s to be found for pg_cron", fn)
			continue
		}
		if ext != "pg_cron" {
			t.Errorf("expected pg_cron for function %s, got %s", fn, ext)
		}
	}
}

func TestLoadUserExtensions(t *testing.T) {
	r := NewBuiltinRegistry()
	r.LoadUserExtensions([]UserExtension{
		{
			Name:      "my_custom_ext",
			Types:     []string{"my_type"},
			Opclasses: []string{"my_ops"},
			Functions: []string{"my_func"},
		},
	})

	ext, ok := r.RequiredExtension("my_ops")
	if !ok {
		t.Fatal("expected my_ops to be found after loading user extension")
	}
	if ext != "my_custom_ext" {
		t.Fatalf("expected my_custom_ext, got %s", ext)
	}

	ext, ok = r.RequiredExtensionForType("my_type")
	if !ok {
		t.Fatal("expected my_type to be found after loading user extension")
	}
	if ext != "my_custom_ext" {
		t.Fatalf("expected my_custom_ext, got %s", ext)
	}

	ext, ok = r.RequiredExtensionForFunction("my_func")
	if !ok {
		t.Fatal("expected my_func to be found after loading user extension")
	}
	if ext != "my_custom_ext" {
		t.Fatalf("expected my_custom_ext, got %s", ext)
	}
}

func TestRequiredExtensionForMethod_Hnsw(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtensionForMethod("hnsw")
	if !ok {
		t.Fatal("expected hnsw to be found")
	}
	if ext != "pgvector" {
		t.Fatalf("expected pgvector, got %s", ext)
	}
}

func TestRequiredExtensionForMethod_Ivfflat(t *testing.T) {
	r := NewBuiltinRegistry()
	ext, ok := r.RequiredExtensionForMethod("ivfflat")
	if !ok {
		t.Fatal("expected ivfflat to be found")
	}
	if ext != "pgvector" {
		t.Fatalf("expected pgvector, got %s", ext)
	}
}

func TestRequiredExtensionForMethod_BuiltinBtree(t *testing.T) {
	r := NewBuiltinRegistry()
	_, ok := r.RequiredExtensionForMethod("btree")
	if ok {
		t.Fatal("expected btree (built-in method) to return false")
	}
}

func TestRequiredExtensionForMethod_Unknown(t *testing.T) {
	r := NewBuiltinRegistry()
	_, ok := r.RequiredExtensionForMethod("unknown")
	if ok {
		t.Fatal("expected unknown method to return false")
	}
}

func TestPgvectorBuiltin(t *testing.T) {
	r := NewBuiltinRegistry()

	// Types
	for _, typ := range []string{"vector", "halfvec", "sparsevec"} {
		ext, ok := r.RequiredExtensionForType(typ)
		if !ok {
			t.Errorf("type %s not found", typ)
			continue
		}
		if ext != "pgvector" {
			t.Errorf("type %s: expected pgvector, got %s", typ, ext)
		}
	}

	// Opclasses
	opclasses := []string{
		"vector_l2_ops", "vector_ip_ops", "vector_cosine_ops", "vector_l1_ops",
		"halfvec_l2_ops", "halfvec_ip_ops", "halfvec_cosine_ops",
		"sparsevec_l2_ops", "sparsevec_ip_ops", "sparsevec_cosine_ops",
		"bit_hamming_ops", "bit_jaccard_ops",
	}
	for _, oc := range opclasses {
		ext, ok := r.RequiredExtension(oc)
		if !ok {
			t.Errorf("opclass %s not found", oc)
			continue
		}
		if ext != "pgvector" {
			t.Errorf("opclass %s: expected pgvector, got %s", oc, ext)
		}
	}

	// Functions
	for _, fn := range []string{"l2_distance", "inner_product", "cosine_distance", "l1_distance"} {
		ext, ok := r.RequiredExtensionForFunction(fn)
		if !ok {
			t.Errorf("function %s not found", fn)
			continue
		}
		if ext != "pgvector" {
			t.Errorf("function %s: expected pgvector, got %s", fn, ext)
		}
	}

	// Index methods
	for _, m := range []string{"hnsw", "ivfflat"} {
		ext, ok := r.RequiredExtensionForMethod(m)
		if !ok {
			t.Errorf("index method %s not found", m)
			continue
		}
		if ext != "pgvector" {
			t.Errorf("index method %s: expected pgvector, got %s", m, ext)
		}
	}
}

func TestValidIndexParams_Btree(t *testing.T) {
	r := NewBuiltinRegistry()
	params, ok := r.ValidIndexParams("btree")
	if !ok {
		t.Fatal("expected btree to have valid params")
	}
	found := false
	for _, p := range params {
		if p == "fillfactor" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fillfactor in btree params, got %v", params)
	}
}

func TestValidIndexParams_Hnsw(t *testing.T) {
	r := NewBuiltinRegistry()
	params, ok := r.ValidIndexParams("hnsw")
	if !ok {
		t.Fatal("expected hnsw to have valid params")
	}
	foundM := false
	foundEf := false
	for _, p := range params {
		if p == "m" {
			foundM = true
		}
		if p == "ef_construction" {
			foundEf = true
		}
	}
	if !foundM || !foundEf {
		t.Errorf("expected m and ef_construction in hnsw params, got %v", params)
	}
}

func TestValidIndexParams_Unknown(t *testing.T) {
	r := NewBuiltinRegistry()
	_, ok := r.ValidIndexParams("nonexistent")
	if ok {
		t.Fatal("expected unknown method to return false")
	}
}

func TestLoadUserExtensions_IndexMethods(t *testing.T) {
	r := NewBuiltinRegistry()
	r.LoadUserExtensions([]UserExtension{
		{
			Name:         "my_ext",
			IndexMethods: []string{"my_method"},
		},
	})
	ext, ok := r.RequiredExtensionForMethod("my_method")
	if !ok {
		t.Fatal("expected my_method to be found after loading user extension")
	}
	if ext != "my_ext" {
		t.Fatalf("expected my_ext, got %s", ext)
	}
}

func TestResolveDDLName(t *testing.T) {
	r := NewBuiltinRegistry()

	// Tier 1: extension found with explicit DDLName (pgvector -> "vector")
	if got := r.ResolveDDLName("pgvector"); got != "vector" {
		t.Errorf("tier 1: expected \"vector\", got %q", got)
	}

	// Tier 2: extension found without DDLName (pg_trgm -> "pg_trgm")
	if got := r.ResolveDDLName("pg_trgm"); got != "pg_trgm" {
		t.Errorf("tier 2: expected \"pg_trgm\", got %q", got)
	}

	// Tier 3: extension not found (user-defined, passthrough)
	if got := r.ResolveDDLName("my_unknown_ext"); got != "my_unknown_ext" {
		t.Errorf("tier 3: expected \"my_unknown_ext\", got %q", got)
	}
}
