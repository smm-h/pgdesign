# Comprehensive roadmap: determinism, identity, migrate integrity, orchestration, imports, API, visualization

Single consolidated plan from the 2026-07-19 design session, revised through three
adversarial critique rounds (each: six investigation agents verifying grounding facts
against source and auditing every phase; accepted findings folded in). This file is
exempted from todo immutability by explicit owner authorization — it is a living plan;
git history preserves prior versions. Fully self-contained: every subphase carries its
complete What/Why/Verify.

## Decision provenance

Per the %% convention: `[%%]` decisions were trust-adopted (owner accepted the
recommended option) — weakly held, freely reversible, never to be cited as deliberate
intent. `[deliberate]` decisions are the owner's own.

- `[deliberate]` No rename; the project stays pgdesign.
- `[deliberate]` ONE release for the whole roadmap, at the very end (global rule).
- `[deliberate]` No backward compat anywhere, ever (global rule): old surfaces are
  deleted, all callers updated, migration via explicit one-time commands; beware
  compat-in-disguise.
- `[%%]` Compiler/live seam: build and the core pipeline stay pure; live-DB work is a
  distinct tier.
- `[%%]` Summit-grade design only where outputs are permanent (identity, migration
  format); pragmatic rungs elsewhere with summits recorded.
- `[%%]` Canonical ordering lives in a shared Canonicalize() finalize routine invoked
  by ALL Schema constructors (Build, BuildMulti, Introspect) and by the filter
  helpers.
- `[%%]` Canonical serialization: per-object primitive, semantic-only, full-model
  scope; Extensions + PGVersion included; builtin-derived DDL-emitting registry
  entries included; dedicated canonical encoder (not struct-tag reflection).
- `[%%]` Revision = SHA-256 of canonical bytes; stamp = full-project revision always
  (provenance; byte-compare owns content); every-command enforcement with
  full-regenerator / partial-writer / source-editor taxonomy.
- `[%%]` Migration precondition drift = hard error, always.
- `[%%]` Predicate IR: one structured definition; Go executor (structured
  diagnostics at apply/verify time) + SQL renderer (generate --idempotent);
  CI conformance matrix incl. the differ as a third leg. (A critique proposal to
  drop the Go executor was REJECTED: the executor exists for structured
  object/expected/found diagnostics via shared normalization, not DB-freedom.)
- `[%%]` Migration history append-only; squash = consolidation edges; archive intact;
  checksums unconditional once the format lands.
- `[%%]` Ops are self-contained via the canonical primitive: pointer-def ops
  REFERENCE their target objects (and transitive type closure) BY CONTENT HASH into
  the per-object store — no inline blobs, no flat mirrors, no second dialect.
- `[%%]` Migrations identified by revision pairs in a parent-linked chain; filenames
  cosmetic (sequence + auto-derived slug, override flag).
- `[%%]` Chain home = one file per edge: `migrations/chain/<from>-<to>.json`
  (merge-conflict-free; the DAG is the data). [Reverses earlier manifest.jsonl pick.]
- `[%%]` Snapshot store = content-addressed per-object files
  `migrations/objects/<hash>.json` + revision manifests
  `migrations/revisions/<revision>.json` (ordered object->hash lists). Ops, revision
  snapshots, and import surfaces all reference the same store. [Reverses earlier
  whole-model-snapshot pick.]
- `[%%]` Visible directory names for committed load-bearing data: migrations/chain/,
  migrations/objects/, migrations/revisions/, migrations/archive/,
  imports/<alias>/. [Reverses earlier dot-dir picks.]
- `[%%]` Journal = table `pgdesign_migration_ops` + view `pgdesign_applied_migrations`
  (merit: one SQL definition of "applied + status" for apply/rollback/status/serve);
  records op identity AND serialized down-op; DB-driven rollback from the upgrade
  boundary forward; pre-upgrade prefix + baselines are ROLLBACK-FROZEN.
- `[%%]` Position anchor = `pgdesign_chain_position` (current revision, in-progress
  edge, per-database boundary).
- `[%%]` Grandfather boundary = verify-then-stamp inside a single-transaction
  `migrate upgrade` (advisory-locked; assert-before-DROP; files written idempotently
  before commit; DB commit is the sole commit point).
- `[%%]` Pure migration generation: diff(head revision manifest, current model);
  ALWAYS emits the large-table-safe form (no row-count input); drift surfaces at
  apply; intentional drift adoption via the baseline flow.
- `[%%]` Orchestrator `pgdesign revise`: pure tier (build + generation) then DB tier
  (import verification + DB checks); separate commits; commit failure = hard error;
  pure outputs kept on partial failure.
- `[%%]` Imports: split slices, fail-closed; `alias:table` in FK ref_table only;
  surface = referenced tables + transitive closure of composition-referenced type
  definitions; per-object hashes from the store; source pin = git URL + ref;
  `import lock` / `import update`; collisions hard error; requirements re-declared.
- `[%%]` Branding (single final release): Go opaque struct, VALIDATING boundary
  (Unmarshal/Scan via Parse, error on invalid), var members (const of struct type is
  illegal Go; deliberate reassignment is documented out-of-scope); Python: parse()
  alias + enum-typed surfaces + PgBackend __post_init__ coercion (construction
  already validates natively — no closing machinery, no pickle override); TS: keep
  the literal union, add parse() (transition maps already typed); Java/Kotlin:
  value-based parse + JPA AttributeConverter (never @Enumerated(STRING)); Zig:
  wrapper struct + parse. Constants mode unchanged; constraints validators re-target
  the branded representation; drizzle/sqlalchemy string-shaped by ORM necessity.
- `[%%]` Seed with imported FKs: tiered real keys; tier-2 (offset subqueries)
  hard-errors when the imported FK participates in a UNIQUE constraint (silent
  dedup-exhaustion otherwise); tier-3 hard error = offline+COPY+NOT-NULL.
- `[%%]` strictcli: connection-env kind (hermetic-suppressed, lazy, no default);
  CheckContext widened; registration-time hard error for unbound --db flags;
  provenance via existing Context.Source(); handed off via generic todo.
- `[%%]` Partition: premake required; opt-in schedule key wiring the pg_cron helper;
  unacknowledged missing schedule = warning.
- `[%%]` pkg/diff deleted; promotion trigger recorded (second flat-schema consumer).
- `[%%]` Web UI frontend deferred; only the DB-free API contract is built.
- `[%%]` Consumer regeneration+adaptation todos filed at the single final release.

## Grounding facts (source-verified across three critique rounds)

- resolveTable builds per-table collections by ranging Go maps — raw model order is
  nondeterministic. Ordering semantics: alphabetical (7 Sorted* helpers) vs
  TOPOLOGICAL (tables in Build; views/matviews/functions topo-sorted in TWO emitters
  — generate.go and python_ddl.go, duplicated; model-side table topo is a THIRD topo
  path that must relocate into Canonicalize with CycleGroups semantics preserved).
  Topo tie-break is input-order (graph/topo.go) — TOML declaration order vs
  introspect's ORDER BY name, an independent cross-source divergence axis.
  Introspect never populates function DependsOn. Matview indexes unsorted on every
  path and not covered by the 7 helpers. Luck-stable raw-map emitters: gorm,
  drizzle, jpa, sqlalchemy, validator policy extraction, python query-layer (~12
  sites). enrich() appends auto-FK indexes AFTER resolveTable — sorts must run
  post-enrich. Top-level type collections: DDL declaration-order vs JSON
  name-sorted. Existing determinism test hand-builds structs and can never fail.
- Introspect constructs Schema directly, never calls Build: nil FKGraph/
  TablesByName, raw query order. Finalize sequence copy-pasted between Build and
  BuildMulti. FilterByGroups/FilterBySource rebuild TablesByName but not FKGraph.
  FKEdge has no schema field; WalkCascade has no depth parameter.
- semtype.Registry: unexported/unserializable; scalar CHECKs + builtin shadowing
  live only there; typeDefsEqual ignores top-level Comment/Source but compares
  nested transition comments; builtins slug/email/short_text are scalar-with-CHECK
  and emit CREATE DOMAIN regardless of Source; TypeDef.Source doc comment is stale
  (three values). Type extends is eagerly inlined at load — closure needs
  composition references only.
- Headers: 36 codegen sites + 5 validator helpers + CLI sites; 7+ wordings incl. a
  no-period CLI fallback, an SQL variant, and seed's distinct no-do-not-edit
  wording. Codegen generators self-embed headers (planner prepend dead for codegen,
  LIVE for sql/d2/graphql); json and doc are the headerless build outputs.
  hasCommentHeader doesn't know `--`. Go headers don't match the
  `^// Code generated .* DO NOT EDIT\.$` tooling regex. codegen --check is
  byte-exact (pkg/genkit).
- Migrate: tracking table version/applied_at/checksum/description; checksum over
  file bytes, never verified; version row written last; no per-op records; partial
  phases + non-transactional ops leave committed DDL unrecorded; re-apply restarts
  at op 0. Rollback re-reads files. TWELVE unserialized op-family fields (nine
  pointer-def families + RawSQL — SM-trigger DDL and partman UPDATEs silently
  dropped on round-trip — + PartitionChildSpec + ParentTable); most degrade to
  comment stubs; create_function/create_trigger fall back to WRONG OBJECTS
  (deny-mutation / append-only); sequences lose params; DOWN-ops embed def pointers
  too and degrade on rollback. opCreateTable passes nil enum/domain lists
  (unqualified type rendering) and hardcodes pgVersion=0 despite DDLOp.PGVersion
  existing — latent version-gating bug. Generation consumes live TableStats
  (pg_stat_user_tables) to pick NOT VALID splits and expand/contract forms — a DB
  input to "generation". IsNonTransactional covers create_ AND drop_index_
  concurrently (drop has no IF EXISTS) and version-conditional enum-add.
  Non-transactional ops run on the raw conn between transactions.
- Squash deletes/rewrites originals; M200 guard only if --db passed; tracking rows
  orphaned; zero CLI tests; optimizeDDLOps keeps only the final type-change's down
  (reverts one step, not to pre-range type).
- migrate generate requires --db + --version; no ledger; discovery skips non-semver
  names; ~7 functions rely on semver order; migrations-dir sentinel hardcode at 8
  sites + a 9th adjacent in serve registration; the `output` flag shows the correct
  Default(nil)+was-set pattern. serve: handleMigrations queries pgdesign_migrations
  directly (500s after DROP); version endpoint opens `version+".toml"`.
- Introspect: no table-level filtering (tracking table reported as user table);
  function/trigger filters use the LEADING-underscore `_pgdesign_sm_%` pattern —
  `pgdesign_%` does not cover it (two patterns needed); views need relkind 'v'
  coverage. Differ compares expressions by raw string vs PG-rewritten forms
  (pg_get_*) — live false-drift bug on CHECKs/partial indexes/policies; only types
  normalize. Explicit schemaNames scoping exists in introspect handlers (imports
  exclusion mostly falls out; only reconcile must not auto-add imported schemas).
- serve: DB-coupled at construction; --timeout never enforced; audit synchronous
  TANE; GenerateD2 called with nil registry (SM diagrams silently dropped); local
  TOML loader discards the registry; project-loading helpers live in package main.
- strictcli: check command builds a full *Context and discards it; infra roots +
  handshake envs hermetic-immune, flag Env() hermetic-suppressed; per-flag
  provenance exists (Context.Source()). 13 --db flags, three default semantics.
- Codegen enum shapes: Go `type X string` + const block (const of STRUCT type would
  be illegal — branding members must become vars or funcs); TS literal union
  (compile-closed, exhaustive narrowing; TS transition maps ALREADY typed
  Record<Status, Status[]>); Python StrEnum (Enum.__call__ already validates —
  Status("bad") raises; the residual openness is raw str structurally satisfying
  StrEnum positions, unclosable); Java/Kotlin real enums (UPPER_SNAKE names vs raw
  getValue() values — @Enumerated(STRING) would persist NAMES); Zig string consts
  (Zig transition maps use sanitized struct-field keys, not raw strings). No parse
  helpers anywhere. Python query-layer neither imports nor defines the enum classes
  it annotates with (survives via `from __future__ import annotations`); PgBackend
  builds rows with no coercion point (Row(**dict(row))). constraints validators
  compare against raw strings in Go/Java/Kotlin (break or go always-false against
  branded types). go_types and go_gorm BOTH emit GenerateEnums into package schema
  (duplicate-type hazard). java_jpa emits multiple public classes per file (illegal
  Java — proof CI never compiles generated Java). CI runs only go vet/test; the
  cross-language conformance tests self-skip without python3/node/gradle/zig.
  build's d2/graphql/sql outputs get planner-prepended headers; json/doc do not.
  splitfmt (.sqlsplit) is sealed: line 1 must be the statement count — cannot carry
  a header. `fmt` rewrites schema TOML in place (--column-order reorders columns =
  revision change); `introspect --output` also writes source files. build applies
  per-output FilterByGroups/FilterBySource; standalone codegen does NOT filter —
  same artifact, two contents by entry point. build auto-commit warns-and-continues
  on safegit failure.
- CI: postgres:17 + pg_partman, PGDESIGN_REQUIRE_DB=1; 11 DB-backed migrate tests
  of ~162.
- Partition bugs: python_ddl Retention-as-p_interval; premake -> 0; silent skip
  without pg_partman; manual children + maintenance contradictory; pg_cron helper
  dead-but-tested.
- pkg/diff (exported stub): zero importers. internal/diff (real differ): ~22 model
  types consumed field-by-field by migrate. generate and migrate are siblings; both
  import internal/sql; internal/sqlparse is the go-pgquery-owning leaf
  (migrate/introspect/model import it); internal/sqlutil is imported by
  validate+codegen — homing normalization there would drag the WASM parser into
  both.

---

## Phase 0 — Foundational groundwork

Build order within the phase: 0.1 -> 0.2 -> 0.3 -> 0.4 (0.1 and 0.2 co-edit
generator files — the header pass lands first); 0.5/0.6/0.7 independent after 0.2.

### 0.1 Header consolidation (byte-preserving) + stamp grammar
- **What:** One shared parameterized header helper (language comment prefix + a
  free-text parameter for the distinct seed wording) routed through ALL sites: 36
  codegen sites, 5 validator helpers, the CLI planner-prepend path for sql/d2/
  graphql, codegenHeader/hasCommentHeader (which also learns `--`), and seed.
  Byte-preserving: each site's current wording reproduced exactly; the unification
  to one wording waits for 4.2 so consumers regenerate once. The stamp GRAMMAR
  (format + parser) is designed now in pkg/genkit — writer and reader in one
  package — with the helper in internal/codegen consuming it.
- **Why:** Phase 4 stamps a revision into every header and phase 6 reads the
  stamps; stamping through 40+ scattered literals with 7+ wordings means that many
  chances to miss one, and a missed stamp is invisible to enforcement. Grammar in
  genkit prevents writer/reader drift.
- **Verify:** Header-originating bytes identical before/after on fixtures; grep:
  zero header literals outside the helper; genkit stamp grammar round-trip test.

### 0.2 Canonical ordering via Canonicalize()
- **What:** A shared finalize routine — alphabetical ordering for per-table
  collections (incl. matview indexes) and top-level type collections
  (enums/domains/composites/sequences); topological ordering WITH ALPHABETICAL
  TIE-BREAK for tables/views/matviews/functions (replacing input-order tie-break,
  so TOML and introspect converge; introspected functions lack DependsOn and fall
  back to alphabetical); columns source-ordered; derived structures (FKGraph,
  TablesByName) built here — invoked by Build, BuildMulti, AND Introspect, and by
  FilterByGroups/FilterBySource (closing their stale-FKGraph hole). The model-side
  table topo relocates here too (CycleGroups semantics preserved) — all THREE topo
  paths (build, generate.go, python_ddl.go) collapse to this one. Sorts run
  post-enrich (auto-FK indexes). Delete the 7 Sorted* helpers and all emitter-side
  sorting; fix the luck-stable emitters (gorm, drizzle, jpa, sqlalchemy, validator
  policy extraction, python query-layer). Replace the determinism test with a
  multi-iteration TOML->Build->serialize->compare CI test (pinned iteration count;
  fixture with >=2 entries per map-sourced collection) + a Canonicalize
  postcondition. pg_version resolution moves from cmd into Build/Canonicalize
  (config input), so the model — and later the revision — is complete at
  construction. Stated: JSON goldens change ONCE (top-level order unification);
  byte-stability means across-runs.
- **Why:** The revision hash is a hash of bytes; nondeterministic bytes make
  identity meaningless. Anchoring in a shared finalize makes INTROSPECTED schemas
  canonical (0.5's verify, 3.3's serve path, 5.8's reconcile consume them),
  deduplicates the copy-pasted finalize and the three topo implementations, and
  the alphabetical tie-break shrinks cross-source divergence to the registry
  marker alone.
- **Verify:** Multi-iteration determinism test red before/green after;
  view-references-view fixture emits in dependency order; introspected schemas
  pass the same postcondition; matview-2-index + multi-FK fixture stable; grep
  finds no emitter-side sorting; filtered schemas carry correct graphs.

### 0.3 Schema-qualified identity + final graph API (single pass)
- **What:** The FKGraph/walker END-STATE API designed and landed once (per the
  collapse-multi-pass rule, instead of three planned passes): FKEdge gains schema
  qualification AND an `Imported bool` (consumed by phase 7); keys become
  (schema, name); cascade walkers get a depth-bounded signature (WalkCascade has
  no depth support today — 9.3 needs it); group resolution rekeyed.
- **Why:** Two identity schemes for one object is a latent bug today (same-named
  tables in two PG schemas collide in cascade analysis) and a guaranteed bug under
  imports; designing the final API now prevents 0.2->7.3->9.3 re-churn.
- **Verify:** Red-green: same-named tables in two schemas through cascade checks
  (W013/14/15), workload analysis, group filtering; depth-bounded walk tested;
  Imported flag present (unused until 7).

### 0.4 Canonical per-object serializer (the primitive) + registry snapshot
- **What:** THE canonical primitive, hoisted to phase 0 (everything else claims to
  reuse it — it must exist first): a DEDICATED canonical encoder (not struct-tag
  reflection — explicit field ordering; per-field presence semantics so
  value-typed optionals distinguish unset from zero, normalizing to pointers where
  needed) producing per-object canonical JSON for every schema object AND registry
  TypeDef. Registry gains a deterministic ordered snapshot/reconstruct whose field
  policy is explicit (semantic fields + ALL comments incl. nested transition
  comments; Source excluded; builtin-sourced entries INCLUDED WHEN they
  materialize as DDL — the builtin email/slug domains must affect identity;
  other builtins excluded). Fix the stale TypeDef.Source doc comment.
- **Why:** One primitive serves whole-model identity (3.1), op bodies (5.1),
  revision manifests (5.2), import surfaces (7.2), and the API payload (8.1).
  Built after 0.2 so its bytes are deterministic; built before everything that
  consumes it so no second dialect can ever appear. Builtin-derived domains must
  be inside identity or a builtin regex change would alter DDL without flipping
  the revision.
- **Verify:** Per-object bytes independent of neighbors/position and of struct
  field-order refactors; snapshot->reconstruct->snapshot byte-stable and
  registration-order independent; Source relabeling changes nothing; nested
  transition comments DO; builtin email-regex change DOES.

### 0.5 Introspect filters managed objects
- **What:** One `isManagedObjectName()` predicate implementing TWO patterns
  (`pgdesign_%` for tables AND views — relkind 'v' coverage added to the view
  queries — and the legacy `_pgdesign_sm_%` function/trigger prefix), applied
  consistently across introspection. A user object matching the reserved patterns
  triggers a diagnostic (it would silently vanish otherwise).
- **Why:** Reconcile demands "introspect, diff, expect empty"; today the tracking
  table introspects as a user table. Pattern-based filtering means future managed
  objects (journal, view, position) inherit coverage; the namespace reservation
  must be loud.
- **Verify:** DB-backed: introspect a migrated DB (tracking + journal + view +
  position present), diff, empty; reserved-name user table produces the
  diagnostic.

### 0.6 One write path; filtering unified; sentinel fix
- **What:** Consolidate multi-file write + owned-dir/orphan bookkeeping onto the
  planner; standalone codegen becomes a thin caller AND gains build's
  FilterByGroups/FilterBySource application (today the same artifact has two
  contents depending on entry point). Fix the migrations-dir sentinel at all NINE
  sites (8 in migrate handlers + 1 in serve registration) via one shared helper
  using the existing Default(nil)+was-set pattern.
- **Why:** Phase 6 enforcement must guard every write; divergent write paths and
  divergent filtering are guards that drift. Nine copies of a sentinel bug are
  nine chances to fix eight.
- **Verify:** Standalone codegen and build byte-identical incl. under group/source
  filters; identical orphan behavior; sentinel red-green at all sites.

### 0.7 Comparison-normalization primitive
- **What:** One shared normalization primitive — types, defaults, expressions
  (parse/deparse both sides) — homed in `internal/sqlparse` (the go-pgquery-owning
  leaf; NOT sqlutil, which would drag the WASM parser into validate and codegen).
  The differ adopts it immediately (red-green: introspect->diff over a schema with
  CHECK constraints, partial indexes, and policies reports false drift today — a
  live shipping bug). Later consumers: 5.2 upgrade reconcile, 5.7 preconditions,
  5.8 reconcile-verify, the shadow test.
- **Why:** Three comparison engines eventually exist (differ, precondition
  executor, SQL guards); without one shared normalization they disagree on the
  same object — and the differ already disagrees with PG's rewritten forms today.
  Hoisted to phase 0 because 0.5's verify cannot pass without it.
- **Verify:** Red-green on the false-drift fixture; normalization unit suite
  (PG-rewritten forms equal their sources); diff --live clean on the comprehensive
  fixture (reused verbatim by 5.8).

## Phase 1 — Ground-clearing

### 1.1 Delete pkg/diff
- **What:** Remove the exported stub package; changelog records the promotion
  trigger (a public differ only when a second flat-schema consumer exists).
- **Why:** An exported API unusable without internal imports is worse than none;
  zero importers confirmed; the trigger keeps the door honest.
- **Verify:** Package gone; build + vet clean.

### 1.2 Partition bug fixes (red-green each)
- **What:** Fix python_ddl.go passing Retention as p_interval (sibling of the
  v0.24.4 generate fix). premake becomes REQUIRED (hard parse error; today's
  silent zero emits p_premake := 0, disabling partman's future-partition
  creation). Hard errors: non-RANGE strategy with maintenance; [maintenance]
  without pg_partman declared (today silently skipped); maintenance + manual
  partition children (today contradictory DDL). The silent part_config query
  failure becomes a diagnostic.
- **Why:** Every item is silent degradation: configs that look accepted but
  produce broken DDL discovered in production partitioning. Loud at compile time
  is the entire value of a schema compiler.
- **Verify:** Failing test first per bug, then fix; CI's postgres+pg_partman
  container exercises the guards live.

### 1.3 Partition lifecycle completion
- **What:** Introspection reads interval/premake/retention from part_config into
  the model. Diff distinguishes initial partman setup (emit create_parent) /
  retention-premake updates (Safe, risk-classified UPDATE part_config ops) /
  interval changes (hard error + repartitioning guidance). Migrate guards on
  extension presence. New `schedule` key emits the pg_cron job via the existing
  dead-but-tested helper (pg_cron must be declared — hard error otherwise); no
  schedule and no external-scheduler acknowledgment = validation warning.
- **Why:** Partitioned tables are where the schema is alive — partman creates
  children at runtime. A tool that creates partman config but cannot see, evolve,
  or schedule it automated the setup and abandoned the lifecycle. Dead helper
  wired per dead-code policy.
- **Verify:** Golden DDL for schedule emission; diff/migrate tests per transition
  class; live introspect round-trip in CI.

### 1.4 Squash safety stopgap
- **What:** Until phase 5 replaces squash: `--db` and the M200 applied-version
  check become mandatory. Stated limits: blocks legitimate offline squash of
  never-applied ranges (acceptable interim) and does NOT fix the rewrite/
  orphaned-row mechanics (phase 5 does). Includes the FIRST test of the squash
  CLI flow.
- **Why:** Squash today deletes files whose checksums production tracking tables
  record, with the DB check opt-in — a guardrail whose escape hatch is the
  default.
- **Verify:** Squash without --db hard-errors; overlapping applied versions
  refuses; CLI-flow test exists.

## Phase 2 — Connection environment

### 2.1 strictcli: connection-env kind + check context access
- **What:** Third env primitive — hermetic-SUPPRESSED, lazily read, no implicit
  default — plus a REGISTRATION-TIME hard error when a command's --db-class flag
  is not bound to a declared connection env (agent-proof enforcement). Check
  framework gains env access: CheckContext interface widening + reconciling the
  two context construction paths (the check command already builds a fully
  populated *Context and discards it). No new provenance machinery — per-flag
  source labels exist (Context.Source(), env>config>default precedence).
  Execution: generically-worded todo filed in the strictcli repo; a strictcli
  session implements and releases; pgdesign bumps and adopts.
- **Why:** A connection URL is precisely what --hermetic should suppress, yet
  both existing primitives survive hermetic and flag Env() is unavailable to
  checks. Registration-time enforcement replaces "checked at review" with a
  mechanical guarantee.
- **Verify:** strictcli tests: declaration, lazy read, hermetic suppression,
  check-side access, registration-time unbound-flag error; schema dump includes
  the new kind.

### 2.2 pgdesign adoption
- **What:** Declare PGDESIGN_DB once; bind all 13 --db flags (normalizing three
  default semantics); provenance surfaced via Source(); checks read via the
  framework; --hermetic makes DB checks skip visibly; config-file [database].url
  stays a separate documented resolution layer (cli > env > config). Phase 2 is
  NOT a leaf: every later phase's new DB entrypoint (revise's DB tier, import
  lock/update + live verification, seed tier-1) binds the connection env from
  birth — enforced by 2.1's registration-time error.
- **Why:** One variable, one story: today checks honor the env var while commands
  ignore it. Without the non-leaf edges, later phases would re-add raw --db flags
  and the pathology regrows.
- **Verify:** Env-only invocation works on every DB command with a provenance
  line; hermetic run shows explicit skips; raw os.Getenv gone from cmd/ (test
  harness excepted); precedence test.

## Phase 3 — Schema identity

### 3.1 Whole-model canonical form + envelope
- **What:** Consuming the 0.4 primitive: whole-model form = versioned preamble +
  ordered concatenation of per-object forms (order per 0.2). Content policy:
  SEMANTIC-ONLY — registry snapshot is the source of truth for type definitions
  (schema-side duplicates StateMachineTransitions and derived CycleGroups
  excluded; FKGraph/TablesByName/candidate-key caches excluded); Extensions
  (ordered) and PGVersion INCLUDED (both change emitted DDL; extension DDL-name
  resolution stays emitter-side, byte-compare-covered — baking resolved names
  into the model is the recorded summit alternative); object comments IN,
  TOML-formatting comments OUT; [suppress] and extension-registry data OUT.
  Explicit "registry absent (introspected)" marker. The JSON ARTIFACT is an
  envelope {format_version, revision, model} with the canonical bytes embedded
  VERBATIM via raw-message inclusion (re-encoding would break revision ==
  hash(model)); a per-field presence/omission policy table in the format spec.
- **Why:** One canonical answer to "what IS this schema," serving identity,
  imports, and the API. The envelope resolves in-band-stamp circularity (bytes
  cannot contain their own hash); the explicit policy table keeps hash stability
  deliberate.
- **Verify:** Byte-identical across repeated builds and struct refactors; golden
  fixture; comment edit changes bytes; Source relabeling does not; pg_version
  change flips the revision; extension add/remove flips it; envelope revision
  verifies against embedded bytes.

### 3.2 Revision hash
- **What:** Revision = SHA-256 of the whole canonical stream; per-object hashes =
  SHA-256 of each object's canonical bytes (same primitive — these key the
  migrations/objects store). Exposed from model; printed by validate/build.
  Stated policies: a pgdesign upgrade that changes the model schema flips all
  revisions and forces one coordinated regeneration (the existing
  consumer-regeneration convention, now load-bearing); revisions are NEVER
  compared across the registry-present/registry-absent boundary (only diff
  crosses it). Conformance, one-directional: revision-equal implies diff-empty.
  Diff fast path on equal revisions.
- **Why:** The revision is the coupling primitive — what migration edges,
  headers, the journal, and enforcement agree on. The conformance test makes the
  serializer and differ police each other's semantic coverage.
- **Verify:** Sensitivity tests (comment/column/registry/pg_version flips; no-op
  rebuild doesn't); boundary-comparison is a programming error with a test;
  conformance in CI; fast path exercised.

### 3.3 One serializer everywhere
- **What:** generate's json format and serve's schema responses call the SAME
  canonical-serializer/envelope function; the two divergent serializers die;
  introspect-sourced responses carry the registry-absent marker.
- **Why:** Two serializers for one struct is how the nondeterminism bug survived.
  Any consumer must see THE schema.
- **Verify:** generate json and serve bodies identical for the same schema;
  marker present on the introspect path; goldens updated once.

## Phase 4 — Codegen breaking release (content lands in the single final release)

### 4.0 CI toolchain provisioning (hard prerequisite)
- **What:** CI gains compilation/type-check of generated fixtures: go build of
  generated Go (types+gorm+constraints), tsc --noEmit, pytest incl. pickle and
  PgBackend round-trips, javac (which will immediately catch the EXISTING
  multiple-public-classes-per-file illegal-Java bug in JPA output), kotlinc, zig
  build where feasible. The existing conformance tests' self-skip-when-missing
  becomes provision-so-they-run.
- **Why:** 4.1's per-language verify goals are currently backed by nothing — CI
  runs only go vet/test and the conformance suite self-skips. Claims without
  execution are hopes.
- **Verify:** All fixture compilations run in CI (no skips for provisioned
  toolchains); the JPA illegal-Java bug is caught red then fixed green.

### 4.1 Branding per language — corrected mechanisms, full surface
- **What:** Shared mechanism first (extend the enum_gen dispatch seam; dedup enum
  emission so co-generated modes — go types + gorm both emitting into package
  schema today — produce the enum block exactly once). Go: opaque struct
  (unexported value field); members are package-level VARS (const of struct type
  is illegal; deliberate reassignment documented out-of-scope); Parse errors on
  unknowns; UnmarshalJSON/UnmarshalText/sql.Scanner IMPLEMENTED VIA Parse
  (validating, never absent — generated structs live in DB-scanned and
  JSON-round-tripped positions); Valuer/MarshalJSON/Stringer for egress; zero
  value detectably invalid. Python: parse() classmethod as ergonomic typed alias
  (Enum.__call__ already validates — no construction-closing machinery, no pickle
  override); query-layer + validator signatures enum-typed; the query-layer
  package gains real imports/definitions of the enum classes it annotates with;
  PgBackend read path coerces via dataclass __post_init__ (branding must close
  the READ door). Stated honestly: StrEnum members still == raw strings. TS: KEEP
  the literal union (compile-closed, exhaustive narrowing — a nominal brand would
  regress); add parse() at boundaries; transition maps are ALREADY typed — no
  re-typing work. Java/Kotlin: value-based parse; JPA gains a generated
  AttributeConverter (@Convert) backed by getValue()/fromValue() — NOT
  @Enumerated(STRING), which persists constant NAMES (IN_PROGRESS) instead of DB
  values; JPA generator becomes a MultiFileGenerator (converter classes; fixes
  the one-public-class-per-file rule) and gains the currently-missing enum-column
  branch. Zig: wrapper struct + parse; transition maps re-keyed to the wrapper
  (three sites: resolver, value emitter, transition maps). Constraints
  validators RE-TARGET the branded representation (Go switch on .String(); Java
  contains(getValue()); Kotlin equivalents) — they break or go always-false
  otherwise. Stated exceptions: constants mode unchanged (can only name valid
  states); drizzle/sqlalchemy string-shaped by ORM necessity.
- **Why:** The drift class (consumer names a state the schema lacks; runtime
  crash at the DB) dies when invalid values cannot be named or smuggled: compile
  error where the language can express it, VALIDATING error at every ingress
  (JSON/DB/string), DB CHECK as backstop. Mechanism corrections matter: rejecting
  (vs validating) boundaries would break DB scans; @Enumerated would write wrong
  values on every insert; TS branding would regress; Python closing was chasing a
  phantom (native validation already exists).
- **Verify:** Per language: invalid values fail at the earliest boundary WITH
  ERRORS; Go fixture: all ingresses validate and valid values round-trip; Java
  fixture: persisted value == getValue() not name(); Python: PgBackend yields
  enum-typed fields, pickle round-trips; TS: exhaustive switches still compile;
  constraints validators pass against branded fixtures; all under 4.0's CI
  toolchains.

### 4.2 Header unification + revision stamping
- **What:** The shared header (0.1) adopts ONE wording — the Go-tooling-
  recognized `Code generated ... DO NOT EDIT.` convention (fixing today's
  non-conformant Go headers), per-language comment prefix, revision line, stamp
  format-version — landing as one rewrite with 4.1. Artifact-class taxonomy:
  comment-stamped = sql, d2, graphql, codegen, seed, doc (headerless today —
  previously missed); in-band-stamped = json (envelope field per 3.1);
  structurally exempt = svg (non-deterministic rendering) and .sqlsplit (sealed
  format — line 1 must be the statement count; a header breaks Decode; freshness
  stays byte-compare-covered). The stamp is the FULL-PROJECT revision always —
  provenance, not content; group/source-filtered outputs carry the same
  full-project stamp and their content freshness stays byte-compare's job.
  Stated cost: partial regeneration impossible — one schema edit re-stamps every
  generated file (intended; enforcement depends on it).
- **Why:** The stamp is how artifacts say which schema they came from — the raw
  material of phase 6's divergence guarantee. Full-project stamping resolves the
  filtered-output paradox (a subset's "own" revision would certify nothing
  useful). The exceptions are named and bounded because a missed class silently
  undermines "every artifact carries the stamp."
- **Verify:** Rebuild-without-change keeps freshness green; a schema edit flips
  every stampable output stale exactly once; doc stamped; .sqlsplit byte-stable
  and Decode-able; json envelope carries revision; filtered outputs carry the
  full-project stamp.

### 4.3 Breaking-change packaging (in the single final release)
- **What:** Breaking-typed changelog entries for: branding (per language),
  header wording, revision stamps, generate --idempotent's RAISE-on-mismatch
  (from 5.7), and constraints-validator re-targeting. Consumer todos filed in
  each consumer repo at THE release (naming pgdesign there is fine — their
  declared dependency) with regeneration + adaptation notes: Python raw-string
  construction sites move to parse()/members; Go call sites comparing enum
  fields to string literals adapt to the struct type; co-generated Go modes note
  the enum-emission dedup; TS gains parse() at boundaries (switches keep
  compiling); JPA consumers pick up converters. Modes in the wild: python ddl
  faceted; python validators+constants; zig constants; generated SQL headers.
- **Why:** All consumer-visible changes, one break, one adaptation, honest
  handoff — red CI is a hostile messenger.
- **Verify:** rlsbl changelog coverage passes with breaking entries; consumer
  todos filed at release; consumer drift-check scripts pass after regeneration.

## Phase 5 — Migrate integrity

### 5.0 Schema and format design (prerequisite subphase)
- **What:** Before any implementation: the complete designs of (a)
  `pgdesign_migration_ops` (op identity: migration ref, phase, sequence, op
  kind, target; serialized down-op; intent/confirm status), (b)
  `pgdesign_applied_migrations` view definition, (c) `pgdesign_chain_position`
  (current revision, in-progress edge ref, per-database grandfather boundary),
  (d) the migration file format (sequence+slug name; from/to revision; ops
  referencing store objects by hash), (e) the chain-edge file format
  (migrations/chain/<from>-<to>.json), (f) the store layout
  (migrations/objects/<hash>.json, migrations/revisions/<revision>.json), and
  (g) the archive layout (migrations/archive/).
- **Why:** 5.2's upgrade migrates rows INTO these schemas and every later
  subphase reads them — per planning discipline the designs precede the
  implementation order rather than being discovered mid-phase.
- **Verify:** Design doc section in this file's history + schema DDL fixtures
  reviewed before 5.1 starts.

### 5.1 Self-contained ops via the object store
- **What:** Every pointer-def op REFERENCES its target object — and the
  transitive closure of composition-referenced type definitions (enums, domains,
  composites; extends parents are pre-inlined and need nothing) — BY CONTENT
  HASH into migrations/objects/ (no inline JSON-in-TOML: the hand-rolled TOML
  writer would %q-escape blobs into unreviewable one-liners). OpToSQL
  reconstructs objects from the store and renders — a total function of
  (file + store). All TWELVE unserialized families covered: the nine pointer-def
  families plus RawSQL (SM-trigger DDL, partman UPDATEs — silently dropped
  today), PartitionChildSpec, and ParentTable. DOWN-ops get the same treatment
  (they embed def pointers today and degrade on rollback). Every comment-stub
  no-op and wrong-object fallback (deny-mutation / append-only) DELETED;
  sequences keep parameters; opCreateTable passes op.PGVersion (hardcoded 0
  today — latent version-gating bug) and resolves enum/domain qualification from
  the closure. Table-driven round-trip test per family — up AND down — on a
  fixture containing an enum column, a domain column, and a version-gated
  generated column, asserting rendered SQL equals generate's output.
- **Why:** A migration file that renders different SQL than intended — empty, or
  the WRONG OBJECT — is the worst artifact the tool can produce; today that's
  possible for eleven families and actual for several. Store-referencing keeps
  ops thin and reviewable, reuses the one canonical primitive, and makes
  degraded states unrepresentable (no lossy mirror exists to degrade into).
- **Verify:** Round-trip table test covers all twelve families, up and down;
  wrong-object fallbacks gone (grep + tests); the enum/domain/version-gated
  fixture renders byte-identically to generate.

### 5.2 Chain, stores, and `migrate upgrade`
- **What:** Chain edges as one file per edge in migrations/chain/ (the DAG is
  the data; git merges never conflict textually); revision manifests in
  migrations/revisions/ (ordered object->hash lists — consecutive revisions
  share object files); chain-head/find-heads API (genesis: empty chain -> null
  parent). Discovery/ordering rewritten off semver (~7 functions; discovery
  currently skips non-semver names). Filenames: sequence + slug auto-derived
  from the diff's dominant change (override flag). `migrate upgrade` (one-time,
  explicit): requires clean schema files per git when in a repo (existing
  gitShow plumbing; outside a repo proceeds with a stated caveat); acquires the
  advisory lock; sequence INSIDE ONE TRANSACTION: snapshot the old applied set
  -> create journal/view/position -> migrate tracking rows -> ASSERT the view
  reproduces the snapshot -> DROP the old table -> COMMIT. Verify-then-stamp:
  requires a clean TOML<->DB reconcile (0.7 normalization) or refuses with the
  drift report; stamps the per-database boundary revision =
  revision(current TOML model) into pgdesign_chain_position. File writes
  (objects, revisions, chain edges) are content-addressed and idempotent,
  written BEFORE the DB commit; the DB commit is the sole commit point; on-disk
  state is reconciled from chain position on next run. Existing semver files
  become the linear chain prefix with SYNTHETIC checksum-verified (not
  model-derived) revisions. serve updated: handleMigrations repointed from the
  dropped table to the view; the version endpoint's `version+".toml"` file
  lookup updated for sequence+slug names. Store<->chain<->files consistency
  check.
- **Why:** Revision pairs give migrations identity tied to the schemas they
  transform; per-edge files make the chain merge-friendly; the store makes
  everything (ops, revisions, imports) one mechanism. The upgrade choreography
  exists because assert-after-DROP is unexecutable, concurrent binaries need the
  lock, and files outside the transaction need an idempotence-based recovery
  protocol — otherwise the upgrade recreates the durability disease it fixes.
- **Verify:** Crash-injection around upgrade (kill before commit: old world
  intact; kill after commit before file writes: next run reconciles files from
  DB state); dirty-tree refusal; mid-edit TOML cannot stamp an unapplied model;
  reconcile-refusal path shows the drift report; store consistency check red on
  tamper.

### 5.3 Append-only squash (consolidation edges)
- **What:** Squash reimplemented: a consolidation migration is an ADDITIONAL
  chain edge; superseded files retire INTACT to migrations/archive/ and remain
  reachable via their edges; a database mid-way through a squashed range applies
  the remaining originals (edge selection by pgdesign_chain_position), fresh
  databases take the consolidation edge, fully-applied databases skip it.
  Consolidation DOWN-ops are derived by diff(from_manifest, to_manifest) over
  the revision manifests — NOT by copying surviving ops' downs (today's
  optimizeDDLOps keeps only the final type-change's down, reverting one step
  instead of to the pre-range type). Tracking/journal lineage handled: no
  orphaned rows. Files are never rewritten, period.
- **Why:** Mutation of applied artifacts stops being an operation; the "file
  changed after apply" class becomes unrepresentable. Snapshot-derived downs
  make the rollback-equivalence invariant true by construction.
- **Verify:** Squash of applied migrations succeeds via consolidation; a
  mid-range DB resumes via archived originals; rolling back the consolidation
  edge and rolling back its superseded originals reach the SAME prior revision
  (incl. a merged-type-change fixture); no orphaned rows; archive intact.

### 5.4 Unconditional checksums
- **What:** Only now — after the format (5.2) and append-only history (5.3) —
  checksum verification becomes unconditional on apply AND rollback (rollback
  never checksums today): any mismatch = corruption, hard error naming the file.
  Prefix files carry synthetic revisions whose checksums ARE verified.
- **Why:** Enforcement before the format would brick users; after it, mismatch
  has exactly one meaning.
- **Verify:** Tamper tests: edited file refuses apply AND rollback with precise
  reports; upgraded fixture applies cleanly.

### 5.5 Applied-op journal
- **What:** `pgdesign_migration_ops` + `pgdesign_applied_migrations` per 5.0.
  Records, as each op completes: op identity AND the serialized down-op (via the
  store, per 5.1). Per-op-class TIMING: transactional ops journal INSIDE the
  op's transaction (atomic with the DDL); non-transactional ops (create AND drop
  index concurrently, version-conditional enum-add — enum-add is transactional
  on PG12+) use INTENT-then-CONFIRM rows plus mandatory idempotent SQL forms
  (CREATE INDEX CONCURRENTLY IF NOT EXISTS; DROP INDEX CONCURRENTLY IF EXISTS —
  its renderer lacks IF EXISTS today) so a crash between DDL and journal is
  recoverable; the same protocol applies to journal-driven rollback of
  non-transactional down-ops. pgdesign_chain_position updates in the same
  transaction as each edge-completing journal write. Re-apply resumes by
  skipping confirmed ops. AppliedVersions/status/serve read the view.
- **Why:** The version row is written LAST today; every committed phase or
  non-transactional op before a failure is real DDL with no durable record, and
  re-apply restarts at op 0 and aborts forever. A journal row written AFTER a
  non-transactional commit can itself be lost — intent/confirm + idempotence
  closes the gap at its own level. Down-ops in the journal are what make
  rollback database-driven.
- **Verify:** Fault injection: mid-phase, after CREATE INDEX CONCURRENTLY,
  after DROP INDEX CONCURRENTLY, around enum-add on both PG version classes —
  re-apply converges with correct journal state; view semantics equal the old
  applied-set semantics.

### 5.6 Journal-driven rollback (scoped)
- **What:** Rollback executes the journal's recorded down-ops in reverse journal
  order — files not consulted (archived or not). SCOPE: guaranteed from the
  upgrade boundary forward; the pre-upgrade prefix and baselined migrations are
  ROLLBACK-FROZEN (crossing the boundary = hard error naming it; synthesized
  rows carry no executable down-ops; old tracking rows and "baseline" checksums
  cannot yield them). Reversibility pre-check retained (irreversible ops
  journaled as such).
- **Why:** Rollback today re-reads files and trusts them absolutely — it will
  invert ops that never ran (the no-op-ADD/DROP-COLUMN data-loss case) or
  follow an edited file. Recorded reality closes both; honest scoping is not
  compat — history before the boundary simply lacks the data.
- **Verify:** Rollback after partial apply drops nothing it didn't create;
  works with source files archived; boundary-crossing rollback refuses with the
  precise error.

### 5.7 Preconditions + predicate IR
- **What:** Before each op, a predicate against pg_catalog asserts expected
  prior state per op class (absent for creates; present-and-matching via 0.7
  normalization for alters/drops); any unexpected state = hard error naming
  object, expected, found. DML ops (backfill, transform, batched loops) are
  explicitly precondition-free — arbitrary SQL has no catalog precondition.
  Predicates are structured data (catalog query + expected shape) in a shared
  leaf package `internal/predicate` (generate and migrate are siblings — the IR
  and SQL renderer must live below both; only the pgx executor lives in
  migrate). Two backends: the Go executor (apply-time and 7.4 verification —
  structured diagnostics) and a SQL renderer compiling the same structures into
  DO-blocks for generate --idempotent, which thereby RAISEs on definition
  mismatch instead of silently skipping (listed in 4.3's breaking notes). CI
  conformance matrix: both backends + the differ as a third leg where object
  classes overlap, against the same live database states (match / missing /
  each mismatch class), asserting identical verdicts.
- **Why:** Blind DDL is how drift gets tolerated or amplified. The IR dissolves
  the second-source-of-truth objection structurally (one definition, two
  compilations) and the matrix makes non-drift a tested property. The Go
  executor is retained deliberately: structured object/expected/found
  diagnostics from Go-side catalog queries beat parsing RAISE text, and moving
  all comparison into generated SQL would re-create the disease in the worse
  direction.
- **Verify:** DB-backed matrix per op class (wrong-type column, missing table,
  mismatched constraint — each precise); golden idempotent SQL; mismatched
  pre-existing column makes the idempotent script RAISE, matching state no-ops;
  conformance matrix green in CI.

### 5.8 Post-apply reconcile-verify
- **What:** After apply: introspect (0.5 exclusions; canonical via 0.2) + diff
  with 0.7 normalization against the target model; any residual mismatch = hard
  error listing every divergent object. Reconcile does not auto-add imported
  schemas (7.4). SM-vs-enum introspection lossiness documented as a comparison
  boundary. Asserts the one-directional conformance (revision-equal implies
  diff-empty) on the same comprehensive fixture as 0.7's red-green.
- **Why:** Preconditions check ops locally; reconcile checks the combined
  result globally — out-of-band changes mid-migration, op interactions,
  generator bugs — reusing the real differ for complete coverage with zero
  bespoke verification code.
- **Verify:** Clean apply over the comprehensive fixture (CHECKs, partial
  indexes, policies) reports empty; out-of-band ALTER mid-migration surfaces in
  the report.

### 5.9 Pure chain-based generation
- **What:** migrate generate = diff(deserialize(head revision manifest via the
  object store), current model) — pure, no DB. Generation ALWAYS emits the
  large-table-safe forms (NOT VALID + VALIDATE for FK/CHECK; backfill-then-
  set-not-null; expand/contract phasing where applicable) unconditionally — a
  revision manifest carries no row counts, and row-count-conditional output
  would make the same input produce different migrations. QueryTableStats and
  the stats plumbing are deleted from the generate path (dead code). Drift is
  caught at apply (5.7/5.8), never folded into generated migrations;
  intentional drift adoption via the baseline flow (baseline writes
  pgdesign_chain_position + a revision manifest).
- **Why:** Same TOML edit must produce the same migration regardless of DB
  state. The always-safe form is safe at any table size and merely adds steps;
  it is what makes purity real rather than aspirational.
- **Verify:** Generation without any DB from a chain fixture; an FK add emits
  the two-step NOT VALID form with no DB present; a drifted DB does NOT alter
  generated output but fails apply with the precondition report; stats plumbing
  gone.

### 5.10 Fork resolution + ecosystem alignment
- **What:** `migrate rebase <head>`: re-parents a fork's tail onto the chosen
  head, recomputes from/to revisions, re-derives revision manifests (per-edge
  chain files make forks a semantic condition, not a textual conflict).
  Shadow test, baseline, squash CLI, and docs updated for
  format+journal+chain+store; migration-guide rewritten.
- **Why:** Two branches each appending a migration is a normal workflow event;
  two-head detection without a resolution path is a dead end.
- **Verify:** Fork fixture: rebase re-parents, revisions recomputed, store
  consistent; shadow test passes on the comprehensive fixture; full migrate
  suite green.

## Phase 6 — Orchestration and enforcement

### 6.1 pgdesign revise
- **What:** New top-level command. PURE tier: build planner + 5.9 generation
  (parent = chain head; two heads = hard error naming both, pointing at migrate
  rebase; genesis handled). DB tier (bound to the phase-2 connection env): live
  import verification (7.4) + DB checks (nf, workload) — findings are
  NON-RETROACTIVE: they fail the command loudly but do not invalidate the
  already-committed migration (the next revise incorporates fixes). Separate
  safegit commits: pure outputs, then migration+chain+store. Commit failure =
  hard error — and build's existing warn-and-continue on safegit failure is
  flipped to hard error in the same pass, via one shared commit helper. Partial
  failure keeps committed pure outputs and exits non-zero naming the skipped
  tier.
- **Why:** The forgotten-step failure mode is real (four commands per schema
  change). revise is "I edited the TOML — make everything consistent and tell
  me what's wrong," without eroding build's purity: with 5.9, even migration
  generation is pure, so the DB tier is exactly the genuinely-live work.
  Separate commits reflect the two artifact lifecycles.
- **Verify:** End-to-end: edit TOML -> revise -> regenerated outputs + chained
  migration + store entries, two commits, one revision everywhere.
  DB-unreachable: pure tier complete and committed, non-zero exit naming the
  skipped tier. Two-head fixture errors with the rebase pointer. Commit-failure
  fixture hard-errors.

### 6.2 Revision enforcement
- **What:** The invariant: all regenerable snapshot artifacts in the planner
  set carry the ONE full-project revision after any write. Writer taxonomy:
  FULL regenerators (build, revise) always allowed — they re-stamp the set;
  PARTIAL writers — exactly one exists: `codegen --output` — refuse when
  artifacts they would not rewrite carry a different revision;
  SOURCE-EDITING writers (fmt, introspect --output) are outside the invariant
  but CHANGE the revision (fmt --column-order reorders columns = canonical-byte
  change) — they print a notice that a build/revise must follow, and the check
  catches staleness. OUTSIDE the invariant, stated: migration files + chain +
  store (append-only at historical revisions — covered by the store consistency
  check), seed output (stamped, not planner-set), stdout output of `generate`
  (covered at check time only — stated). Missing/old-format stamps = stale
  (full regenerators proceed; the partial writer refuses); stamp
  format-version prevents post-upgrade lock-out. The revision CHECK (error
  severity, sibling of build-freshness) covers what byte-compare cannot:
  chain/store integrity (edge continuity, single head, manifest<->object
  consistency), cross-artifact stamp agreement, standalone artifacts. genkit's
  stamp-extractor (grammar from 0.1) complements the byte-compare loop —
  byte-compare says "this file isn't what the model produces"; stamp-compare
  says "a sibling I'm not regenerating is at a different revision."
- **Why:** Divergence is created by partial writes and source edits, resolved
  by full ones; the taxonomy makes each writer's obligations explicit, and
  full-project stamping resolves the filtered-output paradox. Scoped to
  reality: one partial writer exists; naming the exclusions prevents them from
  becoming unclassified stamp-disagreement sources.
- **Verify:** TOML edit then build succeeds (re-stamps all); TOML edit then
  codegen --output of one output refuses naming stale siblings; fmt prints the
  follow-up notice and the check goes stale until build; tampered header
  caught; chain-continuity violation caught; seed/migration/stdout artifacts
  never flagged by the planner-set invariant.

## Phase 7 — Imports

### 7.1 Declaration and reference syntax
- **What:** [imports] config parsing (alias -> git URL + ref + target PG
  schema). `alias:table` accepted ONLY in FK ref_table (the single dot-split
  site); alias resolution runs BEFORE dot-split; aliases anywhere else
  (depends_on, groups) are hard errors naming the supported sites. Diagnostics:
  unknown alias, unresolvable target, collisions.
- **Why:** References should name the DEPENDENCY, not a physical schema string
  — provenance visible at the reference site, renames touch one line, a typo'd
  alias is a resolution error rather than a plausible-looking phantom schema.
- **Verify:** Parse/build tests; alias typo -> resolution error; precedence
  test; alias-in-depends_on -> the scoping error.

### 7.2 Surface snapshot and pinning
- **What:** `pgdesign import lock` resolves the pin (git URL + ref; fetch via
  the git plumbing; no DB needed), parses the framework's schema TOML, and
  vendors the import surface into `imports/<alias>/`: referenced tables PLUS
  the transitive closure of COMPOSITION-referenced type definitions (enums,
  domains, composites, SM types their columns use; extends parents are
  pre-inlined and need no snapshotting) — each object stored via the 0.4
  canonical primitive with its per-object hash, plus a lockfile entry (source
  URL, ref, resolved commit, surface hash). `pgdesign import update` re-pins.
  `check --tag imports` re-derives the surface from the pin and reports
  SEMANTIC drift at column level ("framework column X changed uuid->bigint,
  breaks app.users.principal_id"), hard-failing CI. Requirement granularity:
  extensions inferred per referenced object from the surface; pg_version
  carried as the framework's floor (consumer must re-declare >=).
- **Why:** Machine-specific committed paths are banned; unpinned imports drift
  silently. Content-addressed vendored surfaces give reproducible offline
  builds; the semantic column-level errors are what make this better than a
  generic lockfile; the type closure is what makes "imported enums usable in
  columns" have data at all.
- **Verify:** Two-project fixture: drifted referenced column type -> the check
  names the exact column and breaking FK; unreferenced changes silent; offline
  build from the vendored surface; per-object hashes stable across unrelated
  framework edits; enum closure present and usable.

### 7.3 Model integration
- **What:** ImportedTables split slice on Schema (owned tables in Tables —
  every consumer iterating Tables is correct BY OMISSION). Integrity machinery
  explicitly unions owned+imported at the known non-TableByName sites:
  BuildFKGraph (edges keyed (schema,name) with Imported=true per 0.3) and
  seed's FQN pool maps. Registry collisions between imported and local types =
  hard error naming both sources; imported enums usable in columns; extension/
  pg_version re-declaration enforced per 7.2's granularity (hard error naming
  the requiring object).
- **Why:** Fail-closed by construction — forgetting imports means no leak, not
  a leak — but only where resolution funnels through the union; the two bypass
  sites are named because they would otherwise produce phantom graph nodes and
  dangling seed FKs.
- **Verify:** E204 resolves imported targets; FKGraph contains imported nodes
  correctly keyed and flagged; seed resolves FK values against imported pools;
  DDL/audit/codegen outputs contain zero imported-table artifacts; collision
  and re-declaration tests.

### 7.4 Downstream sweep
- **What:** Generate emits app-only DDL with schema-qualified FK constraints.
  Diff/migrate exclude imported tables from add/drop; reconcile (5.8) does not
  auto-add imported schemas (introspect's explicit schemaNames scoping already
  covers the rest). `migrate generate`'s live import verification — referenced
  imports present and matching in the target DB, hard error otherwise —
  consumes the 5.7 predicate executor (a catalog-predicate check by
  definition; bound to the phase-2 connection env). Audit, design checks,
  orphan warnings skip imported tables. Codegen skips them. Seed tiers as a
  branch in FK-value resolution: tier 1 (DB available): pre-populate imported
  value pools from real keys (deterministic sorted selection; Zipf and COPY
  work unchanged); tier 2 (offline): count-wrapped ordered-offset subqueries in
  INSERT mode (OFFSET n % GREATEST(count,1)); tier-2 HARD-ERRORS when the
  imported FK participates in a UNIQUE constraint (identical subquery strings
  would silently exhaust the dedup retry — a silent-degradation hole otherwise)
  and drops Zipf (stated); tier 3: hard error only for
  offline+COPY+NOT-NULL-imported-FK, naming all three constraints. D2/GraphQL
  render imported tables as minimal reference shapes (a first-class shape
  class 9.x preserves) so edges never dangle.
- **Why:** Ownership discipline end-to-end: the framework's objects are facts
  the app consumes, never regenerates, audits, or fabricates rows for; the
  error surface bans exactly the impossible and the silently-wrong, nothing
  more.
- **Verify:** Per-package fixture assertions; live verification via the
  executor (present passes; absent and mismatched fail with specifics); seed
  tier tests incl. determinism, small-table offset wrap, the UNIQUE hard
  error, and the triple-constraint error; D2 golden compiles.

## Phase 8 — Read API

### 8.1 DB-free serve mode
- **What:** Pool becomes optional; --db optional in project-schema mode. ONE
  shared project-loading helper returning (schema, registry, cfg) — extracted
  from package main, used by build/codegen/revise/serve (serve's local loader
  discards the registry today; that dies). The schema endpoint calls the SAME
  canonical envelope function as generate json (3.3): canonical model +
  revision + an FKGraph projection (a deterministic (schema,name)-keyed
  derived-view serializer built once in model per 0.3 — a reconstructable
  convenience view, not a second truth). Fix the nil-registry bug (serve
  currently drops state-machine diagrams). DB-only endpoints degrade with
  explicit unavailable-in-this-mode errors.
- **Why:** The seam made real: today even diagram endpoints demand production
  credentials. The endpoint is the compiler's half of the product boundary —
  and it is literally the same function as the json output, so it can never
  drift from it.
- **Verify:** serve starts with no database and answers the model endpoint
  (byte-consistent with generate json); SM diagrams render; DB-only endpoints
  degrade explicitly.

### 8.2 API hygiene
- **What:** The registered-but-ignored --timeout becomes request-context
  enforcement; the synchronous TANE audit endpoint becomes cancellable and
  non-blocking (job-start/poll); the doc format gets an endpoint.
- **Why:** A timeout flag that does nothing is a lie in the CLI surface; an
  unbounded synchronous FD-discovery endpoint is a self-DoS button; if the API
  is a designed boundary, operational behavior is part of the design.
- **Verify:** Slow-audit test observes timeout/cancel; doc endpoint matches
  generate's doc output.

## Phase 9 — Visualization

### 9.1 Options plumbing (split dependency)
- **What:** D2 options struct threaded from config (depends only on phase 0);
  serve query-param plumbing for the same options (depends on phase 8).
  RenderSVG parameterized: layout (dagre/elk — TALA excluded, not in the OSS
  library), theme, direction.
- **Why:** Every enrichment needs a config-to-generator path; the serve half is
  honestly sequenced (diagram endpoints are DB-coupled until phase 8).
- **Verify:** Config round-trip; elk golden; serve params exercised post-8.

### 9.2 Enrichment
- **What:** Conditional-generation layers (D2's native layers are separate
  pages, not toggles): index/unique markers, nullable indicator in the type
  column, comments as tooltips, checks as notes, RLS/append-only markers,
  enums as plain rectangles with value lists. Imported reference shapes (7.4)
  preserved as a first-class shape class.
- **Why:** Diagrams omit most of what the doc format knows; layers are opt-out
  because show-everything is unreadable and hide-silently is wrong.
- **Verify:** Golden per layer; independently disableable; all goldens compile
  through the D2 library; reference shapes survive every layer combination.

### 9.3 Filtering
- **What:** Include/exclude globs; include-dependencies depth via the 0.3
  depth-bounded walker; summary mode (rectangles, names + edges); edges to
  excluded tables skipped; self-referential FKs preserved; filtered schemas
  canonical via 0.2's filter integration.
- **Why:** Large schemas need subset views; building on the finalized
  graph/filter machinery instead of parallel logic.
- **Verify:** Goldens per mode; filtered output always compiles; depth
  semantics match the walker.

### 9.4 Cardinality
- **What:** Edge block syntax with native crow's-foot arrowheads; 1:1 via
  unique/PK detection on FK columns, 1:N default, M:N via the strict junction
  heuristic (exactly two FKs constituting the whole PK, no other columns).
- **Why:** Cardinality is the most-sought ERD information and currently
  absent; the strict rule avoids false M:N collapses — conservative for
  diagrams people trust.
- **Verify:** Golden per class; junction-with-extra-column NOT collapsed.

### 9.5 Heat maps and live stats
- **What:** Fan-in/fan-out from FKGraph on a fixed colorblind-safe stroke-based
  scale; live row-count/ratio annotations accepted as caller-provided data
  (serve/CLI fetch); generate stays DB-free.
- **Why:** Hubs are where cascade risk concentrates and FKGraph knows them;
  caller-provided stats keep the purity boundary intact.
- **Verify:** Heat map golden; injected-stats test; no DB import in generate.

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract. Unplanned by design; the seam
guarantees phases 8-9 need no rework when it wakes.

---

## Dependency DAG

- 0 internal order: 0.1 -> 0.2 (co-edited generator files) -> {0.3, 0.4};
  0.5/0.6/0.7 independent after 0.2.
- 0 -> {1, 2, 3, 9.1-config-half}; 0.1 -> 4.1 (parallelizable from there);
  {0.1, 3.2, 3.3} -> 4.2; 4.0 precedes 4.1's verify; 4.1+4.2 -> 4.3;
  4.2 -> 6.2.
- 0.4 -> {3.1, 5.1, 7.2}; 0.7 -> {5.2, 5.7, 5.8}; 3 -> {5, 7, 8};
  {3, 0.3} -> 7.3; 2 -> {6.1, 7.4} (new DB entrypoints bind the connection
  env from birth; enforced at registration by 2.1).
- 5 internal: 5.0 (design) -> 5.1 -> 5.2 -> 5.3 -> 5.4 -> 5.5 -> 5.6 -> 5.7 ->
  5.8 -> 5.9 -> 5.10. {5, 0.6, 3, 4.2} -> 6; 5.7 -> 7.4; 7.4 -> 9.2 (reference
  shapes precede enrichment layers); 8 -> 9.1-serve-half.
- Parallelizable after phase 3: 4.1 (already after 0.1), 5, 7 (through 7.2), 8.

## Relationship to existing todos

- infra-env-db-locator.md — superseded by phase 2.
- migrate-add-column-missing-if-not-exists.md — superseded by phase 5.
- genericize-diff-library.md — resolved by phase 1.1.
- partition-lifecycle-and-diff-library.md — Part 1 = 1.2/1.3; Part 2 = 1.1's
  trigger decision.
- cross-framework-schema-composition.md — core = phase 7.
- orxtra-codegen-deferred-remaining.md — item 17 via phase 4 + DB CHECK; item
  18 = phases 3/6; item 20 = phase 6; item 19 dropped; items 21/22 out of
  scope.
- visualization-and-web-ui.md — its phases 1-5 = phase 9; web UI = phases
  8/10.
- rename-to-strictpg.md — in todo/.obsolete/ per the no-rename decision.

## Out of scope, pending their own design rounds

Test schema mode. N-project topology. Manifest + per-language linter ecosystem
(evidence-gated). Recorded summit end-states: declarative catalog
reconciliation for migrate; structural semantics/metadata split in the model;
registry materialization into Schema as sole type-truth; extension-DDL-name
resolution baked into the model; DB/boot-time revision binding.

## Effort

Phase 0: 2-3 sessions (grew: primitive + normalization hoisted in). Phases
1-2: 1-2 each. Phase 3: 1 (thinned: primitive moved to 0). Phase 4: 3-4
(incl. CI toolchain provisioning). Phase 5: 5-7 (largest). Phase 6: 1-2.
Phase 7: 3-4. Phase 8: 1. Phase 9: 2-3. Parallelization per the DAG.

Release: exactly ONE rlsbl release at the very end (global release-once rule);
everything accumulates unreleased; consumer todos filed at that release. No
intermediate state can reach a consumer.
