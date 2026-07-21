# Comprehensive roadmap: determinism, identity, migrate integrity, orchestration, imports, API, visualization

Single consolidated plan from the 2026-07-19 design session, revised 2026-07-21 after
two adversarial critique rounds (each: six investigation agents verifying grounding
facts against source and auditing every phase; accepted findings folded in). This file
is exempted from todo immutability by explicit owner authorization — it is a living
plan; git history preserves prior versions. Supersedes/absorbs several active todos
(see "Relationship to existing todos" at the end).

## Decision provenance

Per the %% convention: decisions marked `[%%]` were trust-adopted (owner accepted the
recommended option) — weakly held, freely reversible, never to be cited as deliberate
intent. `[deliberate]` decisions were the owner's own.

- `[deliberate]` **No rename.** The project stays `pgdesign`. rename-to-strictpg todo
  moved to todo/.obsolete/.
- `[deliberate]` ONE release for the whole roadmap, at the very end — never per-phase
  releases. Global rule in ~/Projects/CLAUDE_ADDITIONS.md.
- `[deliberate]` No backward compat anywhere (global rule): `migrate upgrade` DROPS
  the old tracking table after migrating rows; all internal callers updated; no
  compat-named objects, no dual recognition. An earlier "compat-named view" idea was
  withdrawn as compat-in-disguise.
- `[%%]` Compiler/live-layer seam: `build` and the core pipeline stay pure; live-DB
  functionality is a distinct tier behind a designed boundary.
- `[%%]` Ambition: summit-grade design only where outputs are permanent (schema
  identity, migration file format); pragmatic rungs elsewhere, maximal designs
  recorded as end-states.
- `[%%]` Canonical ordering lives in a shared finalize routine (`Canonicalize()`)
  invoked by ALL Schema constructors — Build, BuildMulti, AND Introspect (amended:
  originally "Build() orders"; introspect never calls Build, and 0.5/3.3/5.8 depend
  on introspected schemas being canonical).
- `[%%]` Canonical serialization is semantic-only, full-model scope (object comments
  in; Extensions and PGVersion in — amended: they affect emitted DDL and were
  originally unenumerated).
- `[%%]` Revision hash = SHA-256 of canonical bytes; every-command enforcement with
  full-regenerator/partial-writer refusal semantics (see 6.2).
- `[%%]` Migration precondition drift = hard error, always. No tolerance flags.
- `[%%]` generate --idempotent unifies with migrate verification via a predicate IR
  (one definition, Go executor + SQL renderer backends, CI conformance matrix).
- `[%%]` Migration history is append-only; squash emits consolidation migrations;
  superseded files archived; checksum verification unconditional once the format
  lands.
- `[%%]` Migration ops are self-contained — amended: the serializable form IS the
  3.1 per-object canonical form (pointer-def ops embed their target object's
  canonical bytes; no second serialization dialect, no lossy flat mirrors).
- `[%%]` Migrations identified by revision pairs in a parent-linked chain; filenames
  cosmetic (sequence + slug); no version bumps.
- `[%%]` Migration generation is PURE: diff(chain-head model, current model) —
  amended to name the mechanism: each migration's to-revision model is stored as a
  canonical per-object SNAPSHOT (no op-replay engine); drift surfaces at apply,
  never folded into generated migrations; intentional drift adoption via an explicit
  baseline-derived flow.
- `[%%]` The chain's home is `migrations/manifest.jsonl` (append-only, atomic line
  appends); consolidations are ADDITIONAL edges; archived originals stay reachable
  via the manifest.
- `[%%]` Per-revision snapshots live in content-addressed
  `migrations/.snapshots/<revision>.json`, referenced by the manifest.
- `[%%]` The per-op journal records op identity AND the serialized down-op; rollback
  is database-driven — amended scope: guaranteed from the upgrade boundary forward;
  the pre-upgrade prefix and baselined migrations are ROLLBACK-FROZEN (crossing the
  boundary = hard error; synthesized journal rows carry no executable down-ops).
  Journal = one per-op table + summary view.
- `[%%]` DB position anchor: `pgdesign_chain_position` table (current revision,
  in-progress edge ref, per-database grandfather boundary); baseline and upgrade
  write it; edge selection reads it.
- `[%%]` Journal = table `pgdesign_migration_ops` + summary view
  `pgdesign_applied_migrations` (view exists on merit: one SQL-level definition of
  "applied migrations + status" shared by apply/rollback/status/serve).
- `[%%]` Grandfather boundary = verify-then-stamp: `migrate upgrade` requires a
  clean TOML<->DB reconcile, then stamps boundary = revision(current TOML model);
  refuses with the drift report otherwise. Requires clean schema files per git
  (when in a repo; outside a repo proceeds with a stated caveat). Runs as ONE
  transaction with an in-transaction assertion that the view reproduces the old
  applied set.
- `[%%]` Orchestrator: `pgdesign revise` = pure tier (build + migration generation),
  then DB tier (import verification + DB checks); seed separate; pure outputs kept
  on partial failure; separate commits; commit failure = hard error.
- `[%%]` Imports: split slices (fail-closed); `alias:table` syntax (FK references
  only, initially); vendored import-surface snapshots = referenced tables + the
  TRANSITIVE CLOSURE of type definitions their columns use, per-object hashes from
  the canonical primitive + source pin; type/enum collision = hard error; app-only
  DDL with live verification; imported extension/pg_version requirements re-declared
  locally.
- `[%%]` Codegen branding, default-on, in the single final release — amended per
  critique: Go opaque struct with VALIDATING boundary (Scanner/UnmarshalJSON/Text
  implemented via Parse, ERROR on invalid — never absent/rejecting, which would
  break DB scans and JSON round-trips); Python guarded StrEnum + enum-typed surfaces
  incl. the DB-read path; TS KEEPS the literal union (already compile-closed with
  exhaustiveness narrowing — a nominal brand would regress) + parse() at boundaries
  + re-typed transition maps; Java/Kotlin value-based parse + JPA AttributeConverter
  (NOT @Enumerated(STRING), which persists enum NAMES like IN_PROGRESS, not DB
  values like in_progress); Zig wrapper struct + re-typed transition maps.
  Constants mode unchanged; constraints mode = stated data exception (embeds
  valid-value lists, not construction); drizzle/sqlalchemy string-shaped by ORM
  necessity — stated.
- `[%%]` Seed with imported FKs: tiered real keys (live pool / count-wrapped offset
  subqueries in INSERT mode / hard error only for offline+COPY+NOT-NULL).
- `[%%]` strictcli: connection-env kind; PGDESIGN_DB declared once; all --db flags
  bind it; provenance via existing Context.Source(); DB checks skip under
  --hermetic. Handed off via generically-worded todo; strictcli session builds.
- `[%%]` Partition: premake required; opt-in `schedule` key; missing schedule
  without acknowledgment = warning.
- `[%%]` pkg/diff deleted; promotion trigger recorded.
- `[%%]` Web UI frontend deferred; only the DB-free API contract is built.
- `[%%]` Migration slugs auto-derived, optional override. Archive =
  `migrations/.archive/`. Commands: `import lock` / `import update`,
  `migrate upgrade`.
- `[%%]` Consumer regeneration+adaptation todos filed in consumer repos at the
  single final release.

## Grounding facts (verified in source; corrected across two critique rounds)

- Raw model.Schema is NON-deterministic: resolveTable builds per-table collections
  by ranging Go maps. Ordering has TWO semantics: alphabetical (the 7 Sorted*
  helpers) and TOPOLOGICAL (tables via Build; views/matviews/functions topo-sorted
  in TWO emitters — generate.go AND python_ddl.go, duplicated). Top-level ordering
  inconsistent between JSON (name-sorted) and DDL (declaration/topo). Matview
  indexes unsorted on EVERY path (and not covered by the 7 helpers). gorm/drizzle/
  jpa/sqlalchemy codegen, validator policy extraction, AND the python query-layer
  family (~12 sites) emit in raw map order. Auto-FK indexes are appended by
  enrich() AFTER resolveTable — any construction-time sort must run post-enrich.
  The existing determinism test cannot catch any of this.
- Introspect constructs model.Schema directly and NEVER calls Build — introspected
  schemas have nil FKGraph/TablesByName and raw query order; diff --live consumes
  them. The finalize sequence is already copy-pasted between Build and BuildMulti.
- Two divergent JSON serializers (generate json sorts; serve emits raw). FKGraph and
  TablesByName are json:"-". FKEdge has NO schema field. FilterByGroups/
  FilterBySource rebuild TablesByName but NOT FKGraph.
- semtype.Registry: separate from Schema, unexported/unserializable; scalar CHECKs
  and builtin shadowing live only there; typeDefsEqual ignores top-level Comment and
  Source but DOES compare nested state/transition comments — snapshot field policy
  must say so. Enums/domains/composites/SM types exist BOTH as registry TypeDefs and
  Schema fields (duplication the canonical form resolves: registry wins for type
  definitions).
- Headers: SIX-plus wordings (incl. a no-period CLI fallback and an SQL variant)
  plus a seed variant, across ~41 codegen sites + 6 validator helpers + 5 CLI sites
  + the codegenHeader/hasCommentHeader path. Codegen generators SELF-EMBED headers —
  the planner's prepend branch is effectively dead; planner and standalone codegen
  are already byte-identical. The genuinely headerless surfaces are the `generate`
  command output (STDOUT-ONLY — no --output flag exists; it cannot create on-disk
  divergence) and build's doc/d2/graphql/json paths. codegen --check is byte-exact
  (pkg/genkit). Go headers today do NOT match the `^// Code generated .* DO NOT
  EDIT\.$` tooling convention (lowercase variant) — 4.2's wording adoption fixes
  that too.
- Migrate: tracking table = version/applied_at/checksum/description; checksum over
  FILE bytes, NEVER verified; no per-op records; version row written LAST; partial
  phases/non-transactional ops leave committed DDL with no durable record; re-apply
  restarts at op 0. Rollback re-reads files, trusting them over the DB. Ops have no
  stable identity. Non-serialized op families include the nine pointer-def families
  PLUS RawSQL (carries SM-trigger DDL and partman UPDATEs — silently dropped on
  round-trip) and PartitionChildSpec. After round-trip, most degrade to comment
  no-ops; sequences lose parameters; create_function/create_trigger fall back to
  emitting the WRONG OBJECT (deny-mutation / append-only). Only create_table has a
  mitigation. Non-transactional ops (CONCURRENTLY, enum-add) run on the raw conn
  between transactions — a post-op journal write can be lost after the DDL commits
  (durability gap one level down).
- Squash deletes/rewrites originals; the M200 applied-version guard runs only if
  --db is voluntarily passed; tracking rows orphaned; zero CLI-flow tests.
- migrate generate requires --db and --version; NO ledger exists; discovery skips
  non-semver filenames; ~7 functions rely on semver ordering; the migrations-dir
  sentinel bug is replicated at EIGHT call sites (the `output` flag already shows
  the correct Default(nil)+was-set pattern to reuse). serve's migration-version
  endpoint opens files by `version+".toml"` — a second serve change under
  sequence+slug names.
- Introspect has NO table-level filtering (pgdesign_migrations reported as a user
  table); function/trigger filters exist but use the `_pgdesign_sm_%` LEADING-
  underscore pattern — a `pgdesign_%` table pattern does NOT cover it (two patterns
  needed); views need relkind 'v' coverage separately. The differ compares
  expressions by RAW STRING equality while introspect returns PG-rewritten forms
  (pg_get_constraintdef/expr/indexdef) — a live bug: introspect->diff over schemas
  with CHECKs/partial indexes/policies reports false drift today; only types have
  real normalization; the shadow test survives on easy fixtures.
- serve: hard DB-coupled; --timeout never enforced; audit synchronous; GenerateD2
  called with nil registry (SM diagrams silently dropped); serve's TOML loader
  discards the registry; project-loading helpers live in package main.
- strictcli: check command builds a fully-populated *Context and discards it
  (widening = interface change + reconciling two construction paths); infra
  roots/handshake envs hermetic-IMMUNE, flag Env() hermetic-SUPPRESSED; per-flag
  provenance ALREADY EXISTS (Context.Source()). 16 --db flags, three default
  semantics.
- Codegen enum shapes: Go `type X string` (open); TS literal union (compile-CLOSED
  with exhaustive-switch narrowing; the open door is runtime string/any ingress);
  Python StrEnum (str subclass, value-construction open; StrEnum members still ==
  raw strings and format into f-strings — the closable door is construction, not
  comparison); Java/Kotlin real enums (closed; constant names are UPPER_SNAKE while
  getValue() returns the raw DB value — @Enumerated(STRING) would persist NAMES);
  Zig bare string consts. No parse helpers anywhere. TS AND Zig transition maps use
  raw string keys. Python query-layer never imports/defines the enum classes it
  annotates rows with (annotations survive via `from __future__ import
  annotations`); PgBackend reconstructs rows with no enum coercion. constraints
  mode embeds raw enum-value lists in six languages. `doc` is a first-class
  [output] format, headerless today. splitfmt (.sqlsplit) is a SEALED format whose
  line 1 must be the statement count — it cannot carry a header stamp.
- CI: postgres:17 + pg_partman, PGDESIGN_REQUIRE_DB=1; 11 DB-backed migrate tests
  of ~162 in the package.
- Partition bugs: python_ddl.go Retention-as-p_interval; premake -> 0; silent skip
  without pg_partman; manual children + maintenance contradictory DDL;
  PartmanRunMaintenanceCron() dead-but-tested.
- pkg/diff: zero importers; matcher generic; result types embed ~22 PG model types
  consumed field-by-field by migrate.
- generate and migrate are sibling packages (neither imports the other; both import
  internal/sql) — predicate IR structs + SQL renderer need a shared leaf home.

---

## Phase 0 — Foundational groundwork

### 0.1 Canonical ordering via a shared finalize routine
- **What:** A `Canonicalize()` finalize routine — canonical ordering (alphabetical
  for the per-table collections incl. matview indexes; topological with input-order
  tie-break for tables/views/matviews/functions; columns source-ordered) plus
  derived-structure construction (FKGraph, TablesByName) — invoked by ALL Schema
  constructors: Build, BuildMulti, AND Introspect (which today never calls Build and
  yields nil graphs and raw query order). Sorting runs AFTER enrich() (auto-FK
  indexes are appended post-resolveTable). ONE canonical top-level order adopted by
  both JSON and DDL. Delete emitter-side sorting: the 7 Sorted* helpers, the
  DUPLICATED topo sorts in generate.go AND python_ddl.go. Fix the luck-stable
  emitters: gorm/drizzle/jpa/sqlalchemy, validator policy extraction, and the
  python query-layer family (~12 sites). Replace the too-weak determinism test with
  a multi-iteration build-and-compare-bytes CI test (single runs have ~50% false-
  negative odds on small fixtures — specify iterations and fixture size) plus a
  Canonicalize postcondition. Note: JSON goldens change ONCE under the unified
  top-level order (functions alphabetical -> topo); "goldens byte-stable" means
  across-runs, not versus pre-0.1.
- **Why:** The revision hash is a hash of bytes; nondeterministic bytes make
  identity meaningless. Anchoring in a shared finalize (not Build alone) is what
  makes INTROSPECTED schemas canonical too — 0.5's verify, 3.3's serve path, and
  5.8's reconcile all consume them — and it deduplicates the finalize sequence
  already copy-pasted between Build and BuildMulti. Topological-vs-alphabetical
  distinction protects DDL correctness.
- **Verify:** Multi-iteration determinism test red before / green after; a
  view-references-view fixture emits in dependency order; INTROSPECTED schemas pass
  the same canonical-order postcondition; fixture with 2 matview indexes + multiple
  FKs/policies stable; grep finds no emitter-side sorting.

### 0.2 Schema-qualified identity keying
- **What:** FKEdge gains a schema field (struct change); rekey FKGraph, cascade
  walkers, group resolution to (schema, name). Fix FilterByGroups/FilterBySource to
  recompute derived structures for the filtered subset (today they carry the
  parent's stale FKGraph referencing excluded tables).
- **Why:** Two identity schemes for one object is a latent bug today and a
  guaranteed bug under imports. Filters handing phase 9.3/7.3 a stale graph is a
  live correctness hole.
- **Verify:** Red-green: same-named tables in two schemas through cascade checks
  (W013/W014/W015), workload analysis, group filtering; filtered schema's graph
  contains no excluded tables.

### 0.3 Header consolidation (byte-preserving)
- **What:** One shared parameterized header helper routed through ALL sites (~41
  codegen + 6 validator helpers + 5 CLI + codegenHeader/hasCommentHeader + seed),
  REPRODUCING each site's current wording byte-for-byte (six-plus wordings
  preserved as-is for now). The stamp GRAMMAR (format + parse) is designed in
  pkg/genkit from the start — writer and reader of the stamp in one package — with
  the language-comment-prefix helper in internal/codegen consuming it. No wording
  change here (that lands in 4.2 with the revision line, so consumers regenerate
  once). Byte-preservation claim scoped: 0.6's write-path consolidation may change
  OTHER bytes; this subphase's own changes are byte-invisible.
- **Why:** Stamping through 50+ scattered literals with six wordings means that
  many chances to miss one; a missed stamp is invisible to enforcement. Grammar in
  genkit prevents the writer/reader drift disease.
- **Verify:** Header-originating bytes identical before/after on consumer fixtures;
  grep: zero header literals outside the helper; stamp grammar round-trip test in
  genkit.

### 0.4 Type-registry snapshot
- **What:** Deterministic ordered exported snapshot + reconstruct. Explicit field
  policy (cannot mirror typeDefsEqual, which ignores top-level Comment/Source but
  DOES compare nested state/transition comments): semantic fields + all comments
  included, Source excluded, builtin-sourced entries excluded.
- **Why:** Registry holds semantic state existing nowhere else; identity omitting
  it calls different schemas "the same." Bridges the registry into the canonical
  form and later into import surfaces.
- **Verify:** Snapshot -> reconstruct -> snapshot byte-stable; registration-order
  independent; Source relabeling changes nothing; nested transition comments DO
  affect it.

### 0.5 Introspect filters managed objects
- **What:** Managed-object exclusion by TWO patterns (`pgdesign_%` tables/views —
  covering tracking, journal, chain-position, and the summary view via relkind 'v'
  coverage in the view queries — and the legacy `_pgdesign_sm_%` function/trigger
  prefix), unified under one concept. A user table matching the reserved pattern
  triggers a diagnostic (it would silently vanish from introspection otherwise).
- **Why:** Reconcile demands "introspect, diff, expect empty"; pattern-based
  filtering means new managed objects inherit coverage. The namespace reservation
  must be loud, not silent.
- **Verify:** DB-backed: introspect a migrated DB (tracking + journal + view +
  position present), diff, empty; reserved-name user table produces the diagnostic.

### 0.6 One write path; sentinel fix
- **What:** Consolidate multi-file write + owned-dir/orphan bookkeeping onto the
  planner; standalone codegen becomes a thin caller. Corrected rationale: codegen
  headers are self-embedded and already byte-identical across paths — the real
  divergence risks are the write/orphan logic itself and the headerless
  generate-command/doc/d2/graphql surfaces (addressed via 0.3's helper + 4.2's
  stamping). Fix the migrations-dir sentinel at ALL EIGHT call sites via one shared
  helper using the existing Default(nil)+was-set pattern (no phase-2 dependency).
- **Why:** Phase 6 enforcement must guard every write; two divergent write paths
  mean two guards that drift. Eight copies of a sentinel bug is eight chances to
  fix seven.
- **Verify:** Standalone codegen and build byte-identical on a fixture; identical
  orphan behavior; sentinel red-green covering explicit-equals-default at all
  sites.

### 0.7 Comparison-normalization primitive
- **What:** ONE shared normalization primitive — types, defaults, and expressions
  (parse/deparse both sides via the existing go-pgquery wrapper) — homed in
  internal/sqlutil (already the sqlexpr/diagnostic adapter; NOT internal/diff,
  which would force introspect->diff coupling). The differ adopts it IMMEDIATELY
  (red-green: introspect->diff over a schema with CHECK constraints, partial
  indexes, and policies reports false drift today — a live bug). Later consumers:
  5.7 preconditions, 5.2 upgrade reconcile, 5.8 reconcile-verify, shadow test.
- **Why:** Hoisted from phase 5 because 0.5's own verify goal ("introspect, diff,
  expect empty") cannot pass without it, and because it fixes a shipping bug now.
  Same build-shared-machinery-first logic phase 0 applies to ordering and headers.
- **Verify:** Red-green on the false-drift fixture; normalization unit suite
  (PG-rewritten forms equal their sources); diff --live clean on the comprehensive
  fixture.

## Phase 1 — Ground-clearing

### 1.1 Delete pkg/diff
- **What/Why/Verify:** As before — remove the stub (zero importers), record the
  promotion trigger; build + vet clean.

### 1.2 Partition bug fixes (red-green each)
- **What/Why/Verify:** As before — python_ddl interval/retention; premake required;
  hard errors for non-RANGE+maintenance, undeclared pg_partman, maintenance+manual
  children; part_config failure becomes a diagnostic. CI has postgres+pg_partman.

### 1.3 Partition lifecycle completion
- **What/Why/Verify:** As before — introspect part_config into the model; diff
  distinguishes setup/update/interval-change; migrate guards extension presence;
  `schedule` key wires the dead helper; missing-schedule warning.

### 1.4 Squash safety stopgap
- **What/Why/Verify:** As before — mandatory --db + M200 until phase 5 replaces
  squash; stated limits (blocks offline squash of never-applied ranges; doesn't fix
  rewrite mechanics); first squash-CLI test.

## Phase 2 — Connection environment

### 2.1 strictcli: connection-env kind + check context access
- **What/Why/Verify:** As before — third env primitive (hermetic-suppressed, lazy,
  no default); CheckContext widening + reconciling the two construction paths;
  provenance via existing Context.Source(). Handed off via generically-worded todo;
  strictcli session implements and releases; pgdesign adopts.

### 2.2 pgdesign adoption
- **What:** As before (declare once; bind all 16 --db flags, normalizing three
  default semantics; checks via framework; hermetic skips visible; config-URL layer
  documented precedence) — PLUS: phase 2 is NOT a leaf. Every later phase's new DB
  entrypoint binds the connection env from birth: revise's DB tier (6.1), import
  lock/update + live verification (7.2/7.4), seed tier-1 pools (7.4). DAG edges
  added accordingly.
- **Why:** Otherwise each later phase re-adds a raw --db and phase 2's pathology
  regrows — the exact thing it exists to end.
- **Verify:** As before, plus: no post-phase-2 command introduces an unbound DB
  flag (checked at review; grep for raw os.Getenv stays clean).

## Phase 3 — Schema identity

### 3.1 Canonical serialization — compositional, per-object primitive
- **What:** As before (per-object canonical JSON, explicit key ordering, whole =
  versioned preamble + ordered concatenation, semantic-only, registry wins for type
  definitions, derived caches excluded, builtins excluded, object comments in,
  TOML-formatting comments out, [suppress] out, registry-absent marker, format
  version) — with amendments: `Schema.Extensions` (ordered) and `PGVersion` are IN
  the canonical form (both change emitted DDL; extension DDL-name resolution stays
  emitter-side, covered by byte-compare — baking resolved names into the model at
  Build is the recorded summit alternative). The omit-unset policy is specified
  PER FIELD in the format spec, distinguishing pointer optionals from value-typed
  optionals (e.g. Premake collapses explicit-0 and omitted today — the spec table
  decides each). The JSON ARTIFACT is an envelope `{format_version, revision,
  model}` — revision = hash(model); bytes cannot contain their own hash.
- **Why:** As before; the envelope resolves the in-band-stamp circularity; the
  per-field policy is what keeps hash stability deliberate rather than accidental.
- **Verify:** As before, plus: pg_version change flips the revision; extension
  add/remove flips it; envelope revision verifies against model bytes.

### 3.2 Revision hash
- **What/Why:** As before (SHA-256 whole + per-object; upgrade-invalidates-all
  policy; one-directional conformance revision-equal => diff-empty; diff fast
  path) — plus the stated invariant: revisions are NEVER compared across the
  registry-present/registry-absent boundary (revision(TOML) != revision(introspect
  of the same DB) by construction; only diff crosses that boundary).
- **Verify:** As before + a test asserting the boundary invariant is enforced
  (comparison across it is a programming error, not a false mismatch).

### 3.3 One serializer everywhere
- **What/Why/Verify:** As before — generate json and serve call the SAME function;
  introspect-sourced responses carry the marker. (DAG: 3.3 -> 4.2, since json
  stamping lives in the unified serializer's envelope.)

## Phase 4 — Codegen breaking release (content lands in the single final release)

### 4.1 Branded types per language — corrected mechanisms, full surface
- **What:** Shared mechanism first (extend the enum_gen dispatch seam). Go: opaque
  struct with a VALIDATING complete boundary — constants as the only in-code
  constructors; Parse errors on unknowns; UnmarshalJSON/UnmarshalText/sql.Scanner
  are all IMPLEMENTED VIA Parse and error on invalid values (never absent — go
  structs live in db-scanned/json-round-tripped positions in go_types and gorm
  output); Valuer/MarshalJSON/Stringer for egress; zero value detectably invalid.
  Python: StrEnum retained; implicit value-construction closed; parse() classmethod
  the only dynamic entry; __reduce_ex__/pickle override; query-layer + validator
  signatures enum-typed; PgBackend read path routes through parse(); AND the
  query-layer package gains actual imports/definitions of the enum classes it
  annotates with (today annotations survive only via `from __future__ import
  annotations` — parse()-routing makes the names load-bearing). Stated honestly:
  StrEnum members still == raw strings and format into f-strings — the closed door
  is construction. TS: KEEP the literal union (already compile-closed, exhaustive
  switches keep working — a nominal brand would regress narrowing); add parse() at
  boundaries; re-type the transition maps off raw string keys. Java/Kotlin:
  value-based parse; JPA gains a generated AttributeConverter (@Convert) backed by
  getValue()/fromValue() — NOT @Enumerated(STRING), which persists constant NAMES
  (IN_PROGRESS) instead of DB values (in_progress). Zig: wrapper struct + parse;
  transition maps re-typed (three-site change). Classified exceptions: constants
  mode unchanged; constraints mode embeds valid-value DATA lists (not construction)
  — stated exception; drizzle/sqlalchemy string-shaped by ORM necessity — stated.
- **Why:** As before — invalid values cannot be named or smuggled — with the
  mechanism corrections from critique: validating-not-rejecting (Go would otherwise
  ship unrecoverable scan failures), union-not-brand (TS), converter-not-Enumerated
  (JPA would otherwise write wrong values on every insert).
- **Verify:** Per language, invalid values fail at the earliest boundary WITH
  ERRORS (never absent methods); Go fixture: all four ingresses validate and
  round-trip valid values; Java fixture: persisted value equals getValue() not
  name(); Python: pickle round-trip + PgBackend yields enum-typed fields; TS:
  exhaustive switches still compile, transition maps reject unknown keys; type
  checkers pass where toolchains exist.

### 4.2 Header unification + revision stamping
- **What:** As before (ONE wording — the Go-tooling-recognized convention, fixing
  today's non-conformant lowercase Go headers; revision line + stamp
  format-version; stamp grammar already in genkit per 0.3) — with corrected
  artifact classes: comment-stamped = sql, d2, graphql, codegen, seed, AND doc
  (first-class [output] format, headerless today — previously missed);
  in-band-stamped = json (envelope field per 3.1); structurally exempt = svg
  (non-deterministic) AND .sqlsplit (SEALED format — line 1 must be the statement
  count; a header breaks Decode; freshness still byte-compare-covered). Stated
  cost: partial regeneration impossible — intended.
- **Why:** As before; the class enumeration exists precisely because a missed
  class (doc) or an impossible class (.sqlsplit) silently undermines "every
  artifact carries the stamp."
- **Verify:** As before + doc output stamped; .sqlsplit byte-unchanged and
  Decode-able; json envelope carries revision.

### 4.3 Breaking-change packaging (lands in the single final release)
- **What/Why/Verify:** As before (breaking-typed changelog entries; consumer todos
  filed at THE release with regeneration + adaptation notes: TS switch sites keep
  compiling now — adjusted note — but parse() call sites and Python raw-string
  construction sites must adapt) — plus one addition: `generate --idempotent`'s
  semantic change (silent IF-NOT-EXISTS skip -> RAISE on definition mismatch, from
  5.7) is ALSO a consumer-visible breaking change and joins these notes.

## Phase 5 — Migrate integrity

Design prerequisites, per planning discipline: the journal/view/position schemas
(5.5, `pgdesign_migration_ops` / `pgdesign_applied_migrations` /
`pgdesign_chain_position`) are DESIGNED before 5.2 is implemented (5.2's upgrade
migrates rows into them); the normalization primitive already exists (0.7). Code
lands in the stated order; nothing ships mid-phase (single-release cadence).

### 5.1 Self-contained ops = per-object canonical bodies
- **What:** The serializable form of every pointer-def op IS the 3.1 per-object
  canonical form: ops embed their target object's canonical bytes (canonical-JSON
  string inside the TOML op); OpToSQL renders from the reconstructed object — a
  total function of the on-disk form. Families: the nine pointer-def families PLUS
  RawSQL (SM-trigger DDL, partman UPDATEs — today silently dropped on round-trip)
  and PartitionChildSpec. Every comment-stub no-op and wrong-object fallback
  (deny-mutation / append-only) DELETED; sequences keep parameters. Table-driven
  round-trip test per family (generate -> write -> re-parse -> byte-identical
  SQL); write-time round-trip invariant.
- **Why:** As before — degraded/wrong-object states unrepresentable — now WITHOUT
  a second serialization dialect: the flat TOML mirrors that caused the lossiness
  are replaced by the same canonical bodies used everywhere else (one primitive:
  ops, snapshots, imports, API).
- **Verify:** Round-trip table test covers ALL families incl. RawSQL and partition
  children; wrong-object fallbacks gone; write-time invariant green.

### 5.2 Chain format, manifest, snapshots, and adoption
- **What:** As before (sequence+slug files — auto-derived slug; from/to revision +
  parent linkage; `migrations/manifest.jsonl`; chain-head/find-heads API; genesis;
  discovery/ordering rewritten off semver) — plus: each migration's to-revision
  model is written as a content-addressed snapshot
  `migrations/.snapshots/<revision>.json` (the canonical envelope; idempotent
  writes; referenced by the manifest; self-verifying — revision(snapshot bytes)
  must equal the manifest's to_revision). `migrate upgrade` (one-time): requires
  clean schema files per git when in a repo (existing gitShow plumbing; outside a
  repo proceeds with a stated caveat); verify-then-stamp reconcile (0.7
  normalization) or refusal with the drift report; runs as ONE TRANSACTION with an
  in-transaction assertion that the new view reproduces the old applied set;
  recomputes checksums; migrates tracking rows into `pgdesign_migration_ops`;
  writes `pgdesign_chain_position` (current revision, per-database boundary);
  DROPS the old table; semver files become the linear prefix with SYNTHETIC
  checksum-verified (not model-derived) revisions; builds manifest + head
  snapshot. serve's migration-version endpoint updated for sequence+slug names
  (it opens files by `version+".toml"` today). Manifest<->files<->snapshots
  consistency check.
- **Why:** As before — plus the snapshots are what make 5.9's pure generation
  implementable at all (no op-replay engine exists or is built), and
  single-transaction upgrade means a crash leaves either the old world or the new
  world, never half.
- **Verify:** As before + snapshot self-verification; crash-injection test around
  upgrade (old world or new world, nothing between); dirty-tree refusal;
  mid-edit-TOML cannot stamp an unapplied model.

### 5.3 Append-only squash (consolidation edges)
- **What/Why:** As before (consolidation = ADDITIONAL manifest edge; originals to
  `migrations/.archive/` intact; edge selection by the database's
  `pgdesign_chain_position`; no orphaned tracking rows) — plus the invariant:
  rolling back a consolidation edge and rolling back its superseded originals
  reach the SAME prior revision (tested).
- **Verify:** As before + the rollback-equivalence test.

### 5.4 Unconditional checksums
- **What/Why/Verify:** As before — only after 5.2/5.3; mismatch = corruption, hard
  error on apply AND rollback; prefix files carry synthetic revisions whose
  checksums ARE verified (the "one meaning" claim holds via checksum, not model
  derivation, for the prefix).

### 5.5 Applied-op journal
- **What:** As before (ONE table `pgdesign_migration_ops` + view
  `pgdesign_applied_migrations`; op identity + serialized down-op; resume skips
  journaled ops) — plus per-op-class journal TIMING: transactional ops journal
  inside the op's transaction (atomic with the DDL); non-transactional ops use
  INTENT-then-CONFIRM rows plus mandatory idempotent SQL forms (CREATE INDEX
  CONCURRENTLY IF NOT EXISTS; enum-add already idempotent) so a crash between DDL
  and journal is recoverable — the same protocol for journal-driven rollback of
  non-transactional down-ops. `pgdesign_chain_position` updated in the same
  transaction as each edge-completing journal write.
- **Why:** As before — plus: a journal row written after a non-transactional
  commit can be lost, recreating the durability gap one level down; intent/confirm
  + idempotence closes it. The idempotent forms here are bounded and principled:
  the precondition already ran before the intent row; the IF-NOT-EXISTS guards
  only the intent-to-confirm window.
- **Verify:** As before + kill-between-DDL-and-journal fault injection for
  CONCURRENTLY and enum-add: re-apply converges with correct journal state.

### 5.6 Journal-driven rollback (scoped)
- **What/Why:** As before (recorded down-ops in reverse journal order; files never
  consulted) — SCOPED: guaranteed from the upgrade boundary forward. The
  pre-upgrade prefix and baselined migrations are ROLLBACK-FROZEN: crossing the
  boundary is a hard error naming it (their synthesized journal rows carry no
  executable down-ops; old tracking rows and `"baseline"` checksums cannot yield
  them). Reversibility pre-check retained.
- **Verify:** As before + boundary-crossing rollback refuses with the precise
  error; post-boundary rollback works with source files archived.

### 5.7 Preconditions + predicate IR
- **What:** As before (per-op-class predicates via 0.7 normalization; hard error
  naming object/expected/found; DML ops precondition-free; predicate IR = one
  structured definition, Go executor + SQL renderer; conformance matrix incl. the
  differ as a third leg where object classes overlap; generate --idempotent
  regenerated from the IR, RAISE on mismatch) — plus placement decided UPFRONT:
  IR structs + SQL renderer live in a shared leaf package (internal/predicate);
  only the pgx executor lives in migrate; 7.4 reuses the executor. The
  --idempotent semantic change is listed in 4.3's breaking notes.
- **Verify:** As before.

### 5.8 Post-apply reconcile-verify
- **What/Why/Verify:** As before (introspect with 0.5 exclusions + 0.7-normalized
  diff; residual mismatch = hard error listing every object; SM-vs-enum lossiness
  documented; verify on a fixture CONTAINING CHECKs, partial indexes, policies;
  revision-equal-implies-diff-empty asserted) — introspected schemas are canonical
  via 0.1's shared finalize.

### 5.9 Pure chain-based generation + ecosystem alignment
- **What:** migrate generate = diff(deserialize(head snapshot), current model) —
  pure, no DB; the head snapshot self-verifies against the manifest. Drift caught
  at apply (5.7/5.8). Intentional drift adoption via the baseline-derived flow
  (baseline writes `pgdesign_chain_position` + a snapshot). Shadow test, serve
  endpoints, docs updated for format+journal+manifest+snapshots.
- **Why:** As before — same TOML edit must produce the same migration regardless
  of DB state; the snapshot mechanism (5.2) is what makes this a deserialization,
  not an unbuilt replay engine.
- **Verify:** As before + generation without any DB from a chain fixture; drifted
  DB does NOT alter generated output but fails apply with the precondition report.

## Phase 6 — Orchestration and enforcement

### 6.1 pgdesign revise
- **What/Why:** As before (pure tier = planner + 5.9 generation; DB tier = import
  verification + DB checks, bound to the phase-2 connection env; chain head from
  manifest, two-head hard error; separate commits; commit failure hard error;
  partial failure keeps pure outputs) — plus stated: DB-tier check findings are
  advisory for the run — they fail the command loudly but do not retroactively
  invalidate the already-committed migration (the next revise incorporates fixes
  as new work).
- **Verify:** As before.

### 6.2 Revision enforcement (corrected taxonomy)
- **What:** The invariant: all regenerable snapshot artifacts in the planner set
  share ONE revision after any write. FULL regenerators (build, revise) always
  allowed. The partial-writer set is exactly ONE command today — `codegen
  --output` — which refuses when artifacts it would not rewrite carry a different
  revision. `generate` is stdout-only: shell-redirected output is covered by
  check-time verification, not write-time refusal (stated). OUTSIDE the invariant,
  explicitly: migration files + snapshots (append-only chain at historical
  revisions — covered by the manifest/consistency checks) and seed output
  (stamped by 4.2 but not in the planner set — stated, so it is never an
  unclassified stamp-disagreement source). Missing/old-format stamps = stale (full
  regenerators proceed; the partial writer refuses); stamp format-version prevents
  post-upgrade lock-out. The revision CHECK covers what byte-compare cannot:
  chain/manifest/snapshot integrity, cross-artifact stamp agreement (cheap for the
  standalone check; the write-time guard necessarily has the model in hand),
  stdout/standalone artifacts. genkit stamp-extractor complements byte-compare.
- **Why:** As before — with the taxonomy shrunk to reality (A6): one genuine
  partial writer, honestly-scoped stdout coverage, and the two artifact classes
  that must be OUTSIDE the invariant named as such.
- **Verify:** TOML edit then build succeeds; TOML edit then codegen --output of
  one output refuses naming stale siblings; tampered header caught; chain
  continuity violation caught; seed/migration artifacts never flagged by the
  planner-set invariant.

## Phase 7 — Imports

### 7.1 Declaration and reference syntax
- **What/Why/Verify:** As before (alias -> source + target schema; `alias:table`;
  alias resolution before dot-split; diagnostics) — plus scope stated: alias
  references are accepted ONLY in FK ref_table initially; appearing anywhere else
  (depends_on, groups) is a hard error naming the supported sites.

### 7.2 Surface snapshot and pinning
- **What/Why/Verify:** As before (per-object canonical hashes + source pin;
  `import lock` / `import update`; check --tag imports with column-level semantic
  drift; extensions inferred per referenced object; pg_version floor) — with the
  surface DEFINITION corrected: referenced tables PLUS the transitive closure of
  every type definition (enum/domain/composite/SM) their columns reference, each a
  0.4 registry-snapshot object with per-object hashes — without which 7.3's
  "imported enums usable in columns" and collision detection have no data.

### 7.3 Model integration
- **What/Why/Verify:** As before (ImportedTables split slice; explicit union
  wiring at the two non-TableByName sites — BuildFKGraph and seed's pool maps;
  collisions hard error; requirement re-declaration).

### 7.4 Downstream sweep
- **What/Why/Verify:** As before (app-only DDL; diff/migrate exclusions; live
  import verification VIA the 5.7 predicate executor; audit/design/orphan skips;
  codegen skips; seed tiers with count-wrapped offsets; D2/GraphQL reference
  shapes) — plus: introspect-side reconcile/diff --live treat configured [imports]
  target schemas as out-of-scope (nothing excludes them today; verify goal:
  reconcile over a DB containing the imported schema reports empty; confirm
  whether existing schema-scoping already mitigates); and the seed tier-2
  limitation stated: subquery-valued FKs render identical strings, so imported FKs
  inside UNIQUE constraints can spuriously exhaust the dedup retry limit —
  documented, with tier-1 as the answer.

## Phase 8 — Read API

### 8.1 DB-free serve mode
- **What/Why/Verify:** As before (pool optional; shared project-loading helper
  returning schema+registry+cfg; endpoint = the same canonical-serializer function
  as generate json, envelope incl. revision + FKGraph derived-view; nil-registry
  SM-drop fixed; DB-only endpoints degrade explicitly).

### 8.2 API hygiene
- **What/Why/Verify:** As before (--timeout enforced; audit job-start/poll; doc
  endpoint).

## Phase 9 — Visualization

### 9.1 Options plumbing (split dependency)
- **What/Why/Verify:** As before (config half after phase 0; serve half after
  phase 8; dagre/elk, theme, direction).

### 9.2 Enrichment
- **What/Why/Verify:** As before (conditional-generation layers; markers,
  tooltips, notes, enum rectangles).

### 9.3 Filtering
- **What/Why/Verify:** As before (globs, include-dependencies depth, summary mode,
  skipped edges, self-FKs preserved) — with the mechanism corrected: WalkCascade
  has NO depth parameter and its callback cannot early-cut; 9.3 adds a depth-aware
  walker signature to FKGraph rather than assuming the current shape supports it.

### 9.4 Cardinality
- **What/Why/Verify:** As before (native crow's-foot; 1:1/1:N inference; strict
  junction heuristic).

### 9.5 Heat maps and live stats
- **What/Why/Verify:** As before (fan-in/out stroke scale; caller-provided stats;
  generate stays DB-free).

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract. Unplanned by design.

---

## Dependency DAG

- 0 -> {1, 2, 3, 9.1-config-half}
- 0.3 -> 4.1; {0.3, 3.2} -> 4.2; 3.3 -> 4.2 (json envelope stamping); 4.1+4.2 ->
  4.3; 4.2 -> 6.2 (enforcement reads the stamps 4.2 writes)
- 0.7 -> {5.2, 5.7, 5.8} (normalization); 3 -> 5; 3 -> 7; 3 -> 8; {3, 0.2} -> 7.3
- 2 -> 6.1 and 2 -> 7.4 (new DB entrypoints bind the connection env from birth)
- {5, 0.6, 3, 4.2} -> 6; 5.7 -> 7.4 (predicate executor); 8 -> 9.1-serve-half
- Design-before-implement within 5: journal/view/position schemas (5.5) and the
  0.7 primitive precede 5.2's implementation; land order 5.1 -> 5.2 -> 5.3 -> 5.4
  -> 5.5 -> 5.6 -> 5.7 -> 5.8 -> 5.9 stands, with 5.2's upgrade command completing
  once 5.5's schemas exist.
- Parallelizable after phase 3: 4.1, 5, 7 (through 7.2), 8.

## Relationship to existing todos

- `infra-env-db-locator.md` — superseded by phase 2.
- `migrate-add-column-missing-if-not-exists.md` — superseded by phase 5.
- `genericize-diff-library.md` — resolved by phase 1.1.
- `partition-lifecycle-and-diff-library.md` — Part 1 = phases 1.2/1.3; Part 2 =
  resolved by 1.1's trigger decision.
- `cross-framework-schema-composition.md` — core = phase 7.
- `orxtra-codegen-deferred-remaining.md` — item 17 via phase 4 + DB CHECK; item 18
  = phases 3/6; item 20 = phase 6; item 19 dropped; items 21/22 out of scope.
- `visualization-and-web-ui.md` — its phases 1-5 = phase 9; web UI = phases 8/10.
- `rename-to-strictpg.md` — in todo/.obsolete/ per the no-rename decision.

## Out of scope, pending their own design rounds

- Test schema mode. N-project topology. Manifest + per-language linter ecosystem
  (evidence-gated). Recorded summit end-states: declarative catalog reconciliation
  for migrate; structural semantics/metadata split in the model; registry
  materialization into Schema as sole type-truth; extension-DDL-name resolution
  baked into the model at Build; DB/boot-time revision binding.

## Effort

Phases 0-2: 1-2 sessions each (0 grew: +0.7 normalization). Phase 3: 1-2. Phase 4:
2-3. Phase 5: 5-7 (largest). Phase 6: 1-2. Phase 7: 3-4. Phase 8: 1. Phase 9: 2-3.
Parallelization per the DAG.

Release: exactly ONE rlsbl release at the very end (global release-once rule);
everything accumulates unreleased; consumer todos filed at that release. No
intermediate state can reach a consumer.
