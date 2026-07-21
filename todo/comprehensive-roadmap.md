# Comprehensive roadmap: determinism, identity, migrate integrity, orchestration, imports, API, visualization

Single consolidated plan from the 2026-07-19 design session, revised through four
adversarial critique rounds (each: independent investigation agents verifying
grounding facts against source and auditing every phase; accepted findings folded
in). This file is exempted from todo immutability by explicit owner authorization —
a living plan; git history preserves prior versions. Fully self-contained.

## Decision provenance

Per the %% convention: `[%%]` decisions were trust-adopted (owner accepted the
recommended option) — weakly held, freely reversible, never to be cited as
deliberate intent. `[deliberate]` decisions are the owner's own.

- `[deliberate]` No rename; the project stays pgdesign.
- `[deliberate]` ONE release for the whole roadmap, at the very end (global rule).
- `[deliberate]` No backward compat anywhere, ever (global rule); beware
  compat-in-disguise.
- `[%%]` Compiler/live seam: build and the core pipeline stay pure; live-DB work is
  a distinct tier.
- `[%%]` Summit-grade design only where outputs are permanent (identity, migration
  format); pragmatic rungs elsewhere with summits recorded.
- `[%%]` Canonical ordering lives in a shared Canonicalize() finalize routine
  invoked by ALL Schema constructors and the filter helpers.
- `[%%]` Canonical serialization: per-object primitive via a dedicated canonical
  encoder; semantic-only; full-model scope; Extensions + PGVersion included; type
  identity carried by the MODEL-LEVEL collections (both construction paths populate
  them); the registry snapshot covers only facts with no model representation
  (confirmed at implementation) — the earlier builtin-inclusion special case is
  withdrawn as redundant (builtin-derived domains already materialize into
  schema.Domains).
- `[%%]` Revision = SHA-256 of canonical bytes; the registry-absent marker lives
  INSIDE the hashed bytes and Revision is an opaque type whose cross-boundary
  comparison errors; stamp = full-project revision (provenance; byte-compare owns
  content); enforcement taxonomy: full regenerators / partial writers /
  source-editing writers.
- `[%%]` Migration precondition drift = hard error, always.
- `[%%]` Predicate IR: one structured definition; Go executor (structured
  diagnostics; shares introspect's catalog-query layer) + SQL renderer (generate
  --idempotent); CI conformance matrix incl. the differ. (Proposal to drop the Go
  executor REJECTED: it exists for structured diagnostics, not DB-freedom.)
- `[%%]` Migration history append-only; squash = consolidation edges; archive
  intact; checksums unconditional ON APPLY (incl. archived-original applies) once
  the format lands — the earlier "and rollback" clause is withdrawn: post-journal
  rollback reads no files, so a rollback checksum surface cannot exist.
- `[%%]` Ops are self-contained via the store: pointer-def ops REFERENCE target
  objects + transitive type closure BY CONTENT HASH; no inline blobs, no flat
  mirrors.
- `[%%]` Migrations identified by revision pairs in a parent-linked chain;
  filenames cosmetic (sequence + auto-derived slug, override flag).
- `[%%]` Chain home = one file per edge (migrations/chain/<from>-<to>.json).
- `[%%]` Store = content-addressed per-object files (migrations/objects/) +
  revision manifests (migrations/revisions/), implemented by ONE store package
  (internal/objstore) that also backs the imports vendoring (one package, multiple
  store roots).
- `[%%]` Visible directory names for committed load-bearing data.
- `[%%]` Journal = pgdesign_migration_ops + view pgdesign_applied_migrations
  (view carries a migration-level checksum column — serve selects it); records op
  identity AND serialized down-op; DB-driven rollback from the upgrade boundary
  forward; pre-upgrade prefix + baselines ROLLBACK-FROZEN.
- `[%%]` Position anchor = pgdesign_chain_position (current revision, in-progress
  edge ref, per-database boundary).
- `[%%]` Grandfather boundary = verify-then-stamp inside single-transaction
  advisory-locked migrate upgrade; assert-before-DROP; content-addressed file
  writes are idempotent and land BEFORE the DB commit (the sole commit point);
  the reverse window (files written, commit failed) is harmless BY THAT PROPERTY.
- `[%%]` Pure migration generation: diff(head revision manifest, current model);
  ALWAYS emits large-table-safe forms; drift surfaces at apply; adoption via
  baseline.
- `[%%]` Orchestrator `pgdesign revise`: PURE tier = build + generation + PURE
  checks (static NF audit, structural workload — both blocking; today --strict-nf
  blocks generation and revise must not regress that); DB tier = import
  verification + LIVE checks (TANE discovery, pg_stat workload) — non-retroactive.
  Separate commits; commit failure hard error; pure outputs kept on partial
  failure.
- `[%%]` Imports: split slices, fail-closed; `alias:table` in FK ref_table only;
  surface = referenced tables + transitive composition-closure of type
  definitions; per-object hashes via objstore; source pin = git URL + ref;
  import lock / import update; collisions hard error; requirements re-declared.
- `[%%]` Branding (single final release): Go opaque struct, validating boundary,
  var members; Python parse() alias + enum-typed surfaces + Row __post_init__
  coercion covering BOTH backends; TS keep-the-union + parse(); Java/Kotlin
  value-based parse + JPA AttributeConverter; Zig wrapper struct + re-keyed
  transition maps; sqlalchemy UPGRADED to native sa.Enum(PyEnumClass) (the "ORM
  necessity" exception was false); drizzle needs NO change (already
  pgEnum-typed — the earlier exception rationale was wrong); constants mode
  unchanged; constraints validators re-target the branded representation.
- `[%%]` Seed with imported FKs: tiered real keys; tier-2 hard error RESCOPED to
  UNIQUE constraints where the imported FK is the sole distinguishing column
  (composite UNIQUEs with an offline-distinct local column are legitimate); the
  existing silent-UUID pool-empty fallback becomes UNREACHABLE for imported FKs;
  tier-3 hard error = offline+COPY+NOT-NULL.
- `[%%]` strictcli: connection-env kind; CheckContext widening; registration-time
  hard error for unbound --db-class flags; provenance via Context.Source();
  generic todo filed at PHASE-0 START (external critical-path milestone).
- `[%%]` Partition: premake required; opt-in schedule key; unacknowledged missing
  schedule = warning.
- `[%%]` pkg/diff deleted; promotion trigger recorded.
- `[%%]` Web UI frontend deferred; only the DB-free API contract is built.
- `[%%]` Consumer regeneration+adaptation todos filed at the single final release.
- `[%%]` Header unification happens ONCE, in phase 0.1, with the final wording —
  the earlier byte-preserve-then-reword staging is withdrawn: the one-release rule
  already guarantees consumers see exactly one change; the two-step was double
  work that deliberately preserved a known-broken Go header.

## Grounding facts (source-verified across four critique rounds)

- resolveTable ranges Go maps — raw model order nondeterministic. Alphabetical
  (7 Sorted* helpers) vs TOPOLOGICAL ordering (tables in Build; views/matviews/
  functions topo-sorted in generate.go AND python_ddl.go; model table topo is a
  third path; internal/format's alphabetical-pre-sort + TopoSort is a FOURTH —
  and is the pattern to reuse for alphabetical tie-breaks). Topo tie-break is
  input-order; TOML declaration vs introspect ORDER BY name diverge; introspected
  functions lack DependsOn. Extensions ordering is another divergence axis (TOML
  declaration vs introspect ORDER BY extname) and Extensions are in identity.
  Matview indexes unsorted everywhere. Luck-stable raw-map emitters: gorm,
  drizzle, jpa, sqlalchemy, validator policy extraction, python query-layer.
  enrich() appends auto-FK indexes after resolveTable. The DDL emitter's
  determinism rests on inline Sorted* calls at ~7 sites — deleting emitter-side
  sorting touches the SQL emitter heavily; fixtures must cover DDL, not just
  JSON. Existing determinism test hand-builds structs and can never fail.
- Introspect constructs Schema directly (never Build): nil FKGraph/TablesByName,
  raw order. The copy-pasted finalize is FOUR steps; two
  (resolveStateMachineTransitions, resolveGroups) need raw/registry inputs
  Introspect lacks — Canonicalize absorbs ordering + derived structures; those
  two stay Build-side. pg_version has THREE resolution tiers (live > config >
  toml); the live tier is a DB input that cannot enter pure Build — only
  config+toml move into Build; ~10 cmd sites mutate schema.PGVersion post-Build
  today plus a second channel via generate.Options.PGVersion; a post-Build
  live-override seam must be defined. TablesByName keys schema.name (with a
  ".name" artifact for empty schema) while FKGraph keys bare names; group
  filtering keys bare t.Name — bare-to-qualified needs a stated multi-schema
  rule. FilterByGroups/FilterBySource shallow-copy and keep the stale FKGraph
  pointer.
- semtype.Registry: unexported/unserializable; typeDefsEqual ignores top-level
  Comment/Source but compares nested transition comments; builtins are
  scalar-with-CHECK; builtin-DERIVED DOMAINS MATERIALIZE INTO schema.Domains when
  used (identity coverage comes from the model collection, not the registry);
  enums/domains/composites/SM types exist in BOTH registry and Schema fields —
  identity reads the model side; TypeDef.Source doc comment stale. Type extends
  is eagerly inlined (closure = composition references only).
- Headers: 36 codegen sites (the 5 validator helpers are within them) + CLI
  planner-prepend for sql/d2/graphql (json and doc headerless) + seed's distinct
  wording; 7+ wordings; hasCommentHeader lacks `--`; Go headers don't match the
  ^// Code generated .* DO NOT EDIT\.$ tooling regex; genkit's Generator/
  MultiFileGenerator interfaces are DUPLICATED in internal/codegen. codegen
  --check is byte-exact. splitfmt is sealed (line 1 = statement count). fmt
  rewrites schema TOML (--column-order = revision change); introspect --output
  writes source. build applies per-output FilterByGroups/FilterBySource;
  standalone codegen does NOT; build auto-commit warns-and-continues.
- Migrate: tracking table version/applied_at/checksum/description; checksum over
  file bytes, never verified; version row last; no per-op records; TWO divergent
  tracking write paths (state.go RecordMigration/RemoveMigration vs inline SQL in
  apply/rollback) to reconcile. Rollback re-reads files. THIRTEEN unserialized
  op-family concerns: nine pointer-def families + RawSQL + PartitionChildSpec +
  ParentTable + the 1.3-introduced partman-config ops (update_partman_retention/
  premake fall to OpToSQL's default comment-stub TODAY). Down-ops embed def
  pointers too. opCreateTable passes nil enum/domain lists and hardcodes
  pgVersion=0 despite DDLOp.PGVersion. Generation consumes live TableStats to
  pick NOT VALID splits and expand/contract forms; deleting stats also removes
  the EXPAND_CONTRACT_TYPE_NARROW advisory warning (relocate it).
  IsNonTransactional: create/drop_index_concurrently + version-conditional
  enum-add (transactional PG12+). An INTERRUPTED CREATE INDEX CONCURRENTLY
  leaves an INVALID index of the target name — IF NOT EXISTS then skips it
  forever (and sql.go's comment claiming CIC+IF NOT EXISTS is
  version-incompatible is wrong; valid since PG 9.5). drop_index_concurrently
  renderer lacks IF EXISTS. Advisory lock is session-level, shared by
  apply/rollback/baseline, held across reopened transactions. BASELINE EXISTS
  (baseline.go; "baseline" checksum literal; semver-based divergence guards that
  must be re-expressed against chain reachability). SHADOW TEST EXISTS
  (handlers_migrate.go:987-1133). The differ is purely STRUCTURAL — backfill/
  transform DML and RawSQL ops are synthesized at generation and NOT recoverable
  from revision manifests. The differ is BLIND to PGVersion (in identity, not in
  SchemaDiff — a latent bug: pg_version changes alter emitted DDL invisibly to
  diff). The differ DOES compare comments and extensions.
- Squash deletes/rewrites originals; M200 only with --db; orphaned rows; zero
  CLI tests; optimizeDDLOps keeps only the final type-change's down.
- migrate generate requires --db + --version; no ledger; discovery skips
  non-semver names; migrations-dir sentinel at 8 migrate sites + 1 serve site;
  serve's handleMigrations has an existence guard (200 with [] — NOT a 500 — the
  repoint is still required, the urgency was overstated); version endpoint opens
  version+".toml". serve returns {schema, diagnostics} — the unified envelope
  must WRAP diagnostics, not drop them.
- Introspect: no table-level filtering; `_pgdesign_sm_%` leading-underscore
  function/trigger pattern (a pgdesign_% table pattern does not cover it); view
  coverage needs its own filter treatment (design note). Introspect already
  contains pg_catalog queries for every object class the predicate executor
  needs — the executor must SHARE that catalog-query layer, not duplicate it.
  Differ compares expressions raw vs PG-rewritten forms (live false-drift bug);
  only types normalize; explicit schemaNames scoping exists in handlers.
- serve: DB-coupled; --timeout ignored; audit synchronous; GenerateD2 called
  with nil registry; local loader discards registry; project-loading helpers in
  package main (destination: internal/project). Phases 5 and 8 both touch
  internal/serve/handlers.go — ordering required, not parallel.
- strictcli: check command discards its built *Context; roots/handshakes
  hermetic-immune, flag Env() suppressed; Source() exists. SIXTEEN
  StringFlag("db") + one StringFlag("live"), three default semantics — all bind.
- Codegen: Go `type X string` + const block (const of struct illegal — members
  become vars); TS literal union compile-closed (transition maps already typed);
  drizzle ALREADY emits pgEnum used as the column builder (typed — no change
  needed); sqlalchemy keeps str but sa.Enum(PyEnumClass) is native (upgrade,
  not exception); Python Enum.__call__ already validates (no closing machinery;
  residual str-structural openness unclosable); Java/Kotlin real enums
  (UPPER_SNAKE names vs raw getValue() values — @Enumerated(STRING) persists
  NAMES); Zig string consts (transition maps use sanitized struct-field keys).
  ILLEGAL JAVA (multiple public types per file) in THREE modes: java_jpa,
  java_types, java_constraints. The conformance tests compile HAND-AUTHORED
  templates, never codegen output, and are DB-gated — provisioning toolchains
  alone would NOT catch the Java bugs; DB-free generated-fixture compile checks
  are a separate deliverable. Python query-layer neither imports nor defines
  the enum classes it annotates (survives via future annotations); BOTH
  PgBackend and InMemoryBackend build rows uncoerced — Row __post_init__ covers
  both; _constraints.py needs NO change (StrEnum str-equality). go_types and
  go_gorm both emit GenerateEnums into package schema (dedup must be
  co-generation-aware — gorm-only consumers rely on gorm's block). Seed content
  depends on --seed/--counts/--mode and is never freshness-checked (its stamp is
  unenforced provenance — stated). Seed's pool-empty fallback silently emits
  random UUIDs (seed.go:996-997) and the UNIQUE dedup keys the concatenation of
  ALL constraint columns with a fixed rowIdx retry then SILENT fall-through
  (seed.go:308-326).
- CI: postgres:17 + pg_partman; 11 DB-backed migrate tests of ~162.
- Partition bugs: python_ddl Retention-as-p_interval; premake -> 0; silent skip
  without pg_partman; manual children + maintenance contradictory; pg_cron
  helper dead-but-tested.
- pkg/diff stub: zero importers. internal/diff: ~22 model types consumed by
  migrate. internal/sqlparse is the go-pgquery leaf (imported by migrate,
  introspect, model, workload, testdb); sqlutil is imported by validate+codegen
  (normalization there would drag the WASM parser into both).

---

## Phase 0 — Foundational groundwork

Build order: 0.1 -> 0.2 (co-edited generator files) -> {0.3, 0.4}; 0.5/0.6/0.7
independent after 0.2. The strictcli todo (phase 2's external dependency) is
filed at phase-0 start for lead time.

### 0.1 Header unification (final wording, one pass) + stamp grammar
- **What:** One shared parameterized header helper homed in pkg/genkit (writer,
  reader, and stamp grammar co-located; internal/seed must stamp and cannot
  import internal/codegen; genkit's duplicated Generator/MultiFileGenerator
  interfaces in internal/codegen are absorbed in the same pass). The helper
  adopts the FINAL wording immediately — `Code generated by pgdesign. DO NOT
  EDIT.` (the Go-tooling-recognized convention; today's Go headers don't match
  it), per-language comment prefix, free-text parameter for seed's distinct
  wording — routed through all 36 codegen sites, the CLI planner-prepend path
  (sql/d2/graphql), codegenHeader/hasCommentHeader (which learns `--`), and
  seed. No byte-preservation staging: the one-release rule guarantees consumers
  see exactly one change, so two passes would be pure double work preserving a
  known-broken header. The stamp grammar (format + parser, format-versioned) is
  designed now; the revision line itself lands in 4.2 as a helper-INTERNAL
  addition (zero site re-touches).
- **Why:** Stamping through 36+ scattered literals with 7+ wordings means that
  many chances to miss one; a missed stamp is invisible to enforcement. Grammar
  and helper in genkit prevents writer/reader drift and solves seed's import
  problem.
- **Verify:** Grep: zero header literals outside the helper (negative check)
  AND the positive invariant: every generator output parses as beginning with
  the canonical stamp via genkit's parser; goldens updated once; Go headers
  match the tooling regex.

### 0.2 Canonical ordering via Canonicalize()
- **What:** A shared finalize routine: alphabetical ordering for per-table
  collections (incl. matview indexes), top-level type collections, AND
  Extensions (TOML-declaration vs introspect-extname divergence axis);
  topological ordering with ALPHABETICAL tie-break for tables/views/matviews/
  functions — implemented by reusing internal/format's existing
  pre-sort-then-TopoSort pattern (the fourth topo path, which becomes the
  blueprint; all four collapse here; introspected functions lack DependsOn and
  fall back to alphabetical); columns source-ordered; derived structures
  (FKGraph, TablesByName) built here. Invoked by Build, BuildMulti, Introspect,
  and FilterByGroups/FilterBySource. Scope split stated: Canonicalize owns
  ordering + derived structures; the two raw/registry-dependent finalize steps
  (SM transitions, group resolution) stay Build-side. pg_version: config+toml
  tiers resolve inside Build; the LIVE tier is a DB input — a post-Build
  live-override seam is defined for commands holding both a model and a
  connection, replacing the ~10 scattered post-Build mutations and the
  Options.PGVersion side channel. Sorts run post-enrich. Delete the 7 Sorted*
  helpers and ALL emitter-side sorting (the SQL emitter's ~7 inline call sites
  included — DDL fixtures must cover this, not just JSON). Fix the luck-stable
  emitters. Multi-iteration TOML->Build->serialize->compare CI determinism test
  (pinned iterations; >=2 entries per map-sourced collection) + Canonicalize
  postcondition. Stated: goldens change once.
- **Why:** The revision hash is a hash of bytes; nondeterministic bytes make
  identity meaningless. A shared finalize makes INTROSPECTED schemas canonical,
  collapses four topo implementations and the copy-pasted finalize, and the
  alphabetical tie-break + extension ordering shrink cross-source divergence to
  the registry marker alone.
- **Verify:** Determinism test red before/green after over DDL AND JSON;
  view-references-view fixture emits dependency-ordered; introspected schemas
  pass the postcondition; matview/multi-FK fixture stable; no emitter-side
  sorting by grep; filtered schemas carry recomputed graphs.

### 0.3 Schema-qualified identity + final graph API (single pass)
- **What:** The FKGraph/walker end-state API landed once: FKEdge gains schema
  qualification AND `Imported bool`; keys become (schema, name) — reconciling
  the current TablesByName ("schema.name" with the empty-schema ".name"
  artifact) vs bare-name FKGraph divergence under ONE keying rule; group
  resolution rekeyed with a stated bare-to-qualified rule for multi-schema
  projects; cascade walkers gain a depth-bounded signature. Plus the FKGraph
  PROJECTION SERIALIZER: a deterministic (schema,name)-keyed derived-view
  serialization (excluded from identity, included in the API payload) — the 8.1
  deliverable, owned here.
- **Why:** Two identity schemes for one object is a latent bug today and a
  guaranteed bug under imports; single-pass API design prevents 0.2->7.3->9.3
  re-churn; the projection serializer previously had no owner.
- **Verify:** Red-green: same-named tables in two schemas through cascade
  checks, workload, group filtering; depth-bounded walk tested; projection
  serializer deterministic and reconstructable; Imported flag present.

### 0.4 Canonical per-object encoder (the primitive)
- **What:** A DEDICATED canonical encoder (explicit field ordering; per-field
  presence semantics distinguishing unset from zero, normalizing to pointers
  where needed; explicit key-sorting for map-typed fields — Index
  Opclasses/Collations/With, SMTransitionMap.Transitions, Schema.Groups,
  NamedTransition.Requires — which stock encoding/json sorted for free but a
  custom encoder must do deliberately) producing per-object canonical JSON for
  every schema object. Type identity is carried by the MODEL-LEVEL collections
  (enums/domains/composites/SM — both construction paths populate them;
  builtin-derived domains materialize there, so builtin regex changes flip the
  revision with no special case). The registry snapshot shrinks to
  serializing/reconstructing whatever has NO model representation (confirmed at
  implementation; expected small or empty for identity purposes — its main role
  becomes import-surface reconstruction), with the explicit field policy
  (semantic + all comments; Source excluded) and the stale Source doc comment
  fixed. MECHANICAL COVERAGE GUARD: a reflection-based test asserting every
  exported field of every DDL-reaching model struct is either encoded or on an
  explicit exclusion allowlist with a reason (CycleGroups,
  StateMachineTransitions, SourceFile, Groups-as-config, caches) — a
  hand-written encoder over 20+ structs WILL silently omit a future field
  otherwise, recreating identity-blindness.
- **Why:** One primitive serves whole-model identity, op bodies, revision
  manifests, import surfaces, and the API payload. The coverage guard is the
  single highest-value addition: it converts "the encoder is complete" from a
  review hope into a mechanical invariant.
- **Verify:** Per-object bytes independent of neighbors and struct-field-order
  refactors; coverage test red when a field is added unencoded; map-key
  ordering deterministic; builtin email-regex change flips the revision;
  Source relabeling does not; nested transition comments do.

### 0.5 Introspect filters managed objects
- **What:** One isManagedObjectName() predicate: `pgdesign_%` for tables and
  views (view filtering designed here — the relation-kind coverage is a design
  requirement, not an existing code site) and the legacy `_pgdesign_sm_%`
  function/trigger prefix. A user object matching reserved patterns triggers a
  diagnostic.
- **Why:** Reconcile demands "introspect, diff, expect empty"; pattern-based
  filtering makes future managed objects (journal, view, position) inherit
  coverage; the namespace reservation must be loud.
- **Verify:** Phase-0 scope: pattern filtering against SYNTHETIC reserved-name
  objects + the diagnostic (the journal/view/position objects don't exist until
  phase 5 — their introspect-cleanliness assertion lives in 5.2/5.8).

### 0.6 One write path; filtering unified; sentinel fix
- **What:** Consolidate multi-file write + owned-dir/orphan bookkeeping onto
  the planner; standalone codegen becomes a thin caller AND gains build's
  per-output FilterByGroups/FilterBySource application (same artifact, two
  contents by entry point today). Fix the migrations-dir sentinel at all NINE
  sites (8 migrate + 1 serve) via one shared helper using the Default(nil)+
  was-set pattern.
- **Why:** Phase 6 enforcement must guard every write; divergent write paths
  and filtering are guards that drift.
- **Verify:** Standalone codegen and build byte-identical incl. under filters;
  identical orphan behavior; sentinel red-green at all nine sites.

### 0.7 Comparison-normalization primitive
- **What:** One shared normalization primitive — types, defaults, expressions
  (parse/deparse both sides) — homed in internal/sqlparse (the go-pgquery leaf;
  normalization MUST use go-pgquery to match pg_get_* forms, so the home is
  necessary, not just convenient). The differ adopts it immediately (red-green:
  introspect->diff over CHECKs/partial indexes/policies reports false drift
  today). Later consumers: 5.2 upgrade reconcile, 5.7 preconditions, 5.8
  reconcile, shadow test.
- **Why:** Multiple comparison engines must agree on the same object; the
  differ already disagrees with PG's rewritten forms today.
- **Verify:** Red-green on the false-drift fixture; normalization unit suite;
  diff --live clean on the comprehensive fixture (reused by 5.8).

## Phase 1 — Ground-clearing

### 1.1 Delete pkg/diff
- **What:** Remove the exported stub; changelog records the promotion trigger
  (second flat-schema consumer).
- **Why:** An exported API unusable without internal imports is worse than
  none; zero importers.
- **Verify:** Package gone; build + vet clean.

### 1.2 Partition bug fixes (red-green each)
- **What:** python_ddl Retention-as-p_interval fix; premake REQUIRED (silent
  zero disables partman); hard errors: non-RANGE + maintenance, undeclared
  pg_partman, maintenance + manual children; part_config query failure becomes
  a diagnostic.
- **Why:** Silent-degradation class: accepted-looking configs producing broken
  DDL discovered in production partitioning.
- **Verify:** Failing test first per bug; CI pg_partman coverage.

### 1.3 Partition lifecycle completion
- **What:** Introspection reads interval/premake/retention from part_config;
  diff distinguishes initial setup / retention-premake updates (Safe,
  risk-classified UPDATE part_config ops — note these ops fall to OpToSQL's
  default comment-stub today and become a first-class op family that 5.1 MUST
  absorb — sequenced before 5.1) / interval changes (hard error + guidance);
  migrate guards extension presence; `schedule` key wires the dead pg_cron
  helper (pg_cron declared or hard error); unacknowledged missing schedule =
  warning.
- **Why:** Partitioned tables are alive at runtime; a tool that creates partman
  config but cannot see, evolve, or schedule it abandoned the lifecycle.
- **Verify:** Golden DDL for schedule; diff/migrate tests per transition class;
  live introspect round-trip; partman ops render (no comment-stub).

### 1.4 Squash safety stopgap
- **What:** Until phase 5: --db and the M200 applied-version check mandatory.
  Stated limits: blocks offline squash of never-applied ranges; doesn't fix
  rewrite/orphan mechanics. First squash-CLI test.
- **Why:** Squash today deletes files production tracking tables record, with
  the DB check opt-in.
- **Verify:** Squash without --db hard-errors; overlap refuses; CLI test.

## Phase 2 — Connection environment (external critical-path milestone)

### 2.1 strictcli: connection-env kind + check context access
- **What:** Third env primitive — hermetic-SUPPRESSED, lazy, no implicit
  default — plus a REGISTRATION-TIME hard error for --db-class flags not bound
  to a declared connection env. CheckContext interface widening + reconciling
  the two context construction paths. Provenance via existing Context.Source().
  Execution: generically-worded todo filed in the strictcli repo AT PHASE-0
  START (lead time — 6.1, 7.4, and seed tier-1 gate on the released version); a
  strictcli session implements and releases; pgdesign bumps and adopts.
- **Why:** A connection URL is precisely what --hermetic should suppress;
  neither existing primitive fits; registration-time enforcement replaces
  review hopes with a mechanical guarantee. Named as an explicit external
  milestone in the DAG.
- **Verify:** strictcli tests: declaration, lazy read, hermetic suppression,
  check-side access, registration-time unbound-flag error; schema dump shows
  the kind.

### 2.2 pgdesign adoption
- **What:** Declare PGDESIGN_DB once; bind ALL SEVENTEEN DB-URL flags (16
  --db + 1 --live; three default semantics normalized); checks read via the
  framework; --hermetic makes DB checks skip visibly; config [database].url
  stays a documented separate layer (cli > env > config). Phase 2 is not a
  leaf: revise's DB tier, import lock/update, live verification, seed tier-1
  bind the connection env from birth (enforced by 2.1's registration error).
- **Why:** One variable, one story; without the non-leaf edges the pathology
  regrows.
- **Verify:** Env-only invocation on every DB command with provenance; hermetic
  skips; raw os.Getenv gone from cmd/ (test harness excepted); precedence test.

## Phase 3 — Schema identity

### 3.1 Whole-model canonical form + envelope
- **What:** Whole-model form = versioned preamble + ordered concatenation of
  per-object forms (0.4 encoder, 0.2 order). Semantic-only policy: type
  identity from model collections; StateMachineTransitions + CycleGroups
  excluded as derived; FKGraph/TablesByName/caches excluded; Extensions
  (ordered) + PGVersion included; object comments IN, TOML-formatting comments
  OUT; [suppress] and extension-registry data OUT. The registry-absent marker
  lives INSIDE the hashed bytes. The JSON artifact is an envelope
  {format_version, revision, model, diagnostics?} — canonical bytes embedded
  VERBATIM (raw-message; re-encoding would break revision == hash(model));
  serve's existing {schema, diagnostics} shape is WRAPPED, not dropped; a
  per-field presence policy table in the format spec.
- **Why:** One canonical answer to "what IS this schema"; the in-bytes marker
  makes cross-boundary hash equality impossible rather than merely forbidden.
- **Verify:** Byte-identical across builds and struct refactors; golden;
  comment edit flips bytes; pg_version and extension changes flip the
  revision; envelope revision verifies against embedded bytes; diagnostics
  preserved on the serve path.

### 3.2 Revision hash
- **What:** Revision = SHA-256 of the whole stream; per-object hashes key the
  object store. Revision is an OPAQUE TYPE whose cross-boundary
  (registry-present vs -absent) comparison is a programming error that errors —
  same validating-boundary discipline as branding. Policies: a pgdesign upgrade
  flips all revisions (one coordinated regeneration — the existing convention,
  now load-bearing). Conformance: revision-equal implies diff-empty as the
  initial gate — with the differ's PGVersion BLINDNESS FIXED first (pg_version
  changes alter emitted DDL invisibly to diff today; the old rationale
  "comments affect revision not diff" was factually wrong — the differ compares
  comments and extensions); once normalization (0.7) is shared by encoder and
  differ, the REVERSE direction (diff-empty implies revision-equal) is adopted
  as the end-state invariant, mechanically forcing both onto one normalization.
  Diff fast path on equal revisions.
- **Why:** The revision is the coupling primitive; the conformance pair makes
  serializer and differ police each other's semantic coverage in both
  directions eventually.
- **Verify:** Sensitivity tests; opaque-type boundary comparison errors with a
  test; differ pg_version test red-green; conformance in CI; fast path
  exercised.

### 3.3 One serializer everywhere
- **What:** generate json and serve schema responses call the SAME envelope
  function; divergent serializers die; introspect-sourced responses carry the
  in-bytes marker.
- **Why:** Two serializers for one struct is how the nondeterminism bug
  survived.
- **Verify:** Identical bodies for the same schema; marker on the introspect
  path; goldens updated once.

## Phase 4 — Codegen breaking release (content lands in the single final release)

### 4.0 Compile checks + CI toolchains (two deliverables)
- **What:** (a) NEW DB-free generated-fixture compile checks: go build, tsc
  --noEmit, javac, kotlinc, zig build-obj, python type-check over freshly
  generated fixtures for every language-mode — no Postgres needed; ALL SIX
  mandatory (no "where feasible" escape hatch). (b) CI toolchain provisioning
  so the existing DB conformance suite stops self-skipping. The known illegal-
  Java output (multiple public types per file in java_jpa AND java_types AND
  java_constraints) is fixed IN THE SAME CHANGE as the javac check lands —
  main is never red, bisection never breaks.
- **Why:** 4.1's per-language claims are currently backed by nothing: CI runs
  go vet/test only, and the conformance tests compile hand-authored templates,
  not codegen output — provisioning alone would not catch the Java bugs.
- **Verify:** All six compile checks run in CI on generated fixtures; the
  Java fix lands with its check in one commit; conformance suite runs
  unskipped.

### 4.1 Branding per language — corrected mechanisms, full surface
- **What:** Shared mechanism first (extend the enum_gen dispatch seam;
  enum-emission dedup CO-GENERATION-AWARE — driven by the output group, since
  gorm-only consumers rely on gorm's enum block). Go: opaque struct (unexported
  value field); package-level VAR members (const of struct type is illegal;
  deliberate reassignment documented out-of-scope); Parse errors on unknowns;
  UnmarshalJSON/UnmarshalText/sql.Scanner IMPLEMENTED VIA Parse (validating —
  generated structs live in DB-scanned/JSON-round-tripped positions; gorm's
  read/write path rides the same Scanner/Valuer); Valuer/MarshalJSON/Stringer;
  zero value detectably invalid. Python: parse() classmethod as ergonomic
  typed alias (native Enum.__call__ already validates); query-layer +
  validator signatures enum-typed; the query-layer package gains real enum
  imports/definitions; the ROW DATACLASS gains __post_init__ coercion
  (idempotent for already-enum values) covering BOTH PgBackend AND
  InMemoryBackend read paths; _constraints.py explicitly needs NO change
  (StrEnum str-equality). TS: keep the literal union; add parse() at
  boundaries (transition maps already typed — no work). Java/Kotlin:
  value-based parse; JPA gains a generated AttributeConverter (@Convert)
  backed by getValue()/fromValue() — never @Enumerated(STRING) (persists
  NAMES); java_jpa, java_types, AND java_constraints move to
  MultiFileGenerator (one public type per file — per 4.0); JPA gains its
  missing enum-column branch. Kotlin value-parse. Zig: wrapper struct + parse;
  transition maps re-keyed. sqlalchemy: UPGRADED to native
  sa.Enum(PyEnumClass) columns (the string-shaped "necessity" was false);
  drizzle: NO change (already pgEnum-typed). Constants mode unchanged.
  Constraints validators re-target the branded representation (Go switch on
  .String(); Java contains(getValue()); Kotlin equivalents).
- **Why:** Invalid values cannot be named or smuggled: compile error where
  expressible, validating error at every ingress, DB CHECK backstop. The
  corrected per-language mechanics avoid shipping broken scans (Go), wrong
  persisted values (JPA), type-safety regressions (TS), phantom machinery
  (Python), and false exceptions (drizzle/sqlalchemy).
- **Verify:** Per language under 4.0's compile checks: invalid values fail at
  the earliest boundary with errors; Go all-ingress round-trip; Java persisted
  value == getValue(); Python: both backends yield enum-typed fields, pickle
  round-trips; TS exhaustive switches compile; constraints validators pass
  against branded fixtures; sqlalchemy models carry sa.Enum.

### 4.2 Revision stamping
- **What:** Helper-internal addition (zero site re-touches — wording landed in
  0.1): revision line + stamp format-version via the genkit grammar.
  Artifact-class taxonomy: comment-stamped = sql, d2, graphql, codegen, doc,
  seed (seed's stamp is honest provenance ONLY — seed content depends on
  --seed/--counts/--mode and is never freshness-checked; stated); in-band =
  json (envelope field); stdout output of generate = stamped in-band,
  freshness-exempt (nothing on disk; stated); structurally exempt = svg
  (non-deterministic) and .sqlsplit (sealed format). Stamp = FULL-PROJECT
  revision always; filtered outputs carry the full-project stamp and their
  content freshness stays byte-compare's job. Stated cost: one schema edit
  re-stamps every generated file (intended).
- **Why:** The stamp is how artifacts say which schema they came from;
  full-project stamping resolves the filtered-output paradox; the named
  exceptions prevent unclassified stamp-disagreement sources.
- **Verify:** Rebuild-without-change stays green; schema edit flips every
  stampable output exactly once; doc stamped; .sqlsplit byte-stable and
  Decode-able; json envelope carries revision; filtered outputs carry the
  full-project stamp.

### 4.3 Breaking-change packaging (in the single final release)
- **What:** Breaking-typed changelog entries: per-language branding, header
  wording, revision stamps, generate --idempotent RAISE-on-mismatch (5.7),
  constraints-validator re-targeting, Java one-type-per-file layout change
  (types/constraints/jpa), gorm branded fields, sqlalchemy sa.Enum columns.
  Consumer todos filed at THE release with regeneration + adaptation notes:
  Python raw-string construction -> parse()/members; Go string-literal
  comparisons on enum fields -> branded type; TS parse() at boundaries
  (switches keep compiling); JPA converters; Java file layout.
- **Why:** All consumer-visible changes, one break, one adaptation, honest
  handoff.
- **Verify:** rlsbl changelog coverage passes with breaking entries; consumer
  todos filed; consumer drift-checks pass after regeneration.

## Phase 5 — Migrate integrity

### 5.0 Schema and format design (design gate)
- **What:** Complete designs before implementation: pgdesign_migration_ops
  (identity: migration ref, phase, sequence, op kind, target; serialized
  down-op; intent/confirm status), pgdesign_applied_migrations (MUST carry a
  migration-level checksum column — serve selects version/applied_at/
  description/checksum today), pgdesign_chain_position (current revision,
  in-progress edge ref, per-database boundary), migration file format
  (sequence+slug; from/to revision; ops referencing store objects by hash),
  chain-edge file format (migrations/chain/<from>-<to>.json), store layout
  (migrations/objects/, migrations/revisions/ — implemented by the
  internal/objstore package, which also backs imports vendoring), archive
  layout (migrations/archive/). The two divergent tracking write paths
  (state.go helpers vs inline SQL in apply/rollback) are reconciled onto one
  path in this design.
- **Why:** 5.2 migrates rows INTO these schemas; per planning discipline the
  designs precede the implementation order. Labeled honestly: this is a human
  design gate, with one mechanical check.
- **Verify:** Design fixtures round-trip through the 0.4 encoder; schema DDL
  fixtures reviewed before 5.1 starts.

### 5.1 Self-contained ops via the object store
- **What:** internal/objstore lands here (hash-keyed put/get, dedup, layout —
  the objects/ root; revisions/ and chain/ roots follow in 5.2). Every
  pointer-def op REFERENCES its target object + the transitive
  composition-closure of type definitions BY CONTENT HASH. All THIRTEEN
  families: nine pointer-def + RawSQL + PartitionChildSpec + ParentTable + the
  1.3 partman-config ops (which hit the default comment-stub today). DOWN-ops
  get identical treatment. Comment-stub no-ops and wrong-object fallbacks
  (deny-mutation / append-only) DELETED; sequences keep parameters;
  opCreateTable passes op.PGVersion (hardcoded 0 today) and resolves
  enum/domain qualification from the closure. Table-driven round-trip test per
  family — up AND down — on a fixture with an enum column, a domain column,
  and a version-gated generated column, asserting rendered SQL equals
  generate's output.
- **Why:** A migration file that renders different SQL than intended — empty,
  or the WRONG OBJECT — is the worst possible artifact; store-referencing
  keeps ops thin and reviewable and makes degraded states unrepresentable.
- **Verify:** Round-trip table test covers all thirteen families up and down;
  fallbacks gone; the mixed fixture renders byte-identically to generate.

### 5.2 Chain, revisions, and `migrate upgrade`
- **What:** Chain edges one-file-per-edge in migrations/chain/; revision
  manifests (ordered object->hash lists) in migrations/revisions/; chain-head/
  find-heads API (genesis: null parent). Discovery/ordering rewritten off
  semver. Filenames: sequence + auto-derived slug (override flag). `migrate
  upgrade` (one-time, explicit): requires clean schema files per git when in a
  repo (stated caveat outside); acquires THE session-level advisory lock
  (shared with apply/rollback/baseline, held across reopened transactions —
  concurrent apply-during-upgrade is a verify case); content-addressed file
  writes (objects, revisions, chain edges) are idempotent and land BEFORE the
  DB transaction; then ONE transaction: snapshot old applied set -> create
  journal/view/position -> migrate tracking rows -> ASSERT view reproduces the
  snapshot -> DROP old table -> COMMIT (the sole commit point; the reverse
  window — files written, commit failed — is harmless BY the idempotence
  property, stated as such; on-disk state reconciles from chain position on
  next run). Verify-then-stamp: clean TOML<->DB reconcile (0.7) or refusal
  with the drift report; per-database boundary stamped into chain_position.
  Multi-database rule stated: synthetic-prefix revisions are per-database
  stamps; shared prefix files are the union; databases at different boundaries
  are a supported state. Existing semver files become the linear prefix with
  synthetic checksum-verified revisions. serve updated (through the
  internal/project loader — see 8.1 ordering): handleMigrations repointed to
  the view (its existing empty-guard returns 200-with-[] today, so this is
  correctness, not crash-fixing); version endpoint updated for sequence+slug
  names. Store<->chain<->files consistency check (the shared integrity
  checker — 6.2 and 7.2 invoke the same primitive).
- **Why:** Revision pairs give migrations identity; per-edge files make the
  chain merge-friendly; the upgrade choreography exists because
  assert-after-DROP is unexecutable, concurrent binaries need the lock, and
  out-of-transaction file writes need an idempotence-based recovery property.
- **Verify:** Crash injection (before commit: old world intact; after commit:
  files reconcile); dirty-tree refusal; mid-edit TOML cannot stamp; drift
  report on unclean reconcile; consistency check red on tamper; concurrent
  apply blocked by the lock.

### 5.3 Append-only squash (consolidation edges)
- **What:** Consolidation = ADDITIONAL chain edge; superseded files retire
  intact to migrations/archive/, reachable via their edges (mid-range DBs
  apply remaining originals by chain_position edge selection). Consolidation
  DOWN-ops derived by diff(from_manifest, to_manifest) — WITH THE STRUCTURAL
  LIMIT ENFORCED: the differ is structural, and backfill/transform DML and
  RawSQL ops are not recoverable from manifests, so squash REFUSES
  snapshot-diff consolidation when the range contains DML/RawSQL ops and falls
  back to composing the originals' recorded downs for that range; the
  rollback-equivalence invariant is stated as STRUCTURAL (revision equality —
  which says nothing about data) and the data-op refusal is what keeps it
  honest. Tracking/journal lineage handled; no orphaned rows; files never
  rewritten.
- **Why:** Mutation of applied artifacts stops existing; snapshot-derived
  downs give by-construction equivalence exactly where they are sound, and
  the DML refusal prevents a silent data-loss hole (a squashed
  add-column/backfill/drop-column range would otherwise get a DOWN that
  recreates the column empty).
- **Verify:** Squash of applied migrations via consolidation; mid-range DB
  resumes via archived originals; rollback-equivalence on a structural
  fixture AND on a merged-type-change fixture; a DML-containing range
  triggers the refusal/fallback; no orphaned rows.

### 5.4 Unconditional checksums (apply surface)
- **What:** After 5.2/5.3: checksum verification unconditional ON APPLY —
  including archived-original applies (the only surviving down-direction file
  read is a mid-range original apply, which IS an apply). Any mismatch =
  corruption, hard error naming the file. Prefix files carry synthetic
  revisions whose checksums ARE verified. (No rollback checksum surface
  exists: post-5.6 rollback reads no files.)
- **Why:** Enforcement after the format means mismatch has exactly one
  meaning; the earlier "and rollback" clause contradicted 5.6 and is
  withdrawn.
- **Verify:** Tamper tests on active and archived files refuse apply with
  precise reports; upgraded fixture applies cleanly.

### 5.5 Applied-op journal
- **What:** pgdesign_migration_ops + pgdesign_applied_migrations per 5.0 (one
  write path — the state.go/inline-SQL divergence reconciled). Records op
  identity AND serialized down-op (via the store). TIMING: transactional ops
  journal INSIDE the op's transaction; non-transactional ops (create AND drop
  index concurrently; version-conditional enum-add — transactional PG12+) use
  INTENT-then-CONFIRM rows with class-specific resume protocols: for
  create-index-concurrently the resume of an unconfirmed intent MUST check
  pg_index.indisvalid — an interrupted CIC leaves an INVALID index of the
  target name that IF NOT EXISTS would silently skip forever — and
  drop-and-rebuild (DROP INDEX CONCURRENTLY IF EXISTS, then CIC); for
  drop-index-concurrently the renderer gains IF EXISTS; enum-add is already
  idempotent. (Also fix sql.go's wrong comment claiming CIC+IF NOT EXISTS is
  version-incompatible.) The same protocols govern journal-driven rollback of
  non-transactional down-ops. chain_position updates in the same transaction
  as each edge-completing journal write. Re-apply resumes by skipping
  confirmed ops. AppliedVersions/status/serve read the view.
- **Why:** The version row is written last today; committed-but-unrecorded
  DDL causes the permanent abort loop. A journal row after a
  non-transactional commit can be lost — intent/confirm closes that; the
  indisvalid check closes the hole INSIDE the recovery protocol itself, which
  plain IF NOT EXISTS would reopen.
- **Verify:** Fault injection: mid-phase; after CIC (asserting indisvalid
  handling — the resumed index is VALID); after DROP INDEX CONCURRENTLY;
  around enum-add on both PG classes; view semantics equal the old
  applied-set semantics; single write path by grep.

### 5.6 Journal-driven rollback (scoped)
- **What:** Rollback executes recorded down-ops in reverse journal order —
  files never consulted. MID-EDGE semantics stated: when chain_position shows
  an in-progress edge, rollback reverses confirmed ops (and applies the
  class-specific protocols to unconfirmed non-transactional intents); the
  reversibility pre-check runs against JOURNALED ops, not file ops (else it
  refuses over an irreversible op that never ran). SCOPE: guaranteed from the
  upgrade boundary forward; pre-upgrade prefix + baselines ROLLBACK-FROZEN
  (crossing = hard error naming the boundary).
- **Why:** Rollback today trusts files absolutely — inverting ops that never
  ran (the DROP-COLUMN data-loss case) or following edited files. Recorded
  reality closes both; mid-edge is the whole point of per-op journaling.
- **Verify:** Rollback after partial apply drops nothing it didn't create;
  works with files archived; mid-edge rollback correct incl. an unconfirmed
  CIC intent; boundary-crossing refuses; journal-based reversibility
  pre-check tested.

### 5.7 Preconditions + predicate IR
- **What:** Per-op-class predicates against pg_catalog (absent for creates;
  present-and-matching via 0.7 for alters/drops); unexpected state = hard
  error naming object/expected/found. DML ops precondition-free. IR =
  structured data in internal/predicate; the Go executor SHARES introspect's
  catalog-query layer (extracted — introspect already contains every needed
  query; a second copy would be the divergence bug class 0.5 kills); only the
  pgx executor lives in migrate; SQL renderer compiles the same structures
  into DO-blocks for generate --idempotent (RAISE on mismatch — in 4.3's
  breaking notes). CI conformance matrix: both backends + the differ where
  classes overlap, against live states, identical verdicts.
- **Why:** Blind DDL is how drift gets tolerated; one definition, two
  compilations, tested equivalence. The executor's structured diagnostics are
  why it exists.
- **Verify:** DB-backed matrix per op class; golden idempotent SQL; mismatch
  RAISEs, match no-ops; conformance green; shared catalog layer by import
  graph.

### 5.8 Post-apply reconcile-verify
- **What:** After apply: introspect (0.5 exclusions; canonical via 0.2) +
  0.7-normalized diff against the target model; residual mismatch = hard
  error listing every object. Reconcile does not auto-add imported schemas.
  SM-vs-enum lossiness documented. Asserts revision-equal-implies-diff-empty
  on 0.7's comprehensive fixture; the journal/view/position introspect-
  cleanliness assertion (deferred from 0.5) lives here.
- **Why:** Preconditions check ops locally; reconcile checks the combined
  result globally, reusing the real differ.
- **Verify:** Clean apply over the comprehensive fixture reports empty;
  out-of-band ALTER mid-migration surfaces; managed objects invisible.

### 5.9 Pure chain-based generation
- **What:** migrate generate = diff(deserialize(head manifest via objstore),
  current model) — pure, no DB. ALWAYS emits large-table-safe forms
  (NOT VALID + VALIDATE; backfill-then-set-not-null; expand/contract
  phasing); QueryTableStats and generate-path stats plumbing deleted; the
  EXPAND_CONTRACT_TYPE_NARROW advisory warning is RELOCATED (emitted from the
  diff classification, not stats) so the one user-visible advisory doesn't
  silently vanish. Drift caught at apply; adoption via baseline (which writes
  chain_position + a revision manifest).
- **Why:** Same TOML edit must produce the same migration regardless of DB
  state; the always-safe form makes purity real.
- **Verify:** Generation without any DB; FK add emits two-step NOT VALID with
  no DB; drifted DB does not alter output but fails apply; stats plumbing
  gone; the advisory warning still appears.

### 5.10 Fork resolution + ecosystem alignment
- **What:** `migrate rebase <head>`: re-parents a fork's tail, recomputes
  from/to revisions, re-derives manifests. Baseline's semver-based
  divergence/out-of-order guards re-expressed against chain reachability.
  Shadow test, squash CLI, docs updated for format+journal+chain+store;
  migration-guide rewritten.
- **Why:** Two branches each appending a migration is normal; detection
  without resolution is a dead end; baseline's guards reference a version
  scheme that no longer exists.
- **Verify:** Fork fixture: rebase re-parents, revisions recomputed, store
  consistent; baseline guards fire on chain-unreachable states; shadow test
  passes on the comprehensive fixture; full migrate suite green.

## Phase 6 — Orchestration and enforcement

### 6.1 pgdesign revise
- **What:** PURE tier: build planner + 5.9 generation + PURE checks — static
  NF audit (audit.Audit is pure; --strict-nf blocks generation today and
  revise must not regress it) and structural workload — all BLOCKING. DB tier
  (phase-2 connection env): live import verification (7.4) + LIVE checks
  (TANE discovery, pg_stat workload) — NON-RETROACTIVE (fail the command
  loudly; the committed migration stands; next revise incorporates fixes).
  Chain head from the chain files; two heads = hard error naming both +
  pointing at migrate rebase; genesis handled. Separate safegit commits (pure
  outputs; then migration+chain+store) via ONE shared commit helper; commit
  failure = hard error — build's warn-and-continue flipped in the same pass.
  Partial failure keeps committed pure outputs, exits non-zero naming the
  skipped tier.
- **Why:** The forgotten-step failure mode is real; revise is "make
  everything consistent and tell me what's wrong" without eroding purity —
  and static analysis that CAN block MUST block (fail-open regression
  otherwise). Commit-before-DB-tier is sound: the migration is pure and
  repo-level; per-database applicability is re-checked fail-closed at apply.
- **Verify:** End-to-end: edit -> revise -> outputs + chained migration + two
  commits, one revision everywhere; a BCNF violation with strict-nf blocks
  the pure tier; DB-unreachable keeps pure outputs, non-zero, names the
  skipped tier; two-head fixture points at rebase; commit-failure
  hard-errors.

### 6.2 Revision enforcement
- **What:** Invariant: all regenerable planner-set artifacts carry the ONE
  full-project revision after any write. Taxonomy: FULL regenerators (build,
  revise) always allowed; PARTIAL writers — exactly one exists today
  (codegen --output) — refuse when non-rewritten siblings differ, and the
  taxonomy PRE-COMMITS the rule that any future file-writing generate mode
  must register as full-or-banned (the invariant is defended by design, not
  by the current absence of a flag); SOURCE-EDITING writers (fmt, introspect
  --output) are outside the invariant but change the revision — they print a
  follow-up notice and the check catches staleness. The partial-writer
  refusal and the revision check regenerate through the SAME per-output
  group/source filters from [output] config (0.6's unification — otherwise
  false-stales against build's filtered artifacts). Outside the invariant,
  stated: migration files + chain + store (append-only; covered by INVOKING
  5.2's consistency checker — not a second implementation), seed output
  (stamped, unenforced provenance), stdout (check-time only). Missing/
  old-format stamps = stale. The revision CHECK (error severity) covers:
  chain/store integrity (via 5.2's checker), cross-artifact stamp agreement,
  standalone artifacts. genkit stamp-extractor complements byte-compare.
- **Why:** Divergence is created by partial writes and source edits,
  resolved by full ones; naming every writer class and reusing the one
  integrity checker prevents unclassified sources and duplicate guards.
- **Verify:** TOML edit then build succeeds; then codegen --output of one
  output refuses naming stale siblings (with filters applied — a
  group-filtered fixture); fmt prints the notice and the check goes stale;
  tampered header caught; chain violation caught via the shared checker;
  seed/migrations/stdout never flagged.

## Phase 7 — Imports

### 7.1 Declaration and reference syntax
- **What:** [imports] parsing (alias -> git URL + ref + target PG schema);
  `alias:table` ONLY in FK ref_table; alias resolution BEFORE dot-split;
  aliases elsewhere = hard error naming supported sites. Diagnostics:
  unknown alias, unresolvable target, collisions.
- **Why:** References name the DEPENDENCY; a typo'd alias is a resolution
  error, not a phantom schema.
- **Verify:** Parse/build tests; typo -> resolution error; precedence test;
  alias-in-depends_on -> scoping error.

### 7.2 Surface snapshot and pinning
- **What:** `import lock`: resolve pin (git URL + ref; git plumbing; no DB),
  parse framework TOML, vendor the surface into imports/<alias>/ via the
  SAME internal/objstore package (one package, multiple store roots):
  referenced tables + transitive composition-closure of type definitions,
  each with per-object hash, plus lockfile entry (URL, ref, resolved commit,
  surface hash). `import update` re-pins. `check --tag imports`: re-derive
  and report SEMANTIC drift at column level, hard-failing CI — built on the
  same store-integrity primitive as 5.2's checker. Requirements: extensions
  inferred per referenced object; pg_version floor carried (consumer
  re-declares >=).
- **Why:** Reproducible offline builds; semantic column-level errors are the
  point; the shared store package prevents a third store implementation.
- **Verify:** Two-project fixture: drifted column type -> exact
  column+FK-naming error; unreferenced changes silent; offline build;
  per-object hashes stable; enum closure usable.

### 7.3 Model integration
- **What:** ImportedTables split slice. Union sites enumerated COMPLETELY:
  buildTablesByName (E204/TableByName resolution — without this the split
  slice breaks FK validation, migrate FK qualification, and check C104),
  BuildFKGraph (edges keyed (schema,name), Imported=true), seed FQN pools,
  AND the D2/GraphQL edge emitters (they emit FK edges by target-name string
  today; D2 also drops fk.RefSchema — fixed here). Registry collisions =
  hard error naming both sources; imported enums usable; extension/
  pg_version re-declaration enforced.
- **Why:** Fail-closed only holds where resolution funnels through the
  union; the FOUR bypass sites are named because each would otherwise
  produce spurious errors (E204), phantom nodes, dangling seeds, or dangling
  diagram edges.
- **Verify:** NO spurious E204 on imported FKs (explicit test); FKGraph
  nodes keyed and flagged; seed resolves imported pools; D2 edges
  schema-qualified; DDL/audit/codegen outputs contain zero imported
  artifacts; collision and re-declaration tests.

### 7.4 Downstream sweep
- **What:** App-only DDL with schema-qualified FKs. Diff/migrate exclude
  imported tables; reconcile does not auto-add imported schemas. Live import
  verification consumes the 5.7 predicate executor (phase-2 env). Audit,
  design checks, orphan warnings, codegen skip imported tables. Seed tiers
  in FK-value resolution — with the existing silent-UUID pool-empty fallback
  made UNREACHABLE for imported FKs (routed exclusively through tiers;
  fallback hard-errors): tier 1 (DB): real-key pools, deterministic sorted
  selection, Zipf + COPY unchanged; tier 2 (offline): count-wrapped
  ordered-offset subqueries in INSERT mode, Zipf dropped (stated); tier-2
  hard error RESCOPED to UNIQUE constraints where the imported FK is the
  sole distinguishing column (single-column, or all columns
  imported/subquery-valued — composite UNIQUEs with an offline-distinct
  local column are fine); the existing fixed-rowIdx silent fall-through in
  the dedup retry is fixed alongside; tier 3: hard error
  offline+COPY+NOT-NULL naming all three constraints. D2/GraphQL render
  imported tables as minimal reference shapes (first-class shape class 9.x
  preserves).
- **Why:** Imported rows are facts — never regenerated, audited, or
  fabricated; the error surface bans exactly the impossible and the
  silently-wrong.
- **Verify:** Per-package fixtures; live verification via the executor; seed
  tier tests incl. determinism, offset wrap, the RESCOPED UNIQUE error (a
  composite-UNIQUE fixture passes), fallback unreachability, and the
  triple-constraint error; D2 golden compiles.

## Phase 8 — Read API

### 8.1 DB-free serve mode
- **What:** Pool optional; --db optional in project-schema mode. The shared
  project loader lands in internal/project — returning (schema, registry,
  cfg) — used by build/codegen/revise/serve (serve's registry-discarding
  loader dies). ORDERING: phase 5's serve edits (5.2) land first; 8.1's
  rework routes them through internal/project (serve/handlers.go is
  co-edited by 5 and 8 — NOT parallel). Schema endpoint = the same canonical
  envelope function as generate json (revision + FKGraph projection from
  0.3; diagnostics wrapped). Nil-registry SM-drop fixed. DB-only endpoints
  degrade explicitly.
- **Why:** The seam made real; the endpoint is literally the same function
  as the json output, so it can never drift.
- **Verify:** serve starts without a database and answers (byte-consistent
  with generate json incl. diagnostics); SM diagrams render; DB-only
  endpoints degrade explicitly.

### 8.2 API hygiene
- **What:** --timeout becomes request-context enforcement; audit becomes
  job-start/poll (cancellable); doc endpoint added.
- **Why:** A dead flag is a lie; an unbounded synchronous endpoint is a
  self-DoS button.
- **Verify:** Slow-audit observes timeout/cancel; doc endpoint matches
  generate's doc.

## Phase 9 — Visualization

### 9.1 Options plumbing (split dependency)
- **What:** D2 options from config (after phase 0); serve query-param
  plumbing (after phase 8). RenderSVG parameterized: dagre/elk (TALA
  excluded — not in the OSS library), theme, direction.
- **Why:** Every enrichment needs the config path; the serve half is
  honestly sequenced.
- **Verify:** Config round-trip; elk golden; serve params post-8.

### 9.2 Enrichment
- **What:** Conditional-generation layers: index/unique markers, nullable
  indicator, comments as tooltips, checks as notes, RLS/append-only
  markers, enums as rectangles with values. Imported reference shapes
  preserved. The column/table presentation logic is FACTORED into a shared
  helper consumed by BOTH doc.go and d2.go (doc already derives all of this
  from the same model fields — no second derivation).
- **Why:** Diagrams omit what doc knows; the shared helper is the DRY fix
  instead of re-deriving.
- **Verify:** Golden per layer; independently disableable; goldens compile;
  reference shapes survive all layer combinations; doc and d2 use the one
  helper.

### 9.3 Filtering
- **What:** Include/exclude globs; include-dependencies depth via 0.3's
  depth-bounded walker; summary mode; edges to excluded tables skipped;
  self-FKs preserved; filtered schemas canonical via 0.2.
- **Why:** Subset views on the finalized machinery, not parallel logic.
- **Verify:** Goldens per mode; filtered output compiles; depth semantics
  match the walker.

### 9.4 Cardinality
- **What:** Edge blocks with native crow's-foot arrowheads; 1:1 via
  unique/PK detection, 1:N default, M:N strict junction heuristic (exactly
  two FKs = whole PK, no other columns).
- **Why:** The most-sought ERD information; strict = conservative.
- **Verify:** Golden per class; junction-with-extra-column NOT collapsed.

### 9.5 Heat maps and live stats
- **What:** Fan-in/out on a fixed colorblind-safe stroke scale; live
  annotations as caller-provided data; generate stays DB-free.
- **Why:** Hubs are cascade risk; purity boundary intact.
- **Verify:** Goldens; injected-stats test; no DB import in generate.

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract. Unplanned by design.

---

## Dependency DAG

- Phase 0 internal: 0.1 -> 0.2 -> {0.3, 0.4}; 0.5/0.6/0.7 after 0.2. The
  strictcli todo is filed at phase-0 start (phase 2 is an EXTERNAL
  critical-path milestone: an independent strictcli release gates 2.2, and
  2 -> {6.1, 7.4, seed tier-1}).
- 0 -> {1, 2, 3, 9.1-config-half}; 0.1 -> 4.1 (parallelizable from there);
  {0.1, 3.2, 3.3} -> 4.2; 4.0 precedes 4.1's verify (Java fixes land WITH
  the javac check — no red window); 4.1+4.2 -> 4.3; 4.2 -> 6.2.
- 0.4 -> {3.1, 5.1, 7.2}; 0.7 -> {3.2-reverse-invariant, 5.2, 5.7, 5.8};
  0.3 -> {7.3, 8.1-projection, 9.3}; 3 -> {5, 7, 8}; 1.3 -> 5.1 (the partman
  op family must exist before the op rewrite absorbs it).
- 5 internal: 5.0 -> 5.1 -> 5.2 -> 5.3 -> 5.4 -> 5.5 -> 5.6 -> 5.7 -> 5.8
  -> 5.9 -> 5.10. {5, 0.6, 3, 4.2} -> 6; 5.7 -> 7.4; 7.4 -> 9.2;
  5.2-serve-edits -> 8.1 (serve/handlers.go co-edited — phases 5 and 8 are
  NOT parallel); 8 -> 9.1-serve-half.
- Parallelizable after phase 3: 4.1 (already after 0.1), 5, 7 (through
  7.2). Phase 8 follows 5.2's serve edits.

## Relationship to existing todos

- infra-env-db-locator.md — superseded by phase 2.
- migrate-add-column-missing-if-not-exists.md — superseded by phase 5.
- genericize-diff-library.md — resolved by 1.1.
- partition-lifecycle-and-diff-library.md — Part 1 = 1.2/1.3; Part 2 =
  1.1's trigger decision.
- cross-framework-schema-composition.md — core = phase 7.
- orxtra-codegen-deferred-remaining.md — item 17 via phase 4 + DB CHECK;
  item 18 = phases 3/6; item 20 = phase 6; item 19 dropped; items 21/22
  out of scope.
- visualization-and-web-ui.md — its phases 1-5 = phase 9; web UI = 8/10.
- rename-to-strictpg.md — in todo/.obsolete/ per the no-rename decision.

## Out of scope, pending their own design rounds

Test schema mode. N-project topology. Manifest + per-language linter
ecosystem (evidence-gated). Recorded summit end-states: declarative catalog
reconciliation for migrate; structural semantics/metadata split in the
model; registry materialization into Schema as sole type-truth;
extension-DDL-name resolution baked into the model; DB/boot-time revision
binding; the reverse conformance invariant as primary (adopted as end-state
in 3.2, activated once encoder and differ share 0.7).

## Effort

Phase 0: 2-3 sessions. Phases 1-2: 1-2 each (2 is externally gated —
milestone). Phase 3: 1. Phase 4: 3-4 (incl. 4.0's two deliverables).
Phase 5: 5-7 (largest). Phase 6: 1-2. Phase 7: 3-4. Phase 8: 1 (after 5.2's
serve edits). Phase 9: 2-3. Parallelization per the DAG.

Release: exactly ONE rlsbl release at the very end (global release-once
rule); everything accumulates unreleased; consumer todos filed at that
release. No intermediate state can reach a consumer.
