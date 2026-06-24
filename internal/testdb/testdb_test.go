package testdb

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGenerateName_Format(t *testing.T) {
	name := GenerateName("myapp")
	if !nameRegex.MatchString(name) {
		t.Fatalf("generated name %q does not match expected format", name)
	}
}

func TestGenerateName_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := GenerateName("myapp")
		if seen[name] {
			t.Fatalf("duplicate name generated: %s", name)
		}
		seen[name] = true
	}
}

func TestGenerateName_Length(t *testing.T) {
	base := strings.Repeat("a", 50)
	name := GenerateName(base)
	if len(name) > MaxNameLen {
		t.Fatalf("name length %d exceeds max %d: %s", len(name), MaxNameLen, name)
	}
}

func TestGenerateName_TruncatesLongBase(t *testing.T) {
	base := strings.Repeat("x", 60)
	name := GenerateName(base)
	if len(name) > MaxNameLen {
		t.Fatalf("name length %d exceeds max %d: %s", len(name), MaxNameLen, name)
	}
	// The base should have been truncated: the full 60-char base would produce
	// a name of 60 + 25 = 85 bytes. After truncation, the base portion should
	// be MaxNameLen - SuffixLen = 38 bytes.
	baseName, _, _, ok := ParseName(name)
	if !ok {
		t.Fatalf("generated name %q does not parse", name)
	}
	if len(baseName) != MaxNameLen-SuffixLen {
		t.Fatalf("expected truncated base length %d, got %d", MaxNameLen-SuffixLen, len(baseName))
	}
}

func TestGenerateName_UTF8SafeTruncation(t *testing.T) {
	// Build a base name that places a multi-byte rune right at the truncation
	// boundary. MaxNameLen - SuffixLen = 38. Use 37 ASCII chars + a 3-byte
	// rune (e.g., U+4E16 = '世', 3 bytes in UTF-8). The full base is 40 bytes,
	// so truncation to 38 bytes must not split the 3-byte rune.
	base := strings.Repeat("a", 37) + "世" // 37 + 3 = 40 bytes
	name := GenerateName(base)
	if len(name) > MaxNameLen {
		t.Fatalf("name length %d exceeds max %d: %s", len(name), MaxNameLen, name)
	}
	baseName, _, _, ok := ParseName(name)
	if !ok {
		t.Fatalf("generated name %q does not parse", name)
	}
	// The rune '世' at byte 37 occupies bytes 37-39. Truncating to 38 bytes
	// would split it, so the truncator should back up to byte 37.
	if len(baseName) > MaxNameLen-SuffixLen {
		t.Fatalf("truncated base %q exceeds max base length", baseName)
	}
	// Verify no broken runes.
	for i, r := range baseName {
		if r == '�' {
			t.Fatalf("broken rune at byte %d in base %q", i, baseName)
		}
	}
}

func TestParseName_Valid(t *testing.T) {
	ts := time.Now().Unix()
	name := "mydb_test_1234567890_abcd1234"
	baseName, created, random, ok := ParseName(name)
	if !ok {
		t.Fatalf("expected parse to succeed for %q", name)
	}
	if baseName != "mydb" {
		t.Fatalf("expected base %q, got %q", "mydb", baseName)
	}
	_ = ts
	if created != time.Unix(1234567890, 0) {
		t.Fatalf("expected timestamp 1234567890, got %d", created.Unix())
	}
	if random != "abcd1234" {
		t.Fatalf("expected random %q, got %q", "abcd1234", random)
	}
}

func TestParseName_Invalid(t *testing.T) {
	cases := []string{
		"mydb",                          // no _test_ segment
		"mydb_test_123_abcd1234",        // timestamp too short
		"mydb_test_1234567890_abc",      // random too short
		"mydb_test_1234567890_abcd12345", // random too long
		"mydb_test_1234567890_ABCD1234", // uppercase in random
		"",                              // empty
	}
	for _, c := range cases {
		_, _, _, ok := ParseName(c)
		if ok {
			t.Errorf("expected parse to fail for %q", c)
		}
	}
}

func TestParseName_RoundTrip(t *testing.T) {
	base := "testapp"
	before := time.Now()
	name := GenerateName(base)
	after := time.Now()

	baseName, created, random, ok := ParseName(name)
	if !ok {
		t.Fatalf("generated name %q does not parse", name)
	}
	if baseName != base {
		t.Fatalf("expected base %q, got %q", base, baseName)
	}
	if created.Before(before.Truncate(time.Second)) || created.After(after.Add(time.Second)) {
		t.Fatalf("timestamp %v outside expected range [%v, %v]", created, before, after)
	}
	if len(random) != RandLen {
		t.Fatalf("expected random length %d, got %d", RandLen, len(random))
	}
}

func TestCreateOptionsValidate(t *testing.T) {
	// Neither set: valid.
	if err := (CreateOptions{}).Validate(); err != nil {
		t.Fatalf("expected no error for empty options, got: %v", err)
	}

	// Only DDL set: valid.
	if err := (CreateOptions{DDL: strings.NewReader("CREATE TABLE t(id int);")}).Validate(); err != nil {
		t.Fatalf("expected no error for DDL-only options, got: %v", err)
	}

	// Only TemplateDB set: error (not yet supported).
	if err := (CreateOptions{TemplateDB: "tmpl"}).Validate(); err == nil {
		t.Fatal("expected error for TemplateDB-only options")
	}

	// Both set: error (mutually exclusive).
	opts := CreateOptions{
		DDL:        strings.NewReader("SELECT 1;"),
		TemplateDB: "tmpl",
	}
	if err := opts.Validate(); err == nil {
		t.Fatal("expected error for DDL + TemplateDB")
	}
}

func TestSkipIfNoPostgres(t *testing.T) {
	SkipIfNoPostgres(t)
	// If we reach here, PostgreSQL is available.
}

func TestDrop_InvalidName(t *testing.T) {
	// Manager with a dummy maintenance URL — we never actually connect.
	m := &Manager{maintenanceURL: "postgres://localhost:5432/postgres"}

	cases := []struct {
		name string
		desc string
	}{
		{"production_db", "plain name"},
		{"my_app", "underscore but wrong format"},
		{"", "empty string"},
		{"my_test_db", "contains 'test' but wrong format"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			db := &EphemeralDB{Name: tc.name}
			err := m.Drop(context.Background(), db)
			if err == nil {
				t.Fatalf("expected error for name %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "refusing to drop") {
				t.Fatalf("expected 'refusing to drop' in error, got: %v", err)
			}
		})
	}
}

func TestDrop_ValidName(t *testing.T) {
	// A validly-formatted name should pass the guard and fail later at pgx.Connect.
	m := &Manager{maintenanceURL: "postgres://localhost:5432/postgres"}
	db := &EphemeralDB{Name: "mydb_test_1234567890_abcd1234"}
	err := m.Drop(context.Background(), db)
	if err == nil {
		// Unexpected: there's no live DB, so we expect a connection error.
		t.Fatal("expected an error (no live DB), got nil")
	}
	if strings.Contains(err.Error(), "refusing to drop") {
		t.Fatalf("valid name should not trigger the guard, got: %v", err)
	}
}

func TestDropByName_InvalidName(t *testing.T) {
	m := &Manager{maintenanceURL: "postgres://localhost:5432/postgres"}
	err := m.DropByName(context.Background(), "production_db")
	if err == nil {
		t.Fatal("expected error for invalid name, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to drop") {
		t.Fatalf("expected 'refusing to drop' in error, got: %v", err)
	}
}
