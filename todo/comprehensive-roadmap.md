# Comprehensive roadmap: determinism, identity, migrate integrity, orchestration, imports, API, visualization

Single consolidated plan from the 2026-07-19 design session. Supersedes/absorbs several
active todos (see "Relationship to existing todos" at the end).

## Decision provenance

Per the %% convention: decisions marked `[%%]` were trust-adopted (user accepted the
recommended option) — they are weakly held, freely reversible, and must never be cited
as the user's deliberate intent. Decisions marked `[deliberate]` were the user's own.

- `[deliberate]` **No rename.** The project stays `pgdesign`. The rename-to-strictpg todo
  is obsolete by this decision. All DB-identifier/repo/package rebranding work is dead.
- `[%%]` Compiler/live-layer seam adopted as internal architecture: `build` and the core
  pipeline stay pure (TOML -> artifacts, no DB); live-DB functionality (serve, stats,
  migrate apply, checks needing a DB) is a distinct tier behind a designed boundary.
- `[%%]` Ambition: summit-grade design only where outputs are permanent (schema identity,
  migration file format); pragmatic rungs elsewhere with the maximal designs recorded as
  end-states, upgraded only on evidence.
- `[%%]` Canonical ordering lives in the IR (Build() sorts; emitters forbidden to sort;
  Sorted* helpers deleted).
- `[%%]` Canonical serialization is semantic-only (non-semantic metadata like
  semtype Source never appears in it); full-model scope (comments included).
- `[%%]` Revision hash = SHA-256 of canonical bytes; every-command enforcement (bare
  build/codegen/migrate-generate refuse revision-inconsistent writes).
- `[%%]` Migration precondition drift = hard error, always. No tolerance flags.
- `[%%]` generate --idempotent unifies with migrate verification via a predicate IR with
  two backends (Go executor + SQL renderer) and a CI conformance matrix.
- `[%%]` Migration history is append-only: files never rewritten; squash emits a NEW
  consolidation migration with recorded lineage; superseded files archived; checksum
  verification unconditional.
- `[%%]` Migration ops are self-contained: serializable fields ARE the rendering inputs;
  non-serialized convenience pointer fields removed; degraded ops unrepresentable.
- `[%%]` Migrations identified by revision pairs (from_revision -> to_revision) forming a
  parent-linked chain; filenames cosmetic (sequence + slug); no version bumps.
- `[%%]` Orchestrator: new `pgdesign revise` command = pure tier, then migration
  generation + DB-dependent checks; seed stays separate; pure outputs kept on partial
  failure; separate commits (pure outputs vs migration); commit failure = hard error.
- `[%%]` Imports: split slices (Tables / ImportedTables, fail-closed); alias reference
  syntax (`alias:table`); vendored import-surface snapshots with per-object hashes +
  source pin (semantic column-level drift errors); type/enum collision = hard error;
  app-only DDL with live verification in migrate generate; imported extension/pg_version
  requirements must be re-declared locally.
- `[%%]` Codegen branding, default-on breaking release: Python = guarded StrEnum
  (implicit construction closed, parse() only dynamic entry) + enum-typed API surfaces;
  Go = opaque struct with complete boundary (constants, erroring Parse, rejecting
  Unmarshal/Scan, Valuer/Marshal/Stringer, detectably-invalid zero value); TS = branded
  type + parse, transition maps off raw string keys; Java/Kotlin = value-based parse
  added (already closed); Zig = wrapper struct + parse. Constants mode unchanged.
  Import-time registry idea dropped (DB CHECK + branding suffice).
- `[%%]` Seed with FKs into imported tables: tiered real keys — live key pool when DB
  available (deterministic sorted pool, plain literals, works in COPY+INSERT, keeps
  distributions); offline = ordered-offset subqueries in INSERT mode; hard error only
  for offline+COPY+NOT-NULL-imported-FK, naming all three constraints.
- `[%%]` strictcli: new "connection env" kind (hermetic-suppressed, lazy, no default);
  PGDESIGN_DB declared once; all ~15 --db flags bind it with visible provenance;
  DB-dependent checks skip visibly under --hermetic.
- `[%%]` Partition: premake required (no default); opt-in `schedule` key emits pg_cron
  job via the existing dead helper; missing schedule without acknowledgment = warning.
- `[%%]` pkg/diff deleted now; promotion trigger recorded: public differ only when a
  second flat-schema consumer actually exists.
- `[%%]` Web UI frontend deferred; only the DB-free API contract is built now.

## Grounding facts that shaped the plan (verified in source, 2026-07-19)

- Raw model.Schema is NON-deterministic: resolveTable builds FKs/Indexes/Uniques/Checks/
  Exclusions/Policies/Triggers by ranging Go maps (build.go ~563-644). SQL/JSON/doc/d2/
  graphql/python-ddl emitters re-sort via model.Sorted* helpers, so their output is
  stable; matview indexes are unsorted on EVERY path (live bug); gorm/drizzle/jpa/
  sqlalchemy codegen and validator policy extraction emit in raw map order (stable only
  while tables have <=1 of each item). The existing determinism test cannot catch this.
- Two divergent JSON serializers: generate's json format sorts; serve's /api/schema
  emits the raw model. FKGraph and TablesByName are json:"-".
- semtype.Registry is separate from Schema and entirely unexported/unserializable;
  scalar CHECK semantics and builtin shadowing live only there. TypeDef.Source is
  documented as metadata not compared by equality.
- Generated-file headers: ~41 inline emission sites + 6 validator-only helpers + 5 CLI
  sites, two inconsistent wordings. codegen --check is byte-exact (pkg/genkit).
- Migrate: tracking table = version/applied_at/checksum/description; checksum is over
  migration FILE bytes and is NEVER verified on re-apply; no per-op records; version row
  written LAST (partial phases/non-transactional ops commit real DDL with no durable
  record; re-apply restarts at op 0). Rollback re-reads the file, trusting it over the
  DB. Ops have no stable identity; some ops depend on non-serialized pointer fields and
  render an EMPTY CREATE TABLE when parsed from disk (degraded-op trap).
- Squash deletes/rewrites original files (saferm + rename over <to>.toml); the
  applied-version guard (M200) runs ONLY if --db is voluntarily passed; tracking rows
  for squashed versions are orphaned; no tests cover the CLI flow.
- migrate generate requires --db and --version (flag-only); no ledger/manifest of
  migrations exists; migrations dir sentinel bug (explicit `--dir migrations`
  indistinguishable from default).
- Introspect does NOT filter pgdesign_migrations (reconcile would false-positive);
  deny-mutation function and _pgdesign_sm_ prefix ARE filtered.
- serve is hard DB-coupled at construction (pool.Ping); --timeout flag registered but
  never enforced; audit endpoint runs TANE synchronously; no DB-free schema endpoint
  exists anywhere; project-loading helpers live in package main.
- Duplicated multi-file write/orphan logic between handlers_codegen.go and
  build_plan.go.
- strictcli: CheckContext exposes only ProjectRoot(); the check command constructs a
  fully-populated *Context and discards it; infra roots and handshake envs are
  hermetic-IMMUNE while flag Env() is hermetic-SUPPRESSED — no primitive fits a
  connection URL; ~15 --db flags ignore PGDESIGN_DB while checks read it raw.
- Codegen enum shapes today: Go `type X string` (fully open), TS string-literal union
  (structural), Python StrEnum (str subclass, value-construction open), Java/Kotlin real
  enums (already closed), Zig bare string consts. No parse helpers generated anywhere.
  TS type-safe transition maps use raw string keys.
- CI runs postgres:17 + pg_partman with PGDESIGN_REQUIRE_DB=1; ~11 DB-backed migrate
  tests exist vs ~150 unit tests.
- Partition bugs: python_ddl.go still passes Retention as p_interval (generate path was
  fixed in v0.24.4); omitted premake becomes p_premake := 0 (breaks partman); silent
  skip when pg_partman undeclared; manual children + maintenance emit contradictory DDL;
  PartmanRunMaintenanceCron() is dead but tested code.
- pkg/diff has zero importers; internal/diff's matcher is generic but result types embed
  ~22 PG model types consumed field-by-field (~350 typed accesses) by migrate.

---

## Phase 0 — Foundational groundwork

Everything later stamps, hashes, compares, or filters. None of that is trustworthy while
the substrate lies. Phase 0 makes the substrate honest so every later phase inherits
honesty instead of re-implementing it.

### 0.1 Canonical ordering in the IR
- **What:** Sort every map-derived collection at Build() construction using the same
  comparators the emitters use today (near-zero churn — main emitters already sort).
  Delete Sorted* helpers and ALL emitter-side sorting. Add a Build postcondition and a
  CI build-twice-compare-bytes determinism test. Fix in the same stroke: matview index
  ordering (nondeterministic everywhere), the four luck-stable ORM codegen generators +
  validator policy extraction, and replace the too-weak determinism test.
- **Why:** The revision hash is a hash of bytes; if the same schema can yield different
  bytes, identity is meaningless, freshness flaps, diffs lie. Today determinism is an
  accident of which emitter you use. Ordering as an IR property makes every current and
  future emitter deterministic BY DEFAULT — "forgot to sort" (a bug class we just found
  four live instances of) becomes impossible rather than guarded.
- **Verify:** Determinism test red before / green after; goldens byte-stable; fixture
  with 2 matview indexes + multiple FKs per table stable across runs; grep finds no
  emitter-side sorting.

### 0.2 Schema-qualified identity keying
- **What:** Rekey FKGraph (Forward/Reverse/FanIn/FanOut), cascade walkers, and group
  resolution from bare table names to (schema, name), matching TableByName/topo-sort.
- **Why:** Two identity schemes for one object is a latent bug today (same-named tables
  in two PG schemas collide in cascade analysis) and a guaranteed bug once imports land
  (imported tables live in foreign schemas by definition). Fix identity before building
  features on the graph so the features never inherit the ambiguity.
- **Verify:** Red-green test with same-named tables in two schemas through cascade
  depth + group filtering; suite green.

### 0.3 One header function
- **What:** Single shared header emitter (language -> comment prefix; one wording —
  Go's machine-readable "Code generated ... DO NOT EDIT." convention; optional
  regenerate-command line). Replace ~41 inline sites, 6 validator helpers, 5 CLI sites.
- **Why:** Phase 4 stamps a revision into every generated header; phase 6 enforcement
  reads those stamps. Stamping through 41 scattered literals means 41 chances to miss
  one, and a missed stamp is an artifact invisible to divergence enforcement. One choke
  point makes "every generated file carries the stamp" true by construction. The Go
  wording makes generated Go files recognizable to standard tooling for free.
- **Verify:** Grep: zero header literals outside the helper; DDL golden regenerated;
  header-asserting tests pass.

### 0.4 Type-registry snapshot
- **What:** Deterministic, ordered, exported snapshot accessor on semtype.Registry plus
  reconstruct-from-snapshot; no semantic changes to the registry.
- **Why:** The registry holds semantic state that exists nowhere else (scalar CHECKs,
  builtin shadowing) and is unserializable today — an identity omitting it would call
  two different schemas "the same." The snapshot lets the canonical form include the
  whole type system, and later lets imports carry type definitions across projects.
- **Verify:** Snapshot -> reconstruct -> snapshot byte-stable; output independent of
  registration order.

### 0.5 Introspect filters managed objects
- **What:** Consistent exclusion of pgdesign's own artifacts in introspection: the
  tracking table (currently reported as a user table), joining the already-filtered
  deny-mutation function and SM trigger prefix.
- **Why:** Reconcile-verify and the shadow test work by "introspect reality, diff
  against intent, demand emptiness." That contract is unusable if the tool's own
  bookkeeping registers as drift on every migrated database — a check that always warns
  is a check everyone ignores. Tool artifacts are infrastructure, not schema, and
  introspection is the single right place to know it.
- **Verify:** DB-backed test: introspect a migrated DB, diff against desired, empty.

### 0.6 One write path; sentinel fix
- **What:** Consolidate duplicated multi-file write + owned-dir/orphan bookkeeping
  (standalone codegen vs build planner) onto the planner; standalone codegen becomes a
  thin caller. Fix the migrations-dir sentinel (explicit `--dir migrations` currently
  indistinguishable from default).
- **Why:** Phase 6 enforcement must guard EVERY write; two divergent write paths means
  two guards that drift (the same disease as the two JSON serializers). One path, one
  guard, everywhere by construction. The sentinel fix matters because revise must know
  what the user actually asked for; a flag meaning two things poisons logic built on it.
- **Verify:** Standalone codegen and build byte-identical on a fixture, identical
  orphan behavior; sentinel red-green test.

## Phase 1 — Ground-clearing

Kill known bugs and lies before building. Nothing here depends on the new architecture;
each removal is one less thing later phases interact with.

### 1.1 Delete pkg/diff
- **What:** Remove the stub package; changelog records the promotion trigger (public
  differ only when a second flat-schema consumer exists).
- **Why:** An exported API unusable without internal imports is worse than none — the
  one external prospect designed against the promise, hit the gap, built natively. The
  stub costs trust; deletion costs nothing (zero importers). The trigger keeps the door
  honest instead of closed.
- **Verify:** Package gone; build + vet clean.

### 1.2 Partition bug fixes (red-green each)
- **What:** Fix python_ddl.go interval/retention conflation (sibling of the v0.24.4
  generate fix). premake required — omission is a hard parse error (silent zero
  disables partman today). Hard errors: non-RANGE strategy with maintenance;
  [maintenance] without pg_partman declared (today silently skipped); maintenance plus
  manual partition children (today emits contradictory DDL). Silent part_config query
  failure becomes a diagnostic.
- **Why:** Every item is the silent-degradation class the house rules exist to kill:
  configs that look accepted but produce broken DDL discovered in production
  partitioning — the worst place. Loud at compile time is the entire value of a schema
  COMPILER. The interval bug fixed in one emitter and missed in its sibling is itself
  the argument for consolidation thinking (0.6).
- **Verify:** Failing test first per bug, then fix; CI has postgres+pg_partman for
  live coverage.

### 1.3 Partition lifecycle completion
- **What:** Introspection reads interval/premake/retention from part_config into the
  model. Diff distinguishes: initial partman setup (emit create_parent) vs
  retention/premake update (Safe, risk-classified UPDATE part_config ops) vs interval
  change (hard error + repartitioning guidance). Migrate guards on extension presence.
  New `schedule` key emits the pg_cron job via the existing dead-but-tested helper
  (requires pg_cron declared — hard error otherwise); no schedule and no external-
  scheduler acknowledgment = validation warning.
- **Why:** Partitioned tables are where the schema is ALIVE — partman creates children
  at runtime. A tool that creates partman config but cannot see it (introspection
  blind), evolve it (no diff/migrate), or ensure it runs (nothing schedules
  run_maintenance) automated the setup and abandoned the lifecycle. This closes the
  loop: "monthly partitions, keep six months, maintained every 30 min" becomes
  expressible, diffable, migratable, verifiable. Dead helper wired up per dead-code
  policy: it makes the system more correct.
- **Verify:** Golden DDL for schedule emission; diff/migrate tests per transition
  class; live introspect round-trip in CI.

### 1.4 Squash safety stopgap
- **What:** Until phase 5 replaces squash: the applied-version guard becomes mandatory
  (--db required, M200 check always runs).
- **Why:** Squash today deletes/rewrites files whose checksums a production tracking
  table records, with the DB check OPT-IN — a guardrail with an escape hatch, hatch
  being the default. Phase 5 rebuilds squash; this closes the hole for the interim,
  because "fixed later" is not protection.
- **Verify:** Squash without --db hard-errors; squash overlapping applied versions
  refuses.

## Phase 2 — Connection environment

The DB URL is pgdesign's most external dependency and currently flows through the least
principled channel (raw getenv in one place, 15 required flags elsewhere, invisible to
hermetic mode).

### 2.1 strictcli: connection-env kind
- **What:** Third env primitive — hermetic-SUPPRESSED, lazily read, no implicit
  default — alongside infra roots and handshake envs (both hermetic-immune). Check
  framework gains access to declared env values (the check command already builds a
  fully-populated Context and discards it; stop discarding it). Released as a strictcli
  version.
- **Why:** A genuine semantic hole: a connection URL is precisely what --hermetic
  should suppress (behavioral reach outside the process), yet both existing primitives
  survive hermetic and flag Env() is unavailable to checks. Without the third kind,
  pgdesign must choose between an undeclared raw getenv (invisible, hermetic-blind —
  status quo) or declaring the DB as "infrastructure" that hermetic cannot turn off —
  both wrong. The framework-level fix gives every strictcli consumer principled
  connection semantics, not just pgdesign.
- **Verify:** strictcli tests: declaration, lazy read, hermetic suppression,
  check-side access; schema dump includes the new kind.

### 2.2 pgdesign adoption
- **What:** Declare PGDESIGN_DB once as a connection env. Bind all ~15 --db flags to it
  with provenance (cli/env/config) reported. Checks read via the framework instead of
  raw os.Getenv. Under --hermetic, DB-dependent checks skip with a visible outcome.
- **Why:** One variable should have one story. Today checks honor the env var while
  commands ignore it — same input, two behaviors, and the raw getenv is invisible to
  --help and the schema dump. Binding everything through one declared primitive makes
  the connection story consistent, discoverable, and hermetic-correct, with provenance
  replacing forced retyping as the explicitness mechanism.
- **Verify:** Env-only invocation works on every DB command with a provenance line;
  hermetic check run shows explicit skips; raw os.Getenv gone from cmd/ (test harness
  excepted).

## Phase 3 — Schema identity

The summit-grade foundation (permanence test: this format gets stamped into migration
files, generated headers, tracking tables, import snapshots — v1 haunts forever, so it
is designed once, correctly).

### 3.1 Canonical serialization
- **What:** One serializer for the fully-resolved model: schema fields + the registry
  snapshot (0.4), SEMANTIC-ONLY (non-semantic metadata like TypeDef.Source never
  appears), excluding derived caches (FKGraph, name indexes, candidate-key cache), with
  a format version field. Deterministic by 0.1.
- **Why:** This artifact is the single canonical answer to "what IS this schema?" —
  the substrate for the revision hash, the import snapshot format, and the DB-free API
  payload. Three consumers, one format: designing them together prevents the divergence
  disease already visible in the two existing JSON serializers. Semantic-only scope
  puts the include/exclude decision in ONE format definition instead of scattered
  hasher logic, and produces the same bytes the future structural-split refactor would
  — so that refactor stays optional, format-compatible, evidence-gated.
- **Verify:** Byte-identical across repeated builds and struct-field-order refactors;
  golden fixture committed; comment edit changes bytes (full-model scope); Source
  relabeling does NOT change bytes (semantic-only).

### 3.2 Revision hash
- **What:** SHA-256 over the canonical bytes; exposed from model; surfaced in CLI
  output (validate/build print it).
- **Why:** The revision is the coupling primitive of the entire roadmap: the thing
  migration files, generated headers, the tracking table, and enforcement all agree on.
  Everything that must not diverge diverges today because nothing shared exists to
  compare; this is the shared thing.
- **Verify:** Sensitivity tests: comment change, column change, registry change each
  flip the hash; no-op rebuild does not.

### 3.3 One serializer everywhere
- **What:** Generate's json format and serve's schema responses emit the canonical form
  (plus endpoint wrappers); the divergent serializers die.
- **Why:** Two serializers for one struct is how the nondeterminism bug survived
  unnoticed — each path had a different truth. Any consumer (UI, import snapshot, jq
  script) must see THE schema, not "the schema according to this endpoint."
- **Verify:** generate json and serve bodies structurally identical for the same
  schema; golden updated once.

## Phase 4 — Codegen breaking release

One coordinated consumer-facing break carrying both artifact-format changes, so
consumers regenerate and adapt exactly once.

### 4.1 Branded types per language
- **What:** Go: opaque struct (unexported value field) with COMPLETE generated
  boundary — constants as sole constructors, Parse erroring on unknowns,
  UnmarshalJSON/UnmarshalText/sql.Scanner rejecting unknown values with errors naming
  the enum, Valuer/MarshalJSON/Stringer for output, zero value detectably invalid.
  Python: StrEnum retained (str-compatibility preserved) but implicit construction
  closed — value-lookup raises; parse() classmethod is the only dynamic entry; query-
  layer and validator signatures move to enum types. TS: branded string type + parse;
  type-safe transition maps switch off raw string keys. Java/Kotlin: value-based parse
  helper added (construction already closed). Zig: wrapper struct + parse over the
  string constants. Constants mode (name strings) intentionally unchanged.
- **Why:** The motivating drift class — consumer code naming a state the schema does
  not define, crashing at runtime when PG rejects it — dies when invalid values cannot
  be NAMED or SMUGGLED: compile error where the language can express it, boundary error
  (JSON/DB/string ingress) everywhere else, DB CHECK as final backstop. The chosen
  mechanisms deliberately refuse the ergonomics-for-safety trade: guarded StrEnum keeps
  the str substrate (no .value boilerplate — the plain-Enum alternative would have
  GROWN the stringly surface); the Go struct's friction dissolves because codegen
  generates the complete interface surface consumers currently get for free. Principle:
  in generated code, never buy safety with ergonomics — generate the missing surface.
- **Verify:** Per language, constructing an invalid value fails at the earliest
  possible boundary (compile where expressible, error return otherwise); Go fixture
  proves all four ingresses reject; Python/TS fixtures pass their type-checkers where
  toolchains exist in CI.

### 4.2 Revision-stamped headers
- **What:** The shared header (0.3) gains the revision line. Deterministic hash, so
  byte-exact freshness (codegen --check, check --tag build) stays stable across no-op
  rebuilds.
- **Why:** The stamp is what makes generated code SAY which schema it came from — the
  raw material for phase 6's "SQL and code cannot silently diverge" guarantee and for
  consumers auditing what they deployed. Determinism is the precondition (0.1/3.1):
  a volatile stamp would make every freshness check permanently red.
- **Verify:** Rebuild-without-change keeps freshness green; a schema edit flips every
  output stale exactly once.

### 4.3 One coordinated breaking release
- **What:** Both changes in a single version bump; changelog entries typed breaking;
  regeneration notes for consumers (known consumer modes: python ddl faceted; python
  validators+constants; zig constants; generated SQL headers).
- **Why:** Header stamping and branding each force a full consumer regeneration; done
  separately they force it twice. Consumers regenerate on version bumps anyway (per the
  consumer-regeneration convention); one break, one adaptation.
- **Verify:** rlsbl changelog coverage passes with breaking entries; consumer
  drift-check scripts pass after regeneration.

## Phase 5 — Migrate integrity

The apply pipeline becomes trustworthy: today it applies SQL blindly (no preconditions),
records nothing until the end (partial state unrecorded, retries abort forever), trusts
files over the database (rollback), tolerates definition drift silently (guards), and
lets squash destroy applied history.

### 5.1 Self-contained ops + chain file format + unconditional checksums
- **What:** Ops become self-contained: serializable fields ARE the rendering inputs;
  non-serialized convenience pointer fields removed; the generator builds ops from
  serializable data only; empty-render fallbacks deleted; write-time parse-and-
  re-render round-trip remains as an invariant test. New file format: sequence+slug
  filenames (cosmetic), from_revision/to_revision pair, parent linkage forming the
  chain. Checksum verification becomes unconditional on apply AND rollback (any
  mismatch = corruption, hard error). Existing semver-named files grandfathered as a
  linear chain prefix.
- **Why:** Three discovered traps die here. (a) The degraded-op trap — ops that render
  an EMPTY CREATE TABLE when parsed from disk — becomes unrepresentable instead of
  guarded: OpToSQL becomes a total function of the on-disk form. (b) Identity: today a
  migration's only identity is a filename and its only integrity a checksum that is
  never checked; the revision pair gives migrations real identity tied to the schema
  they transform, making "which migrations are pending" a chain traversal instead of
  filename arithmetic. (c) Unconditional checksums are only fair once files are
  append-only (5.7) — enforcing against a format where legitimate rewriting exists
  would brick databases, which is why format, chain, and enforcement land together.
- **Verify:** Round-trip tests; tamper test (edited applied file: apply and rollback
  both refuse); degraded-op fixture errors at parse instead of emitting empty DDL;
  grandfathered history traverses correctly.

### 5.2 Applied-op journal
- **What:** Journal table beside pgdesign_migrations recording each op as it commits —
  including per-phase commits and non-transactional breakouts. Re-apply resumes by
  skipping journaled ops.
- **Why:** The version row is written LAST; every phase commit and every
  CONCURRENTLY/ADD VALUE breakout before a failure leaves real committed DDL with NO
  durable record, and re-apply restarts at op 0 and aborts on duplicates — forever.
  That abort-loop is the original bug report behind this whole phase. The journal makes
  applied reality durable at the moment it becomes real, which is what turns retries
  from a gamble into a resume.
- **Verify:** DB-backed fault injection: fail mid-phase and after a non-transactional
  op; re-apply resumes cleanly, no duplicate-object errors, completes.

### 5.3 Precondition checks
- **What:** Before each op, a Go-side predicate against pg_catalog asserts expected
  prior state per op class (absent for creates; present-and-matching for alters/drops).
  Any unexpected state = hard error naming object, expected, found. No tolerance flags.
- **Why:** Blind DDL is how drift gets tolerated or amplified: IF NOT EXISTS-style
  guards silently accept a column with the WRONG definition; bare DDL turns benign
  retries into crashes. Preconditions make apply a checked state transition — the
  database must be where the migration thinks it is, or a human decides. This is the
  no-silent-degradation rule applied to the one place it matters most: other people's
  production data.
- **Verify:** DB-backed matrix seeding conflicting state per op class (wrong-type
  column, missing table, mismatched constraint) — each aborts with the precise report.

### 5.4 Journal-driven rollback
- **What:** Rollback inverts only ops the journal says ran, in journal order; the 5.1
  checksum check guards the file; reversibility pre-check retained.
- **Why:** Rollback today re-reads the file and trusts it absolutely — it will happily
  invert ops that never ran. The catastrophic case: an ADD COLUMN that no-opped
  (column pre-existed) whose rollback DROPs a column the migration never created,
  destroying data. Rolling back recorded reality instead of assumed intent makes that
  class impossible.
- **Verify:** Rollback after partial apply drops nothing it did not create (the
  no-op-ADD/DROP-COLUMN scenario tested and impossible).

### 5.5 Post-apply reconcile-verify
- **What:** After apply: introspect (with 0.5 exclusions) + diff against the target
  model; any residual mismatch = hard error listing every divergent object. SM-vs-enum
  introspection lossiness documented as a known comparison boundary.
- **Why:** Preconditions check each op locally; reconcile checks the COMBINED result
  globally — catching what per-op checks structurally cannot (out-of-band changes
  mid-migration, op interactions, generator bugs). It reuses the real differ, so
  coverage is complete across all object types with zero bespoke verification code.
  Preconditions + journal + reconcile together approximate the recorded end-state
  (declarative catalog reconciliation) without the rewrite.
- **Verify:** DB-backed test: out-of-band ALTER between ops surfaces in the final
  report; clean apply reports empty.

### 5.6 Predicate IR + conformance suite
- **What:** Preconditions defined once as structured data (catalog query + expected
  shape). Two backends: the Go executor (5.3) and a SQL renderer compiling the same
  structure into DO-blocks for generate --idempotent (which thereby stops silently
  skipping mismatched objects and RAISEs instead). CI conformance matrix runs both
  backends against the same live database states (match / missing / each mismatch
  class) asserting identical verdicts.
- **Why:** The sibling-path rule: generate --idempotent has the SAME silent-mismatch
  hole migrate is being cured of; fixing one and not the other leaves the bug class
  alive. The naive fix (hand-written definition-checking PL/pgSQL) creates a second
  source of truth that drifts from the Go comparison logic. The predicate IR dissolves
  that objection structurally — one definition, two compilations — and the conformance
  matrix makes non-drift a TESTED property instead of a review promise: backend
  divergence is a red build, not a latent bug.
- **Verify:** Golden idempotent SQL; DB test: mismatched pre-existing column makes the
  idempotent script fail loudly, matching state no-ops; conformance matrix green and
  wired into CI.

### 5.7 Append-only history: squash replacement + ecosystem alignment
- **What:** Squash reimplemented as a consolidation migration: a NEW migration
  referencing the superseded range with recorded lineage; superseded files retire to an
  archive directory intact; tracking-table lineage handled (no orphaned rows); files
  are never rewritten, period. migrate test --shadow, baseline, and serve migrations
  endpoints updated for the new format/journal; migration-guide docs rewritten.
- **Why:** Squash today deletes applied history with an opt-in guard and orphans
  tracking rows — mutation of applied artifacts is an OPERATION the tool offers. Making
  history append-only deletes the operation, not just the bug: "file changed after
  apply" stops being a state anyone must detect because it stops being expressible.
  It is also what makes 5.1's unconditional checksum enforcement fair, and the recorded
  lineage is what lets rollback traverse across a squash boundary correctly.
- **Verify:** Squash of applied migrations succeeds safely via consolidation (today it
  refuses or corrupts); archive intact; no orphaned tracking rows; full migrate suite
  green including expanded DB-backed set.

## Phase 6 — Orchestration and enforcement

### 6.1 pgdesign revise
- **What:** New top-level command: pure tier first (reuses the build planner
  unchanged), then migration generation, then DB-dependent checks (nf, workload).
  Migration identity derived from the revision chain — parent = current chain head,
  target = current model revision; sequence+slug filename generated; hard error on a
  diverged chain (two heads), both named. Separate safegit commits: pure outputs, then
  the migration. Partial failure keeps committed pure outputs and exits loudly stating
  exactly what did not happen. Commit failure = hard error (and build's current
  warn-and-continue on commit failure gets the same fix).
- **Why:** The forgotten-step failure mode is real: a schema change today takes four
  commands, and skipping one ships stale artifacts or an unmigrated database. revise is
  the single answer to "I edited the TOML — make everything consistent and tell me if
  anything is wrong," WITHOUT eroding build's purity (the compiler/live seam: pure tier
  runs anywhere, DB tier is explicit). Chain-derived identity eliminates the invented
  --version/--bump ceremony — the tool stops asking questions it can answer. Separate
  commits reflect the two artifact lifecycles (regenerable snapshots vs append-only
  ledger). Keeping pure outputs on DB failure is safe BECAUSE revision stamps make the
  incomplete state detectable everywhere (6.2) — nothing can silently ship.
- **Verify:** End-to-end fixture: edit TOML -> revise -> regenerated outputs + correctly
  chained migration, two commits, all stamped with one revision. DB-unreachable run
  keeps pure outputs, exits non-zero, names the skipped tier. Diverged-chain fixture
  errors with both heads.

### 6.2 Every-command revision enforcement
- **What:** Bare build / codegen / migrate-generate compare on-disk artifact stamps
  against the current model revision and REFUSE inconsistent writes. New revision check
  (error severity, sibling of the build-freshness check) as the CI backstop. genkit
  gains stamp verification following its compare-and-report pattern.
- **Why:** revise alone is a convention — any bare command run afterwards could still
  regenerate half the world and desync SQL from code. Enforcement at every entry point
  turns "the artifacts that must change together" from a workflow hope into an
  invariant: divergence is a hard error at the earliest boundary that touches it, no
  matter which command got run. This is the every-command depth chosen over check-only
  (too late) and DB/boot-binding (needs consumer cooperation; recorded as a possible
  future extension).
- **Verify:** Hand-tampered header -> next build hard-errors naming the file; stale
  revision -> migrate generate refuses; CI check red on either.

## Phase 7 — Imports

Cross-project schema composition: an app declares a framework's tables exist in the same
database, FKs into them with full validation, without ever owning them.

### 7.1 Declaration and reference syntax
- **What:** [imports] config parsing (alias -> source + target PG schema). Alias
  reference form (`alias:table`) in FK targets, resolved through the declaration into
  schema-qualified references. New diagnostics: unknown alias, unresolvable target,
  collisions.
- **Why:** References should name the DEPENDENCY, not a physical schema string: the
  alias makes provenance visible at the reference site, target-schema renames touch one
  config line, and a typo'd alias is a hard resolution error instead of a plausible-
  looking phantom schema. Cross-schema FKs written as raw SQL today are invisible to
  validation, migration, and the dependency graph — this brings them inside the
  compiler.
- **Verify:** Parse/build tests; alias typo yields the resolution error, never a
  phantom local reference.

### 7.2 Surface snapshot and pinning
- **What:** Import-surface extraction — only the objects the app actually references —
  serialized via the canonical format (3.1) into committed vendored snapshots with
  per-object hashes plus a source pin (git URL + ref). Lock/update subcommands (names
  need user approval). `check --tag imports` re-derives the surface and reports
  semantic drift at column level, hard-failing CI.
- **Why:** Machine-specific committed paths are banned (house rule) and unpinned
  imports drift silently — a framework column rename breaks the app's FK with no
  warning until DDL hits the database. Content-addressed vendored snapshots make
  builds reproducible and offline-capable; the SEMANTIC diff is what makes pgdesign's
  pinning better than a generic lockfile: "framework column X changed uuid->bigint,
  breaks app.users.principal_id" instead of "hash mismatch." Snapshot format = the
  canonical serialization — one format, no second dialect.
- **Verify:** Two-project fixture: drift the referenced column type -> check names the
  exact column and the breaking FK; unreferenced framework changes stay silent; offline
  build works from the snapshot.

### 7.3 Model integration
- **What:** ImportedTables split slice on Schema; integrity machinery (FK validation,
  topo-sort, FKGraph, seed FK-resolution) explicitly unions owned+imported; everything
  else iterates owned tables untouched. Registry collisions between imported and local
  types = hard error naming both sources. Imported enums usable in columns. Extension /
  PG-version requirements of referenced imports must be re-declared locally — hard
  error naming the requiring object otherwise.
- **Why:** Fail-closed by construction: every existing and future consumer that
  iterates Tables gets correct behavior BY OMISSION — forgetting about imports means
  no leak (no DDL, no audit, no codegen for foreign tables), not a leak. Only the small
  set of integrity consumers opts into the union. Explicit requirement re-declaration
  keeps the app's config an honest statement of its deployment needs instead of
  silently inheriting a higher PG floor or an extension dependency.
- **Verify:** E204 resolves imported targets; DDL/audit/codegen/seed outputs contain
  zero imported-table artifacts on the fixture; collision and re-declaration tests.

### 7.4 Downstream sweep
- **What:** Generate emits app-only DDL with schema-qualified FK constraints.
  Diff/migrate exclude imported tables from add/drop; migrate generate hard-errors when
  referenced imports are missing or mismatched in the live database. Audit, design
  checks, orphan warnings skip imported tables. Codegen skips them (no duplicate
  types). Seed: tiered real keys — with DB access, read real keys into a deterministic
  sorted pool, emit plain literals (COPY+INSERT, distributions kept); offline, ordered-
  offset subqueries in INSERT mode; hard error ONLY for offline+COPY+NOT-NULL-imported-
  FK, naming all three constraints. D2/GraphQL render imported tables as minimal
  reference shapes so edges never dangle.
- **Why:** Ownership discipline end-to-end: the framework's objects are facts the app
  consumes, not fictions it may regenerate, audit, or fabricate rows for. Live
  verification in migrate generate converts the apply-order requirement from
  documentation into enforcement. The seed tiers follow one principle — imported rows
  are facts; seed never invents them — and the error surface is exactly the one true
  impossibility rather than a blanket ban on legitimate cases.
- **Verify:** Per-package fixture assertions; live verification test (present passes;
  absent and mismatched fail with specifics); seed tier tests incl. determinism under
  --seed and the triple-constraint error; D2 golden compiles.

## Phase 8 — Read API

### 8.1 DB-free serve mode
- **What:** Pool becomes optional; --db optional when project-schema mode is active;
  project-loading helpers extracted from package main for reuse; new endpoint returns
  the canonical serialized model with revision and a serialized FKGraph projection.
  DB-only endpoints return a clear unavailable-in-this-mode error.
- **Why:** The seam made real: today even the diagram endpoints introspect a live DB
  first — there is no way to browse a schema without production credentials. The
  canonical model + FKGraph payload is the contract the future interactive layer will
  consume (and the same serialization imports already use), so this endpoint is the
  compiler's half of the product boundary, shippable with zero frontend.
- **Verify:** serve starts with no database and answers the model endpoint; DB-only
  endpoints degrade with explicit errors, not panics.

### 8.2 API hygiene
- **What:** The registered-but-ignored --timeout becomes request-context enforcement.
  The synchronous TANE audit endpoint becomes cancellable and non-blocking (job-start /
  poll). Doc format gets an endpoint.
- **Why:** A timeout flag that does nothing is a silent lie in the CLI surface; an
  unbounded synchronous FD-discovery endpoint is a self-DoS button on large tables. If
  the API is a designed boundary (8.1), its operational behavior — cancellation,
  bounded latency, documented outputs — is part of the design, not an afterthought.
- **Verify:** Slow-audit test observes timeout/cancel; doc endpoint output matches
  generate's doc format.

## Phase 9 — Visualization

Depends only on phase 0; scheduled late by priority, can be pulled forward any time.

### 9.1 Options plumbing
- **What:** D2-specific options struct threaded from config and serve query params;
  SVG rendering parameterized: layout (dagre/elk — TALA excluded, not in the OSS
  library), theme, direction.
- **Why:** Every enrichment below needs a path from config to generator; today
  GenerateD2 takes no options and RenderSVG hardcodes dagre with no theme. Plumbing
  first so each later feature is config, not surgery.
- **Verify:** Config round-trip test; elk-rendered golden exists.

### 9.2 Enrichment
- **What:** Conditional-generation layers (D2's native layers are separate pages, not
  toggles): index/unique markers, nullable indicator in the type column, comments as
  tooltips, checks as notes, RLS/append-only markers, enums as plain rectangles with
  value lists.
- **Why:** The diagrams currently omit most of what the doc format knows — users
  cross-reference three outputs to understand one schema. Layers are opt-out because
  a diagram that shows everything is unreadable and one that hides things silently is
  wrong; conditional generation puts the choice in config.
- **Verify:** Golden D2 per layer; each layer independently disableable; all goldens
  compile through the D2 library in tests.

### 9.3 Filtering
- **What:** Include/exclude globs, include-dependencies depth, summary mode
  (rectangles, names + edges only). Edges to excluded tables skipped; self-referential
  FKs preserved.
- **Why:** Large schemas produce unreadable full diagrams; subset views are the most
  requested ERD affordance. Edge handling must be explicit because the current
  filter helpers keep FK arrays intact — naive filtering emits edges to nonexistent
  shapes and the diagram fails to compile.
- **Verify:** Goldens per mode; filtered output always compiles (no dangling refs).

### 9.4 Cardinality
- **What:** Edge block syntax with D2's native crow's-foot arrowheads (confirmed
  supported); 1:1 via unique/PK detection on FK columns, 1:N default, M:N via the
  strict junction heuristic (exactly two FKs constituting the whole PK, no other
  columns).
- **Why:** Cardinality is the single most-sought piece of information in an ERD and
  the diagrams currently show none. Structural inference costs nothing at runtime and
  is right in the overwhelming case; the strict junction rule avoids false M:N
  collapses at the price of missing decorated junction tables — the conservative
  direction for a diagram that people trust.
- **Verify:** Golden per cardinality class; junction fixture WITH an extra column is
  correctly NOT collapsed.

### 9.5 Heat maps and live stats
- **What:** Fan-in/fan-out from FKGraph mapped to a fixed colorblind-safe stroke-based
  scale. Live row-count/ratio annotations accepted as caller-provided data (serve/CLI
  fetch); generate stays DB-free.
- **Why:** Dependency density is invisible in a uniform diagram; hubs are where
  cascade risk and migration pain concentrate, and the FKGraph already knows them.
  Caller-provided stats keep the purity boundary intact — generate remains a pure
  function even when the picture shows live numbers.
- **Verify:** Heat map golden; live-annotation test with injected stats; no DB import
  appears in the generate package.

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract (client-side graph interactivity from
the serialized model, live drift dashboard, D2 export for fidelity). Unplanned by
design; the seam guarantees phases 8-9 need no rework when it wakes.

---

## Relationship to existing todos

- `infra-env-db-locator.md` — superseded by phase 2 (broader, framework-level fix).
- `migrate-add-column-missing-if-not-exists.md` — superseded by phase 5 (the one-line
  guard fix is replaced by journal + preconditions, which solve the reported abort-loop
  without the silent-mismatch hazard the todo itself flags).
- `genericize-diff-library.md` — resolved by phase 1.1 (delete stub, recorded trigger).
- `partition-lifecycle-and-diff-library.md` — Part 1 = phases 1.2/1.3; Part 2 =
  resolved by 1.1's trigger decision.
- `cross-framework-schema-composition.md` — core feature = phase 7; shared enums =
  7.3; migration coordination beyond live verification = out of scope below.
- `orxtra-codegen-deferred-remaining.md` — item 17 (consumer validation) resolved via
  phase 4 branding + DB CHECK (manifest/linter ecosystem out of scope, evidence-gated);
  item 18 (atomic migration codegen) = phases 3/6; item 20 (full round-trip) = phase 6;
  items 19 (import-time registry) dropped by decision; 21 (test schema mode) and 22
  (multi-repo topology) out of scope below.
- `visualization-and-web-ui.md` — phases 1-5 of that todo = phase 9 here; web UI =
  phases 8/10 here.
- `rename-to-strictpg.md` — obsolete by the [deliberate] no-rename decision.

## Out of scope, pending their own design rounds

Not phases, per planning discipline (a plan may contain zero unresolved design work):

- Test schema mode (extension stubs, relaxed constraints, test fixtures).
- N-project topology beyond the two-project import case.
- Manifest + per-language linter ecosystem (evidence-gated: build when a consumer
  demands it).
- Recorded summit end-states: declarative catalog reconciliation for migrate
  (preconditions+journal+reconcile are the stepping stones); structural
  semantics/metadata split in the model (format already matches it); DB/boot-time
  revision binding (tracking-table + consumer startup assertion).

## Effort

Rough order: phases 0-2 are each 1-2 sessions; phase 3 is 1-2 sessions; phase 4 is 2-3
sessions (six languages + consumer coordination); phase 5 is the largest (4-6 sessions,
DB-backed test expansion included); phase 6 is 1-2; phase 7 is 3-4; phase 8 is 1; phase
9 is 2-3. Dependencies are strictly ordered 0 -> {1,2} -> 3 -> 4 -> 5 -> 6 -> 7 -> 8,
with 9 dependent only on 0 and schedulable anywhere after it.
