# IndexMethods validation rule and array codegen for non-Go languages

Two gaps remain from the v0.10.0 feature push.

## IndexMethods validation is dead code

The plumbing for IndexMethods validation is complete end-to-end: `Extension.IndexMethods` is populated in `internal/extregistry/builtins.go` (pgvector registers `["hnsw", "ivfflat"]`), config parsing flows user extensions through `internal/config/config.go`, and `RequiredExtensionForMethod()` in `internal/extregistry/extregistry.go` performs the reverse lookup.

However, no validation rule calls `RequiredExtensionForMethod()`. Using `method = "hnsw"` on an index without declaring the pgvector extension produces no error. Compare with E214, which correctly validates opclasses via `RequiredExtension(opclass)` in `internal/validate/validate.go` — the analogous rule for index methods was never implemented.

The fix: add an E-code rule in `internal/validate/validate.go` (adjacent to `checkOpclassMissingExtension`) that checks `index.Method` against `config.ExtRegistry.RequiredExtensionForMethod(method)` and errors when the required extension is undeclared. The same pattern should cover `RequiredExtensionForType()` and `RequiredExtensionForFunction()`, which are also implemented but never consumed.

## Array column codegen only supports Go

The `--mode types` codegen generates correct array types for Go (`[]string`, `*[]int32`, etc.) in `internal/codegen/go_types.go`. The other 5 languages (Python, TypeScript, Java, Kotlin, Zig) do not have a `--mode types` implementation at all — the flag is rejected for non-Go languages at `cmd/pgdesign/handlers_codegen.go`.

When `--mode types` is extended to other languages, array columns should produce:
- Python: `list[str]`, `list[int]`, `Optional[list[str]]`
- TypeScript: `string[]`, `number[]`, `string[] | null`
- Java: `List<String>`, `List<Integer>`
- Kotlin: `List<String>`, `List<Int>`
- Zig: `[]const u8` or equivalent
