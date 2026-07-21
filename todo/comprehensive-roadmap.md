# Comprehensive roadmap: determinism, identity, migrate integrity, orchestration, imports, API, visualization

Single consolidated plan from the 2026-07-19 design session, revised 2026-07-21 after an
adversarial critique round (six investigation agents verified every grounding fact against
source and audited each phase; ~30 accepted findings folded in). This file is exempted
from todo immutability by explicit owner authorization — it is a living plan; git history
preserves prior versions. Supersedes/absorbs several active todos (see "Relationship to
existing todos" at the end).

## Decision provenance

Per the %% convention: decisions marked `[%%]` were trust-adopted (owner accepted the
recommended option) — weakly held, freely reversible, never to be cited as deliberate
intent. `[deliberate]` decisions were the owner's own.

- `[deliberate]` **No rename.** The project stays `pgdesign`. rename-to-strictpg todo
  moved to todo/.obsolete/.
- `[%%]` Compiler/live-layer seam: `build` and the core pipeline stay pure (TOML ->
  artifacts, no DB); live-DB functionality is a distinct tier behind a designed boundary.
- `[%%]` Ambition: summit-grade design only where outputs are permanent (schema identity,
  migration file format); pragmatic rungs elsewhere, maximal designs recorded as
  end-states.
- `[%%]` Canonical ordering lives in the IR (Build() orders; emitters forbidden to sort).
- `[%%]` Canonical serialization is semantic-only, full-model scope (object comments in).
- `[%%]` Revision hash = SHA-256 of canonical bytes; every-command enforcement with the
  full-regenerator/partial-writer refusal semantics (see 6.2).
- `[%%]` Migration precondition drift = hard error, always. No tolerance flags.
- `[%%]` generate --idempotent unifies with migrate verification via a predicate IR
  (one definition, Go executor + SQL renderer backends, CI conformance matrix).
- `[%%]` Migration history is append-only: files never rewritten; squash emits a NEW
  consolidation migration; superseded files archived; checksum verification
  unconditional once the format lands.
- `[%%]` Migration ops are self-contained: serializable fields ARE the rendering
  inputs; degraded ops unrepresentable.
- `[%%]` Migrations identified by revision pairs (from_revision -> to_revision) in a
  parent-linked chain; filenames cosmetic (sequence + slug); no version bumps.
- `[%%]` (critique round) Migration generation is PURE: diff(chain-head model, current
  model), no live DB; out-of-band drift surfaces at apply (preconditions/reconcile),
  never silently folded into generated migrations; intentional drift adoption via an
  explicit baseline-derived flow.
- `[%%]` (critique round) The chain's home is a small committed append-only MANIFEST
  (sequence, filename, from/to revision, superseded-by); consolidations are ADDITIONAL
  edges; archived originals stay reachable via the manifest, so mid-range databases are
  never stranded.
- `[%%]` (critique round) The per-op journal records op identity AND the serialized
  down-op as applied; rollback is fully database-driven and never consults files.
  Journal = one per-op table + a summary view (not two tables).
- `[%%]` Orchestrator: `pgdesign revise` = pure tier (build + migration generation),
  then DB tier (import verification + DB checks); seed separate; pure outputs kept on
  partial failure; separate commits; commit failure = hard error.
- `[%%]` Imports: split slices (fail-closed); `alias:table` syntax; vendored
  import-surface snapshots (per-object hashes from the canonical primitive + source
  pin); type/enum collision = hard error; app-only DDL with live verification;
  imported extension/pg_version requirements re-declared locally.
- `[%%]` Codegen branding, default-on breaking release: Python guarded StrEnum +
  enum-typed surfaces incl. the DB-read path; Go complete-boundary opaque struct;
  TS branded type + re-typed transition maps; Java/Kotlin value-based parse (+ JPA
  @Enumerated(STRING) fix); Zig wrapper struct + re-typed transition maps. Constants
  mode unchanged. Drizzle/sqlalchemy remain string-shaped by ORM necessity — stated
  exceptions. Import-time registry dropped.
- `[%%]` Seed with imported FKs: tiered real keys (live pool / count-wrapped offset
  subqueries in INSERT mode / hard error only for offline+COPY+NOT-NULL).
- `[%%]` strictcli: new "connection env" kind (hermetic-suppressed, lazy, no default);
  PGDESIGN_DB declared once; all --db flags bind it; provenance via the EXISTING
  Context.Source(); DB checks skip visibly under --hermetic.
- `[%%]` Partition: premake required; opt-in `schedule` key wires the pg_cron helper;
  missing schedule without acknowledgment = warning.
- `[%%]` pkg/diff deleted; promotion trigger recorded (second flat-schema consumer).
- `[%%]` Web UI frontend deferred; only the DB-free API contract is built.

## Grounding facts (verified in source 2026-07-19; corrected by critique 2026-07-21)

- Raw model.Schema is NON-deterministic: resolveTable builds per-table collections by
  ranging Go maps (build.go ~563-644). SQL/JSON/doc/d2/graphql/python-ddl emitters
  re-sort per-table collections via model.Sorted* — but ordering has TWO semantics:
  alphabetical (the 7 Sorted* helpers) and TOPOLOGICAL (tables via Build; views/
  matviews/functions topo-sorted in the SQL emitter via graph.TopoSort — correctness,
  not just determinism). Top-level collections are inconsistently ordered between JSON
  (name-sorted) and DDL (declaration/topo). Matview indexes are unsorted on EVERY path.
  gorm/drizzle/jpa/sqlalchemy codegen and validator policy extraction emit in raw map
  order (stable only while tables have <=1 of each item). The existing determinism test
  cannot catch any of this.
- Two divergent JSON serializers (generate json sorts; serve emits raw). FKGraph and
  TablesByName are json:"-". FKEdge has NO schema field. FilterByGroups/FilterBySource
  rebuild TablesByName but NOT FKGraph — filtered schemas carry stale graphs
  referencing excluded tables.
- semtype.Registry: separate from Schema, unexported/unserializable; scalar CHECKs and
  builtin shadowing live only there; typeDefsEqual ignores BOTH Comment and Source, so
  a snapshot needs its own explicit field policy. Enums/domains/composites/SM types
  exist BOTH as registry TypeDefs and as Schema fields (duplication the canonical form
  must resolve).
- Headers: THREE wordings plus a seed variant across ~41 codegen sites + 6 validator
  helpers + 5 CLI sites + a separate codegenHeader/hasCommentHeader CLI path; the
  standalone single-file codegen path writes NO header at all (divergence from the
  planner path). codegen --check is byte-exact (pkg/genkit).
- Migrate: tracking table = version/applied_at/checksum/description; checksum is over
  migration FILE bytes and NEVER verified; no per-op records; version row written LAST
  (committed phases and non-transactional ops leave real DDL with no durable record;
  re-apply restarts at op 0). Rollback re-reads files, trusting them over the DB. Ops
  have no stable identity. ALL pointer-def op families (tables, views, matviews,
  composite types, domains, policies, functions, triggers, sequences) are
  non-serialized: after disk round-trip most degrade to comment no-ops, sequences lose
  parameters, and create_function/create_trigger fall back to emitting the WRONG OBJECT
  (deny-mutation function / append-only trigger). Only create_table has a mitigation
  (ConsolidatedOps). No round-trip tests beyond create_table.
- Squash deletes/rewrites originals (saferm + rename over <to>.toml); the
  applied-version guard (M200) runs only if --db is voluntarily passed; tracking rows
  orphaned; zero CLI-flow tests.
- migrate generate requires --db and --version today; NO ledger/manifest exists;
  discovery skips non-semver filenames and ~7 functions rely on semver ordering;
  migrations-dir sentinel bug (explicit `--dir migrations` == default).
- Introspect has NO table-level filtering at all (pgdesign_migrations reported as a
  user table); deny-mutation function and _pgdesign_sm_ prefix ARE filtered. The
  differ compares expressions by raw string equality while introspect returns
  PG-rewritten forms (pg_get_constraintdef/expr/indexdef) — only types have real
  normalization; the shadow test survives on easy fixtures only.
- serve: hard DB-coupled at construction; --timeout registered but never enforced;
  audit endpoint synchronous TANE; serve calls GenerateD2 with a nil registry (state
  machine diagrams silently dropped); serve's local TOML loader discards the registry;
  project-loading helpers live in package main.
- strictcli: CheckContext exposes only ProjectRoot(); the check command constructs a
  fully-populated *Context and discards it (widening = interface change + reconciling
  two construction paths); infra roots and handshake envs are hermetic-IMMUNE, flag
  Env() hermetic-SUPPRESSED — no primitive fits a connection URL; per-flag provenance
  ALREADY EXISTS (Context.Source(), env>config>default precedence). 16 --db flags with
  three different default semantics.
- Codegen enum shapes: Go `type X string` (open), TS literal union (structural),
  Python StrEnum (str subclass, value-construction open), Java/Kotlin real enums
  (closed), Zig bare string consts. No parse helpers anywhere. TS AND Zig transition
  maps use raw string keys. JPA emits no @Enumerated — Hibernate defaults to ORDINAL
  (latent bug). Python query-layer PgBackend reconstructs rows with no enum coercion
  (a branding read-door). drizzle pgEnum infers string-literal unions; sqlalchemy
  keeps str columns.
- CI: postgres:17 + pg_partman, PGDESIGN_REQUIRE_DB=1; ~11 of ~133 migrate-package
  tests are DB-backed.
- Partition bugs: python_ddl.go passes Retention as p_interval (sibling of the v0.24.4
  generate fix); omitted premake -> p_premake := 0; silent skip without pg_partman;
  manual children + maintenance emit contradictory DDL; PartmanRunMaintenanceCron() is
  dead-but-tested code.
- pkg/diff: zero importers; internal/diff matcher generic; result types embed ~22 PG
  model types consumed field-by-field by migrate (~350 typed accesses).

---

## Phase 0 — Foundational groundwork

Everything later stamps, hashes, compares, or filters; phase 0 makes the substrate
honest so later phases inherit honesty instead of re-implementing it.

### 0.1 Canonical ordering in the IR
- **What:** Two order semantics, both moved into Build(): ALPHABETICAL — the seven
  Sorted* sorts move to construction (same comparators, so per-table output is
  unchanged); TOPOLOGICAL — views/matviews/functions get their topo order (with
  input-order tie-break via graph.TopoSort) computed at Build like tables already are.
  Tables stay topo-ordered (never alphabetized — correctness); columns stay
  source-ordered (semantic). ONE canonical top-level order adopted by both JSON and
  DDL (= the IR order). Then delete ALL emitter-side sorting and the Sorted* helpers.
  Fix in the same stroke: matview index ordering (nondeterministic everywhere), the
  ORM codegen generators + validator policy extraction (luck-stable), and replace the
  too-weak determinism test with a build-twice-compare-bytes CI test + Build
  postcondition.
- **Why:** The revision hash is a hash of bytes; nondeterministic bytes make identity
  meaningless, freshness flappy, diffs dishonest. Ordering as an IR property makes
  every current and FUTURE emitter deterministic by default — "forgot to sort" (four
  live instances found) becomes impossible. Distinguishing alphabetical from
  topological protects DDL correctness (a view must be created after the view it
  references, regardless of name).
- **Verify:** Determinism test red before / green after; goldens byte-stable; fixture
  with 2 matview indexes + multiple FKs/policies per table stable across runs; a
  view-references-view fixture still emits in dependency order; grep finds no
  emitter-side sorting.

### 0.2 Schema-qualified identity keying
- **What:** FKEdge gains a schema field (struct change, not just key format); rekey
  FKGraph maps, cascade walkers, and group resolution to (schema, name). Fix the
  stale-graph bug: FilterByGroups/FilterBySource must recompute derived structures
  (FKGraph, TablesByName) for the filtered subset.
- **Why:** Two identity schemes for one object is a latent bug today (same-named
  tables in two PG schemas collide in cascade analysis) and a guaranteed bug under
  imports (foreign schemas by definition). The filter bug hands phase 9.3 and 7.3 a
  graph referencing excluded tables. Fix identity before building on the graph.
- **Verify:** Red-green: same-named tables in two schemas through cascade depth
  (W013/W014/W015), workload analysis, and group filtering; filtered schema's graph
  contains no excluded tables; suite green.

### 0.3 Header consolidation (byte-preserving)
- **What:** One shared parameterized header helper routed through ALL sites — the ~41
  codegen sites, 6 validator helpers, 5 CLI sites, the codegenHeader/hasCommentHeader
  path, the headerless standalone-codegen path (via 0.6), and seed's variant —
  REPRODUCING each site's current wording byte-for-byte. No wording change here: the
  unification to one wording lands in 4.2 together with the revision line, so
  consumers regenerate once, not twice.
- **Why:** Phase 4 stamps a revision into every header and phase 6 enforcement reads
  the stamps; stamping through 40+ scattered literals with four wordings means that
  many chances to miss one, and a missed stamp is invisible to divergence enforcement.
  Byte-preservation keeps this phase consumer-invisible — the roadmap's own one-break
  principle applied to itself.
- **Verify:** All outputs byte-identical before/after (freshness checks stay green on
  consumer fixtures); grep finds zero header literals outside the helper.

### 0.4 Type-registry snapshot
- **What:** Deterministic, ordered, exported snapshot accessor on semtype.Registry +
  reconstruct-from-snapshot. Explicit field policy of its own (it cannot mirror
  typeDefsEqual, which ignores both Comment and Source): semantic fields + Comment
  included, Source excluded, builtin-sourced entries excluded (shadowing flips Source
  to "user", so shadows survive).
- **Why:** The registry holds semantic state existing nowhere else; an identity
  omitting it calls two different schemas "the same." The snapshot bridges the
  registry into the canonical form and later carries type definitions across imports.
- **Verify:** Snapshot -> reconstruct -> snapshot byte-stable; independent of
  registration order; Source relabeling does not change the snapshot.

### 0.5 Introspect filters managed objects
- **What:** Managed-object exclusion by pattern/explicit list (pgdesign_% tables —
  covering the tracking table AND the future phase-5 journal automatically), unified
  with the existing function/trigger filters under one "managed objects" concept.
- **Why:** Reconcile-verify and the shadow test demand "introspect reality, diff,
  expect empty" — unusable if the tool's own bookkeeping registers as drift. Pattern-
  based filtering means new managed objects inherit coverage instead of each one
  reintroducing the false-drift bug.
- **Verify:** DB-backed: introspect a migrated database (tracking table + journal
  present), diff against desired, empty.

### 0.6 One write path; sentinel fix
- **What:** Consolidate multi-file write + owned-dir/orphan bookkeeping onto the
  planner implementation; standalone codegen becomes a thin caller — which also fixes
  its single-file path writing NO header (byte-divergence from the planner path).
  Fix the migrations-dir sentinel (explicit `--dir migrations` indistinguishable from
  default).
- **Why:** Phase 6 enforcement must guard EVERY write; two divergent write paths mean
  two guards that drift. The headerless standalone path would be a stamping blind spot.
  revise must know what the user actually asked for; a flag meaning two things poisons
  logic built on it.
- **Verify:** Standalone codegen and build byte-identical on a fixture (including
  headers), identical orphan behavior; sentinel red-green test.

## Phase 1 — Ground-clearing

### 1.1 Delete pkg/diff
- **What:** Remove the stub; changelog records the promotion trigger (second
  flat-schema consumer).
- **Why:** An exported API unusable without internal imports is worse than none; it
  costs trust and has zero importers. The trigger keeps the door honest, not closed.
- **Verify:** Package gone; build + vet clean.

### 1.2 Partition bug fixes (red-green each)
- **What:** python_ddl.go interval/retention conflation; premake required (hard parse
  error on omission); hard errors for non-RANGE + maintenance, maintenance without
  pg_partman declared, maintenance + manual children; part_config query failure
  becomes a diagnostic.
- **Why:** All are the silent-degradation class: configs that look accepted but
  produce broken/contradictory DDL discovered in production partitioning. Loud at
  compile time is the entire value of a schema compiler. The sibling-path miss
  (interval bug fixed in one emitter, not the other) is itself the argument for 0.6-
  style consolidation.
- **Verify:** Failing test first per bug; CI postgres+pg_partman coverage.

### 1.3 Partition lifecycle completion
- **What:** Introspection reads interval/premake/retention from part_config; diff
  distinguishes initial setup (create_parent) / retention-premake update (Safe,
  risk-classified) / interval change (hard error + guidance); migrate guards on
  extension presence; `schedule` key emits the pg_cron job via the dead-but-tested
  helper (pg_cron must be declared); no schedule + no acknowledgment = warning.
- **Why:** Partitioned tables are where the schema is alive; a tool that creates
  partman config but cannot see, evolve, or schedule it has automated the setup and
  abandoned the lifecycle. Dead helper wired up per dead-code policy.
- **Verify:** Golden DDL for schedule; diff/migrate tests per transition class; live
  introspect round-trip in CI.

### 1.4 Squash safety stopgap
- **What:** Until phase 5 replaces squash: --db and the M200 applied-version check
  become mandatory. Stated limits: this blocks legitimate offline squash of
  never-applied ranges (acceptable for the interim) and does NOT fix the rewrite/
  orphaned-row mechanics — phase 5 does. Includes the FIRST test of the squash CLI
  flow (none exist).
- **Why:** Squash today deletes files whose checksums production tracking tables
  record, with the DB check opt-in — a guardrail whose escape hatch is the default.
  "Fixed later" is not protection.
- **Verify:** Squash without --db hard-errors; overlapping applied versions refuses;
  CLI-flow test exists and passes.

## Phase 2 — Connection environment

### 2.1 strictcli: connection-env kind + check context access
- **What:** Third env primitive — hermetic-SUPPRESSED, lazily read, no implicit
  default. Check framework gains env access: this is an interface widening of
  CheckContext plus reconciling the two context construction paths (the check command
  builds a fully-populated *Context and discards it) — more than "stop discarding."
  No new provenance machinery: per-flag source labels already exist
  (Context.Source(), env>config>default). Released as a strictcli version.
- **Why:** A connection URL is precisely what --hermetic should suppress, yet both
  existing primitives survive hermetic and flag Env() is unavailable to checks. The
  framework-level fix gives every strictcli consumer principled connection semantics.
  Reusing Source() avoids rebuilding what exists.
- **Verify:** strictcli tests: declaration, lazy read, hermetic suppression,
  check-side access; schema dump includes the new kind.

### 2.2 pgdesign adoption
- **What:** Declare PGDESIGN_DB once; bind all 16 --db flags (normalizing their three
  different default semantics); provenance surfaced via Source(); checks read via the
  framework; --hermetic makes DB checks skip visibly. The config-file [database].url
  remains a separate, explicit resolution layer (documented precedence).
- **Why:** One variable, one story: today checks honor the env var while commands
  ignore it, and the raw getenv is invisible to --help/schema. Provenance replaces
  forced retyping as the explicitness mechanism.
- **Verify:** Env-only invocation works on every DB command with a provenance line;
  hermetic run shows explicit skips; raw os.Getenv gone from cmd/ (test harness
  excepted); documented precedence test (cli > env > config).

## Phase 3 — Schema identity

Summit-grade foundation (permanence test: stamped into migration files, headers,
tracking tables, import snapshots — v1 haunts forever).

### 3.1 Canonical serialization — compositional, per-object primitive
- **What:** The PRIMITIVE is per-object canonical serialization: each schema object
  (table, view, function, type definition, ...) serializes independently to canonical
  JSON with EXPLICIT key ordering (never struct reflection order) and a deliberate
  omit-unset-optional policy. The whole-model form = versioned preamble + ordered
  concatenation of per-object forms (order per 0.1). Content policy: SEMANTIC-ONLY —
  registry snapshot (0.4) is the source of truth for type definitions (schema-side
  duplicates like StateMachineTransitions excluded as derived; CycleGroups excluded as
  derived; FKGraph/TablesByName/candidate-key caches excluded); builtin-sourced
  registry entries excluded; object comments IN (they emit COMMENT ON), TOML
  formatting comments OUT (parse-layer); [suppress] config and extregistry outside
  identity (config, not model). Explicit "registry absent (introspected)" marker for
  schemas without a registry. Format version field.
- **Why:** One primitive, three consumers — whole-model identity (3.2), per-object
  import-surface hashes (7.2), API payload (8.1). Subsetting whole-model bytes cannot
  yield context-independent per-object hashes; without the compositional design,
  imports would grow a second serialization dialect — the exact disease 3.3 kills.
  Explicit key ordering is what makes "byte-identical across struct refactors"
  actually achievable; the omit-unset policy is what keeps unused new features from
  churning every hash on upgrade.
- **Verify:** Byte-identical across repeated builds AND across a struct-field-order
  refactor test; per-object bytes independent of neighbors/position; golden fixture;
  comment edit changes bytes; Source relabeling does not; registry-absent marker
  distinguishes introspected from built-empty.

### 3.2 Revision hash
- **What:** Revision = SHA-256 of the whole canonical stream; per-object hashes =
  SHA-256 of each object's canonical bytes (same primitive). Exposed from model;
  surfaced in CLI output. Stated policy: a pgdesign upgrade that changes the model
  schema flips all revisions and forces one coordinated regeneration (the existing
  consumer-regeneration convention, now load-bearing by design). Conformance test,
  one-directional: revision-equal implies diff-empty (the reverse is not asserted).
  Diff fast path: equal revisions skip the diff.
- **Why:** The revision is the coupling primitive of the roadmap — what migration
  files, headers, tracking table, and enforcement agree on. The hash-implies-diff
  conformance test makes the serializer and the differ police each other's semantic
  field coverage for free.
- **Verify:** Sensitivity tests (comment/column/registry changes flip it; no-op
  rebuild doesn't); conformance test wired into CI; fast path exercised.

### 3.3 One serializer everywhere
- **What:** generate's json format and serve's schema responses call the SAME
  canonical-serializer function (plus endpoint wrappers); divergent serializers die.
  Introspect-sourced responses carry the registry-absent marker.
- **Why:** Two serializers for one struct is how the nondeterminism bug survived
  unnoticed. Any consumer must see THE schema, not "the schema according to this
  endpoint."
- **Verify:** generate json and serve bodies structurally identical for the same
  schema; introspect-path response carries the marker; golden updated once.

## Phase 4 — Codegen breaking release

One coordinated consumer-facing break carrying branding, wording unification, and
revision stamping — consumers regenerate and adapt exactly once.

### 4.1 Branded types per language — full surface enumeration
- **What:** Shared mechanism first: extend the enum_gen dispatch seam (one shared
  value/naming model, six small per-language emitters) — not six independent efforts.
  Per language: Go opaque struct (unexported field) with COMPLETE boundary —
  constants as sole constructors, erroring Parse, rejecting UnmarshalJSON/Text +
  sql.Scanner, emitting Valuer/MarshalJSON/Stringer, detectably-invalid zero value.
  Python: StrEnum retained, implicit value-construction closed, parse() classmethod
  as the only dynamic entry, __reduce_ex__/pickle override (Enum reconstructs via
  cls(value)), query-layer and validator signatures enum-typed, AND the PgBackend
  DB-read path routes through parse() — branding must close the read door, not just
  the write door. TS: branded string type + parse; type-safe transition maps re-typed
  off raw string keys. Java/Kotlin: value-based parse added; JPA additionally gains
  @Enumerated(EnumType.STRING) (fixes the latent Hibernate-ORDINAL default bug) in
  the same break. Zig: wrapper struct + parse; Zig transition maps re-typed (a
  three-site change: resolver, value emitter, transition maps). Stated exceptions:
  drizzle pgEnum and sqlalchemy columns remain string-shaped by ORM necessity;
  constants mode (name strings) unchanged — constants can only name valid states.
- **Why:** The drift class (consumer names a state the schema lacks; runtime crash at
  the DB) dies when invalid values cannot be NAMED or SMUGGLED: compile error where
  expressible, boundary error at every ingress (JSON/DB/string), DB CHECK backstop.
  The read-door fix matters because a branded field holding a raw str from the DB
  would falsify the brand's promise exactly where consumers trust it most. The
  ORM exceptions are stated so the brand's coverage map is honest.
- **Verify:** Per language: constructing an invalid value fails at the earliest
  boundary; Go fixture proves all four ingresses reject; Python round-trips through
  pickle and through PgBackend yielding enum-typed (not str) fields; TS/Zig
  transition maps reject unknown keys at compile time where the language allows;
  type-checkers pass on generated fixtures where toolchains exist in CI.

### 4.2 Header unification + revision stamping
- **What:** The shared header (0.3) adopts ONE wording (Go's machine-readable "Code
  generated ... DO NOT EDIT." convention, per-language comment prefix) and gains the
  revision line + a stamp format-version — landing together as one header rewrite.
  Per-artifact-class stamping: comment-stamped (sql, d2, graphql, codegen, seed);
  in-band-stamped (json — the canonical form carries revision as a data field beside
  format_version; a comment channel doesn't exist); .sqlsplit stamped as sql; svg
  structurally exempt (non-deterministic rendering — documented as outside freshness
  AND stamping). Stated cost: partial regeneration becomes impossible — one schema
  edit re-stamps every generated file (intended; the enforcement depends on it).
- **Why:** The stamp is how artifacts SAY which schema they came from — the raw
  material of phase 6's divergence guarantee. Wording + stamp together honor the
  one-break principle (0.3 kept phase 0 byte-preserving precisely so this single
  rewrite exists). The class enumeration exists because "every artifact carries the
  stamp" is only enforceable if the exceptions are named, bounded, and documented.
- **Verify:** Rebuild-without-change keeps freshness green; a schema edit flips every
  stampable output stale exactly once; json revision field present; svg documented
  exempt; stamp format-version present.

### 4.3 One coordinated breaking release
- **What:** Branding + wording + stamps in a single version bump; changelog entries
  typed breaking; regeneration notes for consumers (modes in the wild: python ddl
  faceted; python validators+constants; zig constants; generated SQL headers) —
  including the ADAPTATION notes, not just regeneration: TS consumers with exhaustive
  switch-on-string-literals stop compiling and must switch on branded constants;
  Python consumers constructing enums from raw strings must move to parse().
- **Why:** Three consumer-visible changes, one break, one adaptation — and honest
  notes because regeneration alone is not enough for TS/Python call sites.
- **Verify:** rlsbl changelog coverage passes with breaking entries; consumer
  drift-check scripts pass after regeneration.

## Phase 5 — Migrate integrity

Internal sub-ordering is load-ordered and explicit (enforcement must not precede the
format it enforces).

### 5.1 Self-contained ops
- **What:** ALL pointer-def op families (tables, views, matviews, composite types,
  domains, policies, functions, triggers, sequences) become self-contained:
  serializable fields ARE the rendering inputs; generator builds ops from serializable
  data only; every comment-stub no-op AND every wrong-object fallback (create_function
  emitting deny-mutation; create_trigger emitting append-only) is DELETED; sequences
  keep parameters. OpToSQL becomes a total function of the on-disk form for EVERY op
  type. Table-driven round-trip test per op family (generate -> write -> re-parse ->
  assert byte-identical SQL); write-time round-trip remains as an invariant test.
- **Why:** A migration file that renders different SQL than intended — empty, or the
  WRONG OBJECT — is the worst artifact the tool can produce, and today it can produce
  it for nine op families. Total-function ops make the degraded state unrepresentable
  instead of guarded; the per-family test makes the claim checkable rather than
  asserted (the earlier single-fixture verify goal would have passed while views
  silently no-op'd).
- **Verify:** Round-trip table test covers every op family; wrong-object fallbacks
  gone (grep + tests); write-time invariant green.

### 5.2 Chain format, manifest, and adoption path
- **What:** New file format: sequence+slug filenames (cosmetic), from_revision/
  to_revision pair, parent linkage. The chain's home is a small committed APPEND-ONLY
  MANIFEST (sequence, filename, from/to revision, superseded-by) — doubling as
  lineage record and head source; chain-head/find-heads API exposed (genesis: empty
  chain -> null parent) for 6.1. Discovery/ordering rewritten off semver (today ~7
  functions rely on semver sorting and discovery skips non-semver names — sequence
  files would be silently ignored without this). Adoption path for existing users:
  a one-time explicit `migrate upgrade` — recomputes stored file checksums, backfills
  the grandfather-boundary revision (existing semver files become a linear chain
  prefix), and builds the manifest. Manifest<->files consistency check.
- **Why:** Revision pairs give migrations real identity tied to the schemas they
  transform; the manifest answers "where does the chain live" (scan-derivation breaks
  once 5.3 archives files) and later "which edge applies to this DB." The adoption
  path exists because without it, 5.4's unconditional checksums would hard-brick
  every existing production database on first contact (stored checksums are over file
  bytes; the format change rewrites every file).
- **Verify:** Round-trip parse/format; grandfathered history traverses correctly;
  upgrade command on a fixture with applied semver migrations yields a consistent
  manifest + verified checksums; manifest-files consistency check red on tamper.

### 5.3 Append-only squash (consolidation edges)
- **What:** Squash reimplemented: a consolidation migration is an ADDITIONAL chain
  edge (recorded in the manifest with superseded-by lineage); superseded files retire
  to an archive directory INTACT and remain reachable via the manifest — a database
  mid-way through a squashed range applies the remaining originals (edge matching its
  current revision), everyone else takes the consolidation edge. Tracking-table
  lineage handled: no orphaned rows. Files are never rewritten, period.
- **Why:** Mutation of applied artifacts stops being an operation the tool offers —
  the "file changed after apply" bug class becomes unrepresentable. The edge model
  (vs. plain archiving) exists because the critique proved plain archiving strands
  mid-range databases with no apply path; edges + manifest keep every reachable
  database resumable.
- **Verify:** Squash of applied migrations succeeds via consolidation; a DB mid-range
  (applied only the first of three squashed migrations) resumes via archived
  originals; fresh DB takes the consolidation edge; fully-applied DB skips it; no
  orphaned tracking rows; archive intact.

### 5.4 Unconditional checksums
- **What:** Only now — after append-only (5.3) and adoption (5.2) — checksum
  verification becomes unconditional on apply AND rollback: any mismatch = corruption,
  hard error.
- **Why:** Enforcement before the format it enforces would brick users (the original
  A2 finding); after 5.2/5.3, a mismatch has exactly one meaning — corruption — so
  the hard error is finally fair and absolute.
- **Verify:** Tamper test: edited file refuses apply and rollback with a precise
  report; upgraded-fixture applies cleanly.

### 5.5 Applied-op journal
- **What:** ONE per-op journal table (plus a summary view for "is migration applied";
  AppliedVersions reads the view) recording, as each op commits: op identity
  (migration ref, phase, sequence, op kind, target) AND the serialized down-op as
  applied. Covers per-phase commits and non-transactional breakouts. Re-apply resumes
  by skipping journaled ops. Covered by 0.5's managed-object filter automatically.
- **Why:** The version row is written LAST; every committed phase or non-transactional
  op before a failure is real DDL with no durable record, and re-apply restarts at op
  0 and aborts forever — the original bug behind this phase. Recording the DOWN-OP
  (not just identity) is what makes rollback fully database-driven (5.6): recorded
  reality, never file trust. One table + view (not two tables) keeps a single
  consistency domain, one filter, one thing for squash lineage to reference.
- **Verify:** DB-backed fault injection (mid-phase, post-non-transactional-op):
  re-apply resumes cleanly and completes; journal rows carry executable down-ops;
  summary view equals today's applied-versions semantics.

### 5.6 Journal-driven rollback
- **What:** Rollback executes the journal's recorded down-ops in reverse journal
  order — files not consulted at all (archived or not). Reversibility pre-check
  retained (irreversible ops block, journaled as such).
- **Why:** Rollback today re-reads files and trusts them absolutely — it will invert
  ops that never ran (the no-op-ADD/DROP-COLUMN data-loss case) or follow an edited
  file. Rolling back recorded reality makes both classes impossible, and journal-
  stored down-ops sever the file dependency entirely — which also simplifies 5.3
  (lineage is for apply-path selection and audit, not rollback).
- **Verify:** Rollback after partial apply drops nothing it did not create; rollback
  works with the source file archived; edited-file scenarios cannot influence
  rollback.

### 5.7 Normalization primitive, preconditions, predicate IR
- **What:** First, ONE shared comparison/normalization primitive (types, defaults,
  and expressions — parse/deparse both sides via the existing go-pgquery wrapper) used
  by the differ, the preconditions, and the shadow test. Then preconditions: before
  each op, a predicate against pg_catalog asserts expected prior state per op class
  (absent for creates; present-and-matching for alters/drops), hard error naming
  object/expected/found; DML ops (backfill, transform, batched loops) explicitly
  precondition-free — arbitrary SQL has no catalog precondition. Predicates defined
  once as structured data (catalog query + expected shape) with two backends: the Go
  executor (apply-time) and a SQL renderer compiling the same structure into
  DO-blocks for generate --idempotent (which thereby RAISEs on definition mismatch
  instead of silently skipping). CI conformance matrix: both backends AND the differ
  (as a third leg where object classes overlap) against the same live database
  states, asserting identical verdicts.
- **Why:** Three comparison engines exist by the end of this phase (differ,
  precondition executor, SQL guards); without one shared normalization they disagree
  on the same object — and the raw-string comparison the differ uses today already
  disagrees with PG's rewritten forms (status IN (...) comes back as = ANY(ARRAY)).
  The predicate IR dissolves the second-source-of-truth objection structurally (one
  definition, two compilations) and the matrix makes non-drift a TESTED property.
  DML carve-out because pretending arbitrary SQL has a catalog precondition would be
  theater.
- **Verify:** Normalization unit suite (PG-rewritten forms equal their sources);
  DB-backed precondition matrix per op class (wrong-type column, missing table,
  mismatched constraint — each precise); golden idempotent SQL; DB test: mismatched
  pre-existing column makes the idempotent script RAISE, matching state no-ops;
  conformance matrix green in CI.

### 5.8 Post-apply reconcile-verify
- **What:** After apply: introspect (0.5 exclusions) + diff against the target model
  — gated on 5.7's normalization; any residual mismatch = hard error listing every
  divergent object. SM-vs-enum introspection lossiness documented.
- **Why:** Preconditions check ops locally; reconcile checks the combined result
  globally — out-of-band changes mid-migration, op interactions, generator bugs.
  Reuses the real differ, so coverage is complete with zero bespoke verification
  code. Gating on normalization is what keeps it from being flaky-by-construction on
  real schemas (the un-normalized shadow test survives on easy fixtures only).
- **Verify:** Clean apply over a fixture CONTAINING check constraints, partial
  indexes, and policies reports empty (the current differ fails this); out-of-band
  ALTER mid-migration surfaces in the report; revision-equal-implies-diff-empty
  conformance asserted here too.

### 5.9 Pure chain-based migration generation + ecosystem alignment
- **What:** migrate generate becomes PURE: diff(chain-head model, current model) —
  the prior model reconstructed from the chain; no --db for generation. Drift is
  caught at apply (5.7/5.8), never folded into generated migrations. Intentional
  out-of-band adoption goes through an explicit baseline-derived flow (baseline
  updated for chain format). Shadow test, serve migrations endpoints, and
  migration-guide docs updated for format+journal+manifest.
- **Why:** Generation from live introspection makes the same TOML edit produce
  different migrations depending on DB state — silent drift absorption, against the
  drift decision — and drags a DB dependency into what is conceptually a pure
  function of intent history. Chain-based generation completes the purity seam
  (6.1's tier story becomes true) and gives drift exactly one loud channel.
- **Verify:** Generation without any DB produces the correct migration from a chain
  fixture; a drifted DB does NOT alter generated output but fails apply with the
  precondition report; baseline flow adopts intentional drift explicitly; full
  migrate suite (unit + expanded DB-backed) green; shadow test passes on the
  comprehensive fixture.

## Phase 6 — Orchestration and enforcement

### 6.1 pgdesign revise
- **What:** New top-level command. PURE tier: build planner + chain-based migration
  generation (5.9) — parent = chain head from the manifest (hard error on two heads,
  both named; genesis handled). DB tier: live import verification (7.4) + DB checks
  (nf, workload). Separate safegit commits: pure outputs, then migration+manifest.
  Partial failure keeps committed pure outputs and exits loudly naming the skipped
  tier. Commit failure = hard error (build's warn-and-continue fixed the same way).
- **Why:** The forgotten-step failure mode is real (four commands per schema change).
  revise is "I edited the TOML — make everything consistent and tell me what's
  wrong," without eroding build's purity: with 5.9, even migration generation is
  pure, so the DB tier is exactly the genuinely-live work. Chain-derived identity
  removes invented version ceremony. Separate commits reflect the two artifact
  lifecycles (regenerable snapshots vs append-only ledger).
- **Verify:** End-to-end: edit TOML -> revise -> regenerated outputs + chained
  migration + manifest entry, two commits, one revision everywhere. DB-unreachable
  run: pure tier complete and committed, non-zero exit naming the skipped tier.
  Two-head fixture errors.

### 6.2 Revision enforcement (precise semantics)
- **What:** The invariant: all regenerable snapshot artifacts (the planner set) share
  exactly ONE revision after any write. FULL regenerators (build, revise) are always
  allowed — they re-stamp the entire set. PARTIAL writers (standalone codegen
  --output, single-format generate) refuse when artifacts they would NOT rewrite
  carry a different revision. Migration files are outside the invariant (append-only
  chain at historical revisions — the manifest check covers them). Missing or
  old-format stamps = stale (full regenerators proceed; partial writers refuse);
  stamp format-version makes the first post-upgrade run land via the coordinated
  4.3 break, not a lock-out. The revision CHECK is scoped to what the existing
  build-freshness check cannot see: chain/manifest integrity (revision-pair
  continuity, single head), cross-artifact stamp agreement (cheap, no re-plan), and
  standalone artifacts. genkit gains a stamp-extractor following its reporting shape
  — complementary to, not a replacement for, the byte-compare loop (byte-compare:
  "this file isn't what the model produces"; stamp-compare: "a sibling I'm not
  regenerating is at a different revision").
- **Why:** The naive rule ("refuse on stamp != current revision") deadlocks — after
  any TOML edit every stamp differs, and regeneration itself would be refused. The
  full/partial split captures the real invariant: divergence is created by PARTIAL
  writes, resolved by FULL ones. Scoping the check avoids duplicating checkBuild's
  byte-compare — overlapping guards drifting is the disease this roadmap fights.
- **Verify:** TOML edit then build succeeds (re-stamps all); TOML edit then
  standalone codegen of ONE output refuses, naming the stale siblings; tampered
  header caught by the check; chain-continuity violation caught; CI red on each.

## Phase 7 — Imports

### 7.1 Declaration and reference syntax
- **What:** [imports] config parsing (alias -> source + target PG schema);
  `alias:table` reference form; stated precedence: alias resolution BEFORE dot-split
  (colon is invalid in unquoted identifiers, so no grammar collision). Diagnostics:
  unknown alias, unresolvable target, collisions.
- **Why:** References should name the DEPENDENCY, not a physical schema string —
  provenance visible at the reference site, renames touch one line, a typo'd alias is
  a hard resolution error rather than a plausible-looking phantom schema.
- **Verify:** Parse/build tests; alias typo yields resolution error; precedence test
  (alias containing a dot-like target resolves correctly).

### 7.2 Surface snapshot and pinning
- **What:** Import-surface extraction (only referenced objects) serialized via the
  3.1 PER-OBJECT primitive into committed vendored snapshots with per-object hashes +
  source pin (git URL + ref) — no second serialization dialect. Lock/update
  subcommands (names need owner approval). `check --tag imports` re-derives the
  surface and reports semantic drift at column level, hard-failing CI. Requirement
  granularity: extensions inferred PER REFERENCED OBJECT from the surface (the
  extraction knows referenced types); pg_version carried as the framework's floor
  (consumer must re-declare >=).
- **Why:** Machine-specific committed paths are banned; unpinned imports drift
  silently. Content-addressed vendored snapshots give reproducible offline builds;
  SEMANTIC drift errors ("framework column X changed uuid->bigint, breaks
  app.users.principal_id") are what make pgdesign's pinning better than a generic
  lockfile — and they fall out of the per-object primitive for free.
- **Verify:** Two-project fixture: drifted referenced column type -> check names the
  exact column and breaking FK; unreferenced changes silent; offline build from
  snapshot; per-object hashes stable across unrelated framework edits.

### 7.3 Model integration
- **What:** ImportedTables split slice. Integrity machinery unions owned+imported —
  with the two non-TableByName consumers explicitly wired: BuildFKGraph (builds edges
  from s.Tables by bare RefTable — needs the union + 0.2 keying) and seed's FQN pool
  maps. Registry collisions between imported and local types = hard error naming both
  sources; imported enums usable in columns; extension/pg_version re-declaration
  enforcement per 7.2's granularity.
- **Why:** Fail-closed by construction: consumers iterating Tables get correct
  behavior BY OMISSION. But fail-closed only holds where resolution funnels through
  the union — the critique found the two bypass sites that would otherwise produce
  phantom graph nodes and dangling seed FKs; naming them is the difference between a
  principle and a bug.
- **Verify:** E204 resolves imported targets; FKGraph contains imported nodes with
  correct (schema,name) keys; seed resolves FK values against imported pools;
  DDL/audit/codegen outputs contain zero imported-table artifacts; collision and
  re-declaration tests.

### 7.4 Downstream sweep
- **What:** Generate emits app-only DDL with schema-qualified FK constraints.
  Diff/migrate exclude imported tables from add/drop; migrate generate's live import
  verification (referenced imports present and matching in the target DB — hard
  error otherwise) CONSUMES the 5.7 predicate IR Go backend rather than a bespoke
  check. Audit, design checks, orphan warnings skip imported tables. Codegen skips
  them. Seed: tiered real keys as a branch in the FK-value resolution path — tier 1
  (DB available): pre-populate the imported-table value pools from real keys
  (deterministic sorted selection; Zipf and COPY work unchanged); tier 2 (offline):
  count-wrapped ordered-offset subqueries in INSERT mode (OFFSET n %
  GREATEST(count,1) — fixed offsets overrun small tables; Zipf not available in this
  tier, stated); tier 3: hard error only for offline+COPY+NOT-NULL-imported-FK,
  naming all three constraints. D2/GraphQL render imported tables as minimal
  reference shapes so edges never dangle.
- **Why:** Ownership discipline end-to-end: the framework's objects are facts the
  app consumes, never regenerates, audits, or fabricates rows for. Predicate-IR reuse
  is the D3 finding: live import verification IS a catalog-predicate check — building
  it twice would be the duplication disease. The seed tiers follow one principle —
  imported rows are facts; seed never invents them — with the error surface being
  exactly the one true impossibility.
- **Verify:** Per-package fixture assertions; live verification via IR (present
  passes; absent and mismatched fail with specifics); seed tier tests incl.
  determinism, small-imported-table offset wrap, and the triple-constraint error;
  D2 golden compiles.

## Phase 8 — Read API

### 8.1 DB-free serve mode
- **What:** Pool optional; --db optional in project-schema mode. ONE shared
  project-loading helper returning (schema, registry, cfg) — extracted from package
  main, used by build/codegen/revise/serve (serve's current local loader discards the
  registry; that dies). The schema endpoint calls the SAME canonical-serializer
  function as generate json (3.3), returning canonical model + revision + an FKGraph
  projection — specified as a single deterministic (schema,name)-keyed DERIVED-view
  serializer built once in model (depends on 0.2), documented as a reconstructable
  convenience view, not a second truth. Fix the serve nil-registry bug (state-machine
  diagrams silently dropped on the serve path).
- **Why:** The seam made real: today even diagram endpoints demand production
  credentials. The endpoint is the compiler's half of the product boundary — and it
  is literally the same function as the json output format, so it can never drift
  from it.
- **Verify:** serve starts with no database and answers the model endpoint
  (byte-consistent with generate json); DB-only endpoints degrade with explicit
  errors; SM diagrams render on the serve path.

### 8.2 API hygiene
- **What:** --timeout becomes request-context enforcement; the synchronous TANE audit
  endpoint becomes cancellable and non-blocking (job-start/poll); doc format gets an
  endpoint.
- **Why:** A timeout flag that does nothing is a lie in the CLI surface; an unbounded
  synchronous FD-discovery endpoint is a self-DoS button. If the API is a designed
  boundary, its operational behavior is part of the design.
- **Verify:** Slow-audit test observes timeout/cancel; doc endpoint matches
  generate's doc output.

## Phase 9 — Visualization

### 9.1 Options plumbing (split dependency)
- **What:** D2 options struct threaded from config (depends only on phase 0); serve
  query-param plumbing for the same options (depends on phase 8's DB-free mode).
  RenderSVG parameterized: layout (dagre/elk — TALA excluded, not in the OSS
  library), theme, direction.
- **Why:** Every enrichment needs a config-to-generator path; the serve half is
  honestly sequenced (its diagram endpoints are DB-coupled until phase 8).
- **Verify:** Config round-trip; elk golden; serve query params exercised post-8.

### 9.2 Enrichment
- **What:** Conditional-generation layers (D2 native layers are separate pages, not
  toggles): index/unique markers, nullable indicator in the type column, comments as
  tooltips, checks as notes, RLS/append-only markers, enums as rectangles with value
  lists.
- **Why:** Diagrams omit most of what the doc format knows; layers are opt-out
  because show-everything is unreadable and hide-silently is wrong.
- **Verify:** Golden per layer; independently disableable; all goldens compile
  through the D2 library.

### 9.3 Filtering
- **What:** Include/exclude globs, include-dependencies depth — implemented on
  FKGraph.WalkCascade with a depth limit and the EXISTING filter helpers (with 0.2's
  derived-graph recomputation fix), summary mode (rectangles, names+edges); edges to
  excluded tables skipped; self-referential FKs preserved.
- **Why:** Large schemas need subset views; building on the fixed graph/filter
  machinery instead of parallel logic is the reuse the critique demanded.
- **Verify:** Goldens per mode; filtered output always compiles; depth-limited
  include-dependencies matches WalkCascade semantics.

### 9.4 Cardinality
- **What:** Edge block syntax with native crow's-foot arrowheads; 1:1 via unique/PK
  detection, 1:N default, M:N via the strict junction heuristic (exactly two FKs
  constituting the whole PK, no other columns).
- **Why:** Cardinality is the most-sought ERD information and currently absent;
  strict junction detection avoids false M:N collapses — the conservative direction
  for diagrams people trust.
- **Verify:** Golden per class; junction-with-extra-column NOT collapsed.

### 9.5 Heat maps and live stats
- **What:** Fan-in/fan-out from FKGraph on a fixed colorblind-safe stroke-based
  scale; live row-count/ratio annotations as caller-provided data (serve/CLI fetch);
  generate stays DB-free.
- **Why:** Hubs are where cascade risk concentrates and FKGraph already knows them;
  caller-provided stats keep the purity boundary intact.
- **Verify:** Heat map golden; injected-stats test; no DB import in generate.

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract. Unplanned by design; the seam
guarantees phases 8-9 need no rework when it wakes.

---

## Dependency DAG (replaces any linear reading)

- 0 -> {1, 2, 3, 9.1-config-half}
- 0.3 -> 4.1 (branding); {0.3, 3.2} -> 4.2 (stamping); 4.1+4.2 -> 4.3
- 3 -> 5; 3 -> 7; 3 -> 8; {3, 0.2} -> 7.3
- {5, 0.6, 3} -> 6 (6.1 additionally needs 5.2's chain-head API and 5.9)
- 5.7 -> 7.4 (predicate IR reuse); 8 -> 9.1-serve-half
- Parallelizable after phase 3: 4.1, 5, 7 (through 7.2), 8. Phase 9 (config half)
  parallelizable after phase 0.
- Within 5, the sub-order 5.1 -> 5.2 -> 5.3 -> 5.4 -> 5.5 -> 5.6 -> 5.7 -> 5.8 ->
  5.9 is load-ordered: enforcement (5.4) must not precede the format (5.2/5.3);
  rollback (5.6) needs the journal (5.5); reconcile (5.8) needs normalization (5.7).

## Relationship to existing todos

- `infra-env-db-locator.md` — superseded by phase 2.
- `migrate-add-column-missing-if-not-exists.md` — superseded by phase 5 (journal +
  preconditions solve the abort-loop without the silent-mismatch hazard).
- `genericize-diff-library.md` — resolved by phase 1.1.
- `partition-lifecycle-and-diff-library.md` — Part 1 = phases 1.2/1.3; Part 2 =
  resolved by 1.1's trigger decision.
- `cross-framework-schema-composition.md` — core = phase 7; coordination beyond live
  verification = out of scope below.
- `orxtra-codegen-deferred-remaining.md` — item 17 via phase 4 branding + DB CHECK
  (manifest/linter ecosystem out of scope, evidence-gated); item 18 = phases 3/6;
  item 20 = phase 6; item 19 dropped by decision; items 21/22 out of scope below.
- `visualization-and-web-ui.md` — its phases 1-5 = phase 9; web UI = phases 8/10.
- `rename-to-strictpg.md` — moved to todo/.obsolete/ per the [deliberate] no-rename
  decision.

## Out of scope, pending their own design rounds

- Test schema mode (extension stubs, relaxed constraints, fixtures).
- N-project topology beyond the two-project import case.
- Manifest + per-language linter ecosystem (evidence-gated).
- Recorded summit end-states: declarative catalog reconciliation for migrate
  (preconditions+journal+reconcile are the stepping stones); structural
  semantics/metadata split in the model (the 3.1 format already matches its bytes);
  registry materialization into Schema as the sole type-truth (3.1 names the interim
  winner per object kind); DB/boot-time revision binding (tracking table + consumer
  startup assertion).

## Effort

Phases 0-2: 1-2 sessions each. Phase 3: 1-2 sessions. Phase 4: 2-3 sessions (six
languages + consumer coordination). Phase 5: 5-7 sessions (largest; includes the
adoption path, manifest, and DB-backed test expansion). Phase 6: 1-2. Phase 7: 3-4.
Phase 8: 1. Phase 9: 2-3. Parallelization per the DAG can overlap 4.1, 5, 7, 8 after
phase 3 lands.
