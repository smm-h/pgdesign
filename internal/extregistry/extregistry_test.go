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

func TestRequiredExtension_Unknown(t *testing.T) {
	r := NewBuiltinRegistry()
	_, ok := r.RequiredExtension("nonexistent_ops")
	if ok {
		t.Fatal("expected unknown opclass to return false")
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
