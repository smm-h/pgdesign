# Simplify Java/Kotlin JSON parsing in testdb wrapper templates

## Problem

The Java and Kotlin wrapper templates each contain ~60 lines of hand-rolled JSON parsing code (`parseJsonStringArray` + `extractStatementsArray`). These parse the `.split.json` file (a JSON object with a `statements` string array). The parsers handle escape sequences manually including `\uXXXX` but lack surrogate pair support. The code is nearly identical between the two templates — a maintenance burden.

All 4 other templates use their language's standard library JSON parser: Go (`encoding/json`), Python (`json`), TypeScript (`JSON.parse`), Zig (`std.json`). Java has no stdlib JSON parser before Jakarta EE.

## Options

### 1. Leave as-is

- **Pros:** Working, tested by conformance suite, no new dependencies for consumers
- **Cons:** 120+ lines of duplicated hand-rolled parsing across two templates, maintenance burden, missing surrogate pair support
- **Effort:** None

### 2. Simplify the parser

Reduce the parser to handle only what `.split.json` actually contains: no nested objects, no nested arrays, only string values. The `extractStatementsArray` + `parseJsonStringArray` two-phase approach could be replaced by a single-pass parser that only handles the specific format.

- **Pros:** Less code, still no external dependency
- **Cons:** Still hand-rolled, still duplicated, still needs escape handling
- **Effort:** Small

### 3. Use Jackson or Gson

Add a dependency on Jackson (`com.fasterxml.jackson.core:jackson-databind`) or Gson (`com.google.code.gson:gson`) to the generated wrapper. Both are widely used and handle all JSON correctly.

- **Pros:** Correct, well-tested, handles all edge cases
- **Cons:** Adds a dependency to every consumer's test classpath. Some consumers may have version conflicts. Makes the generated wrapper not standalone.
- **Effort:** Small

### 4. Use javax.json (Jakarta JSON Processing)

Add a dependency on `jakarta.json:jakarta.json-api` + a provider like Eclipse Parsson. Part of the Jakarta EE standard.

- **Pros:** Standard API, correct, maintained
- **Cons:** Two dependencies (API + implementation), less common than Jackson/Gson, same "adds dependencies" concern
- **Effort:** Small

### 5. Embed DDL as inline string array literals

At `testdb init` time, instead of writing a wrapper that reads `.split.json` at runtime, embed the DDL statements directly as Java/Kotlin string array literals in the generated wrapper. The wrapper would have `private static final String[] STATEMENTS = {...}` instead of file-reading + JSON parsing code.

- **Pros:** Eliminates JSON parsing entirely. No external dependency. Simpler generated code. Faster test startup (no file I/O).
- **Cons:** Wrapper must be regenerated when DDL changes (currently it reads the file, so DDL changes don't require regeneration). Statement strings with special characters (backslashes, quotes) need careful escaping in the generated source. Larger generated files for big schemas.
- **Effort:** Medium (changes the architecture of testdb init, not just the template)

## Recommendation

Option 5 (inline literals) is the most architecturally correct — it eliminates the problem rather than mitigating it. Option 3 (Jackson/Gson) is the pragmatic choice if inline embedding is too much scope. Option 1 (leave as-is) is acceptable since the conformance test validates correctness.
