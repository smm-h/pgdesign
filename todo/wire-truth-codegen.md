# Wire-truth growth direction: OpenAPI emission, discriminated unions, projections

## Context

pgdesign owns "storage truth": TOML → validated model → DDL, migrations, and
six-language codegen. Schema-first consumer stacks increasingly also need
"wire truth" — the shapes data takes in transport: typed projections of
tables for API payloads, discriminated unions (sum types) for polymorphic
payloads, endpoint declarations, and OpenAPI as the interoperable artifact.
Today that layer is hand-authored per stack in per-language schema libraries
(Zod and friends), and the existing OpenAPI ecosystem's cross-language
codegen for discriminated unions is notoriously weak — while pgdesign already
has a six-language codegen engine held to a real-compilation quality gate in
CI. The question this todo captures: grow pgdesign to cover some or all of
the wire layer, in the right order, without diluting the tool's identity.

## Evidence inventory (what exists today, verified at file level)

Assets the direction would build on:

- Shared scalar type mapping is centralized: `internal/codegen/typemap.go`
  (`buildTypeMap()` maps each PG type group to all six languages in one
  entry; `ApplyNullable`, `ApplyArray`), `internal/codegen/type_resolver.go`
  dispatches enum/state-machine/scalar/domain.
- Construct fan-out pattern exists: `internal/codegen/enum_gen.go` and
  `internal/codegen/sm_transitions.go` emit one construct across all six
  languages from one call.
- State machines are a structurally adjacent sum type already in the model
  (`model/model.go`: `StateMachine`, `SMState`, `SMTransition`,
  `NamedTransitions`, initial state) — rich enough to drive a tagged-type
  codegen, though today they emit only enum + transition-map.
- New language backends are cheap at the per-mode level: `pkg/genkit`
  (`Generator`, `MultiFileGenerator`, determinism contract);
  `internal/codegen/zig_types.go` is 85 LOC for a full "types" backend.
- A non-DDL wire artifact already ships: `internal/generate/graphql.go` emits
  GraphQL SDL (enums, per-table types with nullability, FK-walked relation
  fields) through the `generate` output-format lane, untouched by
  diff/migrate. This is the proven "emit a transport artifact" path.
- The pipeline has a clean no-DDL lane: `internal/generate/generate.go`
  dispatches by format; diff/migrate only consume DDL-producing constructs.
- TOML surface: `parse/parse.go` `walk()` (~lines 154-164) silently ignores
  unknown top-level sections (note: arguably a bug worth fixing on its own —
  a typoed section name today vanishes without a warning); fmt
  (`internal/format/format.go`) round-trips unknown sections verbatim with
  comments. The 16-step new-section checklist is documented at the top of
  `model/model.go`; a wire-only construct skips the DDL half.
- Quality gate: `internal/test/codegen_compile_test.go` + CI provision all
  six toolchains and compile generated Go/TS (tsc strict)/Python
  (mypy strict)/Java/Kotlin/Zig; DB-execution conformance tests run generated
  code against ephemeral Postgres.
- The revision kernel (`internal/enc`/`objstore`/`rev`/`chain`) gives model
  states content-addressed identity; `rev` already distinguishes model
  classes — a future wire/contract registry could be a first-class revisioned
  model class.

Gaps and corrections (things a naive roadmap would get wrong):

- "validators" codegen mode is NOT schema validation — it emits RLS-policy
  checker functions that query Postgres (`internal/codegen/ts_validators.go`).
  Nothing Zod-shaped exists anywhere; runtime-schema emission is net-new.
- Views are opaque: `model.View` (`model/model.go` ~117-125) holds only the
  query string — no resolved columns, no nullability, no codegen
  participation. There is NO query→typed-columns resolver in the codebase.
- Composite types are modeled and get DDL but are never emitted as native
  product types in any language; domains collapse to their base type in
  codegen (`type_resolver.go` ~33-40).
- jsonb is opaque by design in codegen (`Record<string, unknown>` /
  `dict[str, Any]` / `JsonNode`); the one typed-jsonb hook
  (`Column.JSONSchema` → CHECK constraint, `model/build.go` ~652-660) never
  reaches generated types.
- No serialization-annotation machinery exists anywhere (no
  `@Serializable`/`@SerialName`/sealed emission in Kotlin output; enums are
  plain `enum class`).
- `sqlexpr` is parse+walk only (no evaluator); SQL-flavored. Usable as a
  syntax for refinement expressions only with a new evaluator/emitter on top.
- `pgdesign serve` is a hand-rolled diagnostic API; no endpoint-declaration
  concept to reuse.

## Proposed staging (ordered by evidence-based cost, cheapest first)

### Stage 1 — OpenAPI emission (medium)

New `[api]`/`[endpoints]` TOML sections + an `openapi` output format riding
the same lane as GraphQL. v1 scope: components generated from tables (and
later from unions/projections), paths declared in TOML referencing those
components, no DDL/diff/migrate involvement. Pros: cheapest to prototype;
immediately useful; the GraphQL emitter is the template. Cons: endpoint
semantics (auth, pagination conventions, content negotiation) can balloon —
keep v1 deliberately thin and declarative. Open question: is endpoint
declaration in-scope for the tool's identity at all, or should stage 1 stop
at component-schema emission and let consumers own paths?

### Stage 2 — Discriminated unions + sealed-hierarchy codegen + Dart (medium-large)

`[union.<name>]` sections: variants, per-variant fields (reusing column-style
typed fields), a discriminator field name, and stable wire tags per variant.
Emission: TS discriminated unions (+ exhaustive-switch helpers), Kotlin
sealed hierarchies with serialization annotations (net-new annotation layer —
design it once, language-agnostically, in the shared layer), Go
interface+structs with tag dispatch, Python tagged unions, Java sealed
interfaces, Zig native tagged unions. Add a Dart backend (new typemap column,
types/enums emitters, CI toolchain provisioning) since mobile consumers are a
primary audience for sealed-hierarchy codegen. Generate fixture
encoders/decoders so consumers can round-trip conformance-test their
hand-written or generated models. Unions are wire-only (no DDL) unless/until
a storage mapping is designed (e.g. tagged jsonb columns referencing a union
type — see nice-to-haves). Pros: this is the genuinely differentiated
capability — nothing in the OpenAPI toolchain does this well; the state
machine model and enum/sm fan-out prove the pattern. Cons: annotation layer
and Dart toolchain are real costs; every construct pays the six(+1)-language
compile-gate tax; two type systems (relational + algebraic) now live under
one roof — the model layer must keep them cleanly separated.

### Stage 3 — Projections (large; the missing compiler subsystem)

`[projection.<name>]`: a declared, typed subset/join over tables, verified
against storage (field existence, type compatibility, nullability flow
through joins, FK-validity of exposed ids), emitted as types in all languages
plus runtime schemas (see runtime-schema mode below). This requires the
query/selection → typed-columns resolver that views never got — the single
largest piece of net-new machinery in the whole direction. Sizeable
side-benefit once built: `model.View` can gain resolved columns and views can
finally participate in codegen. Do this last, not first: everything else
delivers value without it.

### Cross-cutting mode — runtime-schema emission (Zod or equivalent)

A codegen mode emitting runtime-validating schemas for TS (and optionally
other languages' equivalents) from tables/unions/projections. Net-new (see
inventory); valuable from stage 2 onward; keep it a mode like `types`, not a
special case.

## Nice-to-haves (each independently valuable)

- Typed jsonb: `Column.JSONSchema` (or a reference to a declared union or
  composite) reaching generated types instead of stopping at a CHECK.
- Composites emitted as native product types in all languages (modeled but
  unused today — cheap win, and a prerequisite pattern for union variant
  payloads).
- Wire/contract registry as a revisioned model class in the kernel, so
  contract states get content-addressed identity and history — pairs with the
  additive-only registry check (separate todo).
- Fix `parse.walk()` silently ignoring unknown top-level sections (warn or
  error; today a typo disappears).
- `fmt` learning canonical placement for the new sections.

## What this direction must NOT do

- No layout/pixel concepts, no framework-specific UI constructs — wire truth
  ends at typed payloads and endpoint declarations.
- No second expression language grown ad hoc: if refinements are wanted,
  decide deliberately between extending `sqlexpr` with an evaluator/emitters
  or declaring refinement constraints structurally.
- No weakening of the determinism/compile-gate discipline to make new
  constructs cheaper — the gate is the product's credibility.

## Affected areas

`parse/` (+ `parse/types.go`), `model/` (+ `build.go`, `validate.go`),
`internal/codegen/*` (new construct emitters ×7 languages, annotation layer,
runtime-schema mode, Dart backend), `internal/generate/` (openapi format),
`cmd/pgdesign/codegen_registry.go`, CI workflow (Dart toolchain),
`internal/format/format.go`, docs throughout.

## Effort

Multi-release direction. Stage 1 medium; stage 2 medium-large (annotation
layer + Dart are the bulk); stage 3 large (a resolver subsystem). Stages are
independently shippable and ordered so each release delivers standalone
value; nothing here blocks on anything outside this repo.
