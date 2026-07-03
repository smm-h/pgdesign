package codegen

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// --- Phase 20: Dual-backend conformance tests ---
//
// These tests verify at the CODEGEN level that PgBackend and InMemoryBackend
// produce structurally equivalent Python code: same public methods, matching
// protocol conformance, correct delegation patterns, and consistent SM transition
// coverage.

// conformanceSchema returns the qlTestSchema() which already exercises
// all relevant features: standard CRUD, append-only, SM transitions, FKs, uniques.
func conformanceSchema() (map[string][]byte, string) {
	schema := qlTestSchema()
	gen := &PythonQueryLayerGenerator{}
	files, _ := gen.GenerateFiles(schema)
	return files, string(files["protocols.py"])
}

// extractPublicMethods returns all "async def <name>(" methods from a Python class,
// starting from the class declaration until the next top-level class or EOF.
func extractPublicMethods(content, className string) []string {
	classMarker := "class " + className
	idx := strings.Index(content, classMarker)
	if idx < 0 {
		return nil
	}
	rest := content[idx:]

	// Find the end of this class: next top-level "class " at column 0.
	endIdx := -1
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip the class declaration itself
		}
		if strings.HasPrefix(line, "class ") {
			// Count characters up to this line.
			charCount := 0
			for j := 0; j < i; j++ {
				charCount += len(lines[j]) + 1
			}
			endIdx = charCount
			break
		}
	}

	var classBody string
	if endIdx > 0 {
		classBody = rest[:endIdx]
	} else {
		classBody = rest
	}

	// Extract all method names.
	re := regexp.MustCompile(`async def (\w+)\(`)
	matches := re.FindAllStringSubmatch(classBody, -1)
	var methods []string
	for _, m := range matches {
		methods = append(methods, m[1])
	}
	sort.Strings(methods)
	return methods
}

// TestConformance_SamePublicMethods verifies that PgBackend and InMemoryBackend
// have the exact same set of public methods.
func TestConformance_SamePublicMethods(t *testing.T) {
	files, _ := conformanceSchema()
	pgContent := string(files["pg_backend.py"])
	memContent := string(files["memory_backend.py"])

	pgMethods := extractPublicMethods(pgContent, "PgBackend:")
	memMethods := extractPublicMethods(memContent, "InMemoryBackend:")

	if len(pgMethods) == 0 {
		t.Fatal("PgBackend has no public methods")
	}
	if len(memMethods) == 0 {
		t.Fatal("InMemoryBackend has no public methods")
	}

	// Build sets for comparison.
	pgSet := make(map[string]bool)
	for _, m := range pgMethods {
		pgSet[m] = true
	}
	memSet := make(map[string]bool)
	for _, m := range memMethods {
		memSet[m] = true
	}

	// Methods in PgBackend but not InMemoryBackend.
	for _, m := range pgMethods {
		if !memSet[m] {
			t.Errorf("PgBackend has method %q but InMemoryBackend does not", m)
		}
	}

	// Methods in InMemoryBackend but not PgBackend.
	for _, m := range memMethods {
		if !pgSet[m] {
			t.Errorf("InMemoryBackend has method %q but PgBackend does not", m)
		}
	}

	// Verify count matches.
	if len(pgMethods) != len(memMethods) {
		t.Errorf("method count mismatch: PgBackend has %d, InMemoryBackend has %d",
			len(pgMethods), len(memMethods))
	}
}

// TestConformance_BothImplementBackendProtocol verifies that every method
// declared in the Backend protocol exists on both PgBackend and InMemoryBackend.
func TestConformance_BothImplementBackendProtocol(t *testing.T) {
	files, protocols := conformanceSchema()

	// Extract Backend protocol methods.
	backendSection := extractAfter(protocols, "class Backend(Protocol):")
	if backendSection == "" {
		t.Fatal("could not find Backend protocol")
	}

	re := regexp.MustCompile(`async def (\w+)\(`)
	matches := re.FindAllStringSubmatch(backendSection, -1)
	if len(matches) == 0 {
		t.Fatal("no methods found in Backend protocol")
	}

	var protocolMethods []string
	for _, m := range matches {
		protocolMethods = append(protocolMethods, m[1])
	}

	pgContent := string(files["pg_backend.py"])
	memContent := string(files["memory_backend.py"])

	for _, method := range protocolMethods {
		if !strings.Contains(pgContent, "async def "+method+"(") {
			t.Errorf("PgBackend missing Backend protocol method: %s", method)
		}
		if !strings.Contains(memContent, "async def "+method+"(") {
			t.Errorf("InMemoryBackend missing Backend protocol method: %s", method)
		}
	}
}

// TestConformance_PgUsesParameterizedSQL verifies that every per-table PG delegate
// uses parameterized SQL ($1, $2, ...) for all queries and never uses unsafe
// string interpolation patterns (%s, %d in SQL strings).
func TestConformance_PgUsesParameterizedSQL(t *testing.T) {
	files, _ := conformanceSchema()

	for name, data := range files {
		if !strings.HasPrefix(name, "_") || !strings.HasSuffix(name, "_pg.py") {
			continue
		}
		content := string(data)

		// Every PG delegate must have at least one parameterized query.
		if !strings.Contains(content, "$1") {
			t.Errorf("%s: no parameterized queries found (missing $1)", name)
		}

		// Scan SQL string literals for unsafe interpolation.
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			// The dynamic UPDATE SET clause uses f-strings safely (for column names, not values).
			if strings.Contains(trimmed, "f\"UPDATE") {
				continue
			}
			if strings.Contains(trimmed, "sql = \"") || strings.Contains(trimmed, "sql = f\"") {
				if strings.Contains(trimmed, "%s") || strings.Contains(trimmed, "%d") {
					t.Errorf("%s: unsafe string interpolation in SQL: %s", name, trimmed)
				}
			}
		}
	}
}

// TestConformance_InMemoryDelegatesToConstraintEngine verifies that every
// per-table InMemory delegate invokes ConstraintEngine for every write operation
// (create uses validate_insert, update uses validate_update, SM transitions
// use validate_update).
func TestConformance_InMemoryDelegatesToConstraintEngine(t *testing.T) {
	files, _ := conformanceSchema()

	// Tables we expect to have write operations.
	tables := []struct {
		name      string
		hasCreate bool
		hasUpdate bool
		hasDelete bool
		hasSM     bool
	}{
		{"users", true, true, true, false},
		{"orders", true, false, false, true}, // append-only: no update/delete
		{"order_items", true, true, true, false},
	}

	for _, tbl := range tables {
		fileName := "_" + tbl.name + "_mem.py"
		data, ok := files[fileName]
		if !ok {
			t.Errorf("missing file %s", fileName)
			continue
		}
		content := string(data)

		if tbl.hasCreate {
			createBody := extractMemMethodBody(content, "create_"+tbl.name)
			if createBody == "" {
				t.Errorf("%s: missing create_%s method", fileName, tbl.name)
			} else if !strings.Contains(createBody, "ConstraintEngine.validate_insert(") {
				t.Errorf("%s: create_%s does not delegate to ConstraintEngine.validate_insert",
					fileName, tbl.name)
			}
		}

		if tbl.hasUpdate {
			updateBody := extractMemMethodBody(content, "update_"+tbl.name)
			if updateBody == "" {
				t.Errorf("%s: missing update_%s method", fileName, tbl.name)
			} else if !strings.Contains(updateBody, "ConstraintEngine.validate_update(") {
				t.Errorf("%s: update_%s does not delegate to ConstraintEngine.validate_update",
					fileName, tbl.name)
			}
		}

		if tbl.hasDelete {
			deleteBody := extractMemMethodBody(content, "delete_"+tbl.name)
			if deleteBody == "" {
				t.Errorf("%s: missing delete_%s method", fileName, tbl.name)
			} else if !strings.Contains(deleteBody, "ConstraintEngine.process_delete(") {
				t.Errorf("%s: delete_%s does not delegate to ConstraintEngine.process_delete",
					fileName, tbl.name)
			}
		}

		if tbl.hasSM {
			// SM transition methods should use validate_update.
			smMethods := []string{"cancel_orders", "confirm_orders", "ship_orders", "suspend_orders"}
			for _, method := range smMethods {
				body := extractMemMethodBody(content, method)
				if body == "" {
					t.Errorf("%s: missing SM transition method %s", fileName, method)
				} else if !strings.Contains(body, "ConstraintEngine.validate_update(") {
					t.Errorf("%s: SM method %s does not delegate to ConstraintEngine.validate_update",
						fileName, method)
				}
			}
		}
	}
}

// TestConformance_SMTransitionMethodsMatchBetweenBackends verifies that state
// machine transition methods (e.g., cancel_orders, confirm_orders) exist on
// both backends with matching method signatures.
func TestConformance_SMTransitionMethodsMatchBetweenBackends(t *testing.T) {
	files, _ := conformanceSchema()
	pgContent := string(files["pg_backend.py"])
	memContent := string(files["memory_backend.py"])

	smMethods := []string{
		"cancel_orders",
		"confirm_orders",
		"ship_orders",
		"suspend_orders",
	}

	for _, method := range smMethods {
		pgSig := extractMethodSignature(pgContent, method)
		memSig := extractMethodSignature(memContent, method)

		if pgSig == "" {
			t.Errorf("PgBackend missing SM method: %s", method)
			continue
		}
		if memSig == "" {
			t.Errorf("InMemoryBackend missing SM method: %s", method)
			continue
		}

		// Signatures should match (same parameters and return type).
		if pgSig != memSig {
			t.Errorf("SM method %s signature mismatch:\n  PgBackend:       %s\n  InMemoryBackend: %s",
				method, pgSig, memSig)
		}
	}
}

// TestConformance_CascadeDeleteUsesALL_CONSTRAINTS verifies that every InMemory
// delete method references ALL_CONSTRAINTS for cascade processing.
func TestConformance_CascadeDeleteUsesALL_CONSTRAINTS(t *testing.T) {
	files, _ := conformanceSchema()

	// Tables that have delete methods (non-append-only).
	deleteTables := []string{"users", "order_items"}

	for _, tbl := range deleteTables {
		fileName := "_" + tbl + "_mem.py"
		data, ok := files[fileName]
		if !ok {
			t.Errorf("missing file %s", fileName)
			continue
		}
		content := string(data)

		deleteBody := extractMemMethodBody(content, "delete_"+tbl)
		if deleteBody == "" {
			t.Errorf("%s: missing delete_%s method", fileName, tbl)
			continue
		}

		// Must reference ALL_CONSTRAINTS (not just per-table constraints).
		if !strings.Contains(deleteBody, "ALL_CONSTRAINTS") {
			t.Errorf("%s: delete_%s does not reference ALL_CONSTRAINTS for cascade processing",
				fileName, tbl)
		}
	}

	// Also verify the import is present in each mem delegate file.
	for _, tbl := range []string{"users", "orders", "order_items"} {
		fileName := "_" + tbl + "_mem.py"
		content := string(files[fileName])
		if !strings.Contains(content, "from ._constraints import ALL_CONSTRAINTS") {
			t.Errorf("%s: missing ALL_CONSTRAINTS import", fileName)
		}
	}
}

// TestConformance_BothCompositesHaveIdenticalForwardingMethods verifies that
// the composite PgBackend and InMemoryBackend classes forward the exact same
// set of methods (including parameter names).
func TestConformance_BothCompositesHaveIdenticalForwardingMethods(t *testing.T) {
	files, _ := conformanceSchema()
	pgContent := string(files["pg_backend.py"])
	memContent := string(files["memory_backend.py"])

	pgMethods := extractPublicMethods(pgContent, "PgBackend:")
	memMethods := extractPublicMethods(memContent, "InMemoryBackend:")

	// Sort for deterministic comparison.
	sort.Strings(pgMethods)
	sort.Strings(memMethods)

	if len(pgMethods) != len(memMethods) {
		t.Fatalf("forwarding method count mismatch: PgBackend=%d, InMemoryBackend=%d",
			len(pgMethods), len(memMethods))
	}

	for i := range pgMethods {
		if pgMethods[i] != memMethods[i] {
			t.Errorf("forwarding method mismatch at index %d: PgBackend=%s, InMemoryBackend=%s",
				i, pgMethods[i], memMethods[i])
		}
	}

	// Verify that for each method, both backends have the same parameter signature.
	for _, method := range pgMethods {
		pgSig := extractMethodSignature(pgContent, method)
		memSig := extractMethodSignature(memContent, method)
		if pgSig != memSig {
			t.Errorf("method %s signature mismatch:\n  PgBackend:       %s\n  InMemoryBackend: %s",
				method, pgSig, memSig)
		}
	}
}

// TestConformance_PerTableDelegateMethodParity verifies that for each table,
// the PG delegate and InMemory delegate have the same set of methods.
func TestConformance_PerTableDelegateMethodParity(t *testing.T) {
	files, _ := conformanceSchema()

	tables := []struct {
		name     string
		pgClass  string
		memClass string
	}{
		{"users", "UsersPg:", "UsersMem:"},
		{"orders", "OrdersPg:", "OrdersMem:"},
		{"order_items", "OrderItemsPg:", "OrderItemsMem:"},
	}

	for _, tbl := range tables {
		pgFile := "_" + tbl.name + "_pg.py"
		memFile := "_" + tbl.name + "_mem.py"

		pgContent := string(files[pgFile])
		memContent := string(files[memFile])

		pgMethods := extractPublicMethods(pgContent, tbl.pgClass)
		memMethods := extractPublicMethods(memContent, tbl.memClass)

		pgSet := make(map[string]bool)
		for _, m := range pgMethods {
			pgSet[m] = true
		}
		memSet := make(map[string]bool)
		for _, m := range memMethods {
			memSet[m] = true
		}

		for _, m := range pgMethods {
			if !memSet[m] {
				t.Errorf("table %s: PG delegate has method %q but InMemory delegate does not",
					tbl.name, m)
			}
		}
		for _, m := range memMethods {
			if !pgSet[m] {
				t.Errorf("table %s: InMemory delegate has method %q but PG delegate does not",
					tbl.name, m)
			}
		}
	}
}

// TestConformance_WriterReaderProtocolCoverage verifies that both backends
// implement all methods from both the per-table Writer and Reader protocols.
func TestConformance_WriterReaderProtocolCoverage(t *testing.T) {
	files, protocols := conformanceSchema()

	// Extract per-table Writer and Reader protocol methods.
	type protoMethods struct {
		writer []string
		reader []string
	}
	tableProtos := map[string]protoMethods{
		"users":       {},
		"orders":      {},
		"order_items": {},
	}

	for tbl := range tableProtos {
		pascal := toPascalCase(tbl)
		pm := protoMethods{}

		writerSection := extractAfter(protocols, "class "+pascal+"Writer(Protocol):")
		if writerSection != "" {
			re := regexp.MustCompile(`async def (\w+)\(`)
			for _, m := range re.FindAllStringSubmatch(writerSection, -1) {
				// Stop at next class definition.
				methodOffset := strings.Index(writerSection, "async def "+m[1])
				prefix := writerSection[:methodOffset]
				if strings.Contains(prefix, "\nclass ") {
					break
				}
				pm.writer = append(pm.writer, m[1])
			}
		}

		readerSection := extractAfter(protocols, "class "+pascal+"Reader(Protocol):")
		if readerSection != "" {
			re := regexp.MustCompile(`async def (\w+)\(`)
			for _, m := range re.FindAllStringSubmatch(readerSection, -1) {
				methodOffset := strings.Index(readerSection, "async def "+m[1])
				prefix := readerSection[:methodOffset]
				if strings.Contains(prefix, "\nclass ") {
					break
				}
				pm.reader = append(pm.reader, m[1])
			}
		}

		tableProtos[tbl] = pm
	}

	pgContent := string(files["pg_backend.py"])
	memContent := string(files["memory_backend.py"])

	for tbl, pm := range tableProtos {
		allMethods := append(pm.writer, pm.reader...)
		for _, method := range allMethods {
			if !strings.Contains(pgContent, "async def "+method+"(") {
				t.Errorf("PgBackend missing %s protocol method: %s", tbl, method)
			}
			if !strings.Contains(memContent, "async def "+method+"(") {
				t.Errorf("InMemoryBackend missing %s protocol method: %s", tbl, method)
			}
		}
	}
}

// TestConformance_PgDelegateForwardingCorrectness verifies that PgBackend
// forwarding methods delegate to the correct per-table delegate attribute.
func TestConformance_PgDelegateForwardingCorrectness(t *testing.T) {
	files, _ := conformanceSchema()
	pgContent := string(files["pg_backend.py"])

	// Each table's methods should forward to self._<table>.
	expectations := map[string][]string{
		"_users":       {"create_users", "get_users", "update_users", "delete_users", "list_users"},
		"_orders":      {"create_orders", "list_orders", "cancel_orders", "confirm_orders"},
		"_order_items": {"create_order_items", "get_order_items", "list_order_items"},
	}

	for attr, methods := range expectations {
		for _, method := range methods {
			delegateCall := "self." + attr + "." + method + "("
			if !strings.Contains(pgContent, delegateCall) {
				t.Errorf("PgBackend: method %s does not delegate to self.%s", method, attr)
			}
		}
	}
}

// TestConformance_MemDelegateForwardingCorrectness verifies that InMemoryBackend
// forwarding methods delegate to the correct per-table delegate attribute.
func TestConformance_MemDelegateForwardingCorrectness(t *testing.T) {
	files, _ := conformanceSchema()
	memContent := string(files["memory_backend.py"])

	// Each table's methods should forward to self._<table>.
	expectations := map[string][]string{
		"_users":       {"create_users", "get_users", "update_users", "delete_users", "list_users"},
		"_orders":      {"create_orders", "list_orders", "cancel_orders", "confirm_orders"},
		"_order_items": {"create_order_items", "get_order_items", "list_order_items"},
	}

	for attr, methods := range expectations {
		for _, method := range methods {
			delegateCall := "self." + attr + "." + method + "("
			if !strings.Contains(memContent, delegateCall) {
				t.Errorf("InMemoryBackend: method %s does not delegate to self.%s", method, attr)
			}
		}
	}
}

// --- Helper ---

// extractMethodSignature extracts the "async def name(...) -> ReturnType:" line
// normalized to remove extra whitespace, for comparison between backends.
func extractMethodSignature(content, methodName string) string {
	marker := "async def " + methodName + "("
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx:]

	// Find closing ") ->" or "):" pattern.
	endIdx := strings.Index(rest, ":\n")
	if endIdx < 0 {
		return ""
	}

	sig := rest[:endIdx+1]
	// Normalize: collapse runs of whitespace, trim.
	sig = regexp.MustCompile(`\s+`).ReplaceAllString(sig, " ")
	return strings.TrimSpace(sig)
}
