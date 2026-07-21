# pgdesign roadmap: the kernel and its boundary

Consolidated plan from the 2026-07-19 design session, five adversarial critique
rounds (the fifth focused on the mathematics itself), and an algebraic
reframing (2026-07-21). This file is exempted from
todo immutability by explicit owner authorization — a living plan; git history
preserves all prior versions, including the pre-algebraic v6 whose content this
version reorganizes (nothing was dropped; everything was re-derived).

The reframing: six versions of iterative design converged, without naming it, on
a small algebra — a content-addressed object model wrapped around a category of
schema migrations, governed by one equivalence relation. Every defect class the
critique rounds found is a violation of one of the laws below. This version
states the laws first and derives the plan from them, so that the remaining
work eliminates defect classes by construction instead of whack-a-mole, and so
that any future proposal can be judged the same way: which law does it
implement, check, or violate?

---

# Part I — The algebra

## Objects and primitives

- **Model**: the fully-resolved schema IR (tables, views, matviews, functions,
  sequences, types, extensions, pg_version, comments, groups — everything that
  determines generated artifacts, DDL and beyond).
- **enc**: the canonical per-object encoder, Model-object -> canonical bytes;
  **decode** is its inverse on canonicalized models (decode∘enc = id is a
  checked property — 5.9 and 7.2 deserialize, so decodability is load-bearing).
- **N**: the normalizer (types, defaults, expressions via parse/deparse);
  **≈_syn** := the kernel of N (a ≈_syn b iff N(a) = N(b)) — an equivalence
  relation BY CONSTRUCTION. **≈_pg** := Postgres's semantic equality — a
  distinct, richer relation we do not compute (see L1).
- **hash**: SHA-256; **id** = hash(enc(x)); **revision** = id of a whole-model
  manifest — a NAME-SORTED MAP object-name -> object-id. Store + manifests form
  a two-level Merkle DAG: manifest comparison is key-wise symmetric difference;
  the shared consistency checker IS Merkle closure verification (every id in
  every reachable manifest resolves in the store — a completeness spec, not a
  checklist); diff gains an O(changed-objects) fast path by comparing
  per-object ids before deep comparison (a shared skeleton for encoder and
  differ — structural support for the single-≈ law).
- **store**: content-addressed map id -> bytes (put/get; puts idempotent).
- **chain**: parent-linked edges between revisions; an edge is a **migration**
  with identity (sequence, from, to) — parallel edges and endomorphisms
  (pure-DML migrations, R -> R) are representable, so edges are NOT determined
  by their endpoints.
- **diff**: (Model, Model) -> Delta. A Delta is a flat description of change,
  NOT a morphism: Deltas do not compose or invert; all composition happens on
  op-lists. diff's specification is L10; diff(a,a) = empty is a pinned test.
- **apply**: the map from chain edges into the world (codomain defined in L5).
- **journal**: the durable trace of apply's actions, with recorded inverses.
- **stamp**: artifact -> revision-that-produced-it (provenance).

## The laws (L1-L10)

- **L1 (One canonical form — with honest status tags).** N is the normalizer;
  ≈_syn is its kernel, an equivalence by construction. (a) enc encodes
  N-normal forms, so enc(a) = enc(b) iff a ≈_syn b — BY CONSTRUCTION,
  property-testable, invariant from kernel 1.1 on. (b) Single-≈: every
  comparison engine (encoder, differ, predicates) computes ≈_syn — enforced
  progressively via the conformance pair: revision-equal implies diff-empty
  (initial gate); diff-empty implies revision-equal (end-state, activated once
  the differ fully adopts N). (c) Boundary conjecture: ≈_syn ⊆ ≈_pg, KNOWN
  INCOMPLETE — pg_get_* returns the deparse of the post-analysis tree
  (IN -> = ANY(ARRAY[...]), materialized casts), which is catalog-dependent
  and unreachable by any pure string normalizer. The gap is bridged on live
  paths by an explicit, finite REWRITE-RULE SET applied to the introspected
  side (fixture-checked, incompleteness documented; the exactness alternative
  — live round-trip normalization through the DB, available on precisely the
  paths that see pg_get_* text — is recorded in the alternatives list).
  Structural sublanguage: the ORDER-SEMANTICS TABLE (which collection orders
  are semantic — columns, enum values — and which are canonical-only —
  checks, indexes, uniques, policies, triggers) is part of the format spec;
  that table IS the definition of ≈_syn on structure.
- **L2 (Content identity / extensionality).** id = hash∘enc (id equality
  implies content equality MODULO SHA-256 collision resistance — a stated
  assumption, since id-equality fast paths skip byte comparison);
  get(put(x)) = x; puts idempotent; identity location-free; decode∘enc = id
  on canonicalized models. Mutation of STORE CONTENT (objects, manifests) is
  not an operation this structure has. Chain-edge FILES are location-addressed
  — their append-onlyness is CHECKED POLICY (the consistency checker), not
  structural impossibility; the derivation is honest about the difference.
- **L3 (The chain is the free category on the edge graph).** Composition =
  path concatenation; identities = empty paths — VIRTUAL: never files, never
  applied (these laws hold trivially and are not what needs testing). The real
  content: (a) edge identity is (sequence, from, to) — the hom-set question is
  answered explicitly, parallel edges and pure-DML endomorphisms are legal;
  (b) SQUASH SOUNDNESS — a consolidation edge is a NEW edge whose ops must be
  apply-equivalent to the path it supersedes: a CHECKED property (the
  commutation test in L10/5.3), never "definitional."
- **L4 (Three-way typed invertibility).** Every primitive op is typed:
  MECHANICALLY-INVERTIBLE / DECLARED-INVERSE (including DML ops whose declared
  inverse is vacuous — data is not restored; today's reversibility semantics,
  now explicit) / NON-INVERTIBLE. The inverse of a composite is the reversed
  composition of component inverses, defined WHEN every component has one.
  This is a deliberate conservative under-approximation: a composite can be
  semantically invertible when components are not (chained type changes whose
  endpoint diff yields a clean structural down) — the manifest-diff down is
  used ONLY for fully-mechanically-invertible ranges; elsewhere recorded downs
  compose. What remains unrepresentable: a manifest-diff down for a range
  containing data-bearing ops.
- **L5 (Apply is a functor on schema-states).** The codomain is named: objects
  are ≈_syn-classes of INTROSPECTED SCHEMA STATES; morphisms are observed
  transitions. Data is deliberately OUTSIDE the codomain — which is exactly
  why rollback equivalence is structural and why apply does not preserve
  inverses on data (the caveat is a codomain choice, not an inconsistency).
  Preconditions are the domain check ("the world is at R_from"); reconcile is
  the codomain check ("the world arrived at R_to"); the journal is the trace.
  Drift is a domain error — always loud, never absorbed. Generation is a pure
  function of revisions and NEVER reads the world. The substantive functor
  equation — apply(consolidation) lands where apply(sequence) lands — is the
  named squash-commutation test.
- **L6 (Total provenance).** Every derived artifact carries the revision that
  produced it; regeneration is re-application of a pure function; freshness is
  extensional equality. All enforcement rules are DERIVED from provenance, not
  legislated case-by-case.
- **L7 (Model classes don't cross).** A model with type information and an
  introspected model without it belong to different model classes. Their
  revisions are values of distinct types; comparing them is a type error, not
  a runtime mismatch.
- **L8 (The trace is recoverable in the world's terms).** Every journal write
  is atomic with its effect, or wrapped in an intent/confirm protocol whose
  resume is idempotent IN THE WORLD'S OWN STATE MODEL (e.g. Postgres's
  invalid-index semantics), not merely in ours.
- **L9 (Verification is law-checking).** Kernel properties are checked by
  property-based tests over GENERATED inputs, not hand-picked fixtures:
  encoder totality (the reflection coverage guard); decode∘enc = id;
  normalizer idempotence N∘N = N over a generated expression corpus;
  SHUFFLED-DECLARATION-ORDER convergence (≈_syn-equal inputs — permuted
  canonical-only collections — encode to identical revisions: canonicality,
  not mere repeatability); squash-rewriting confluence (finite critical-pair
  enumeration + a termination measure, hence unique normal forms by Newman's
  lemma); the L10 round-trip; and a GOLDEN CORPUS of normalized expressions
  committed so that a go-pgquery dependency bump that shifts ≈_syn — which
  under L1 shifts IDENTITY — turns CI red instead of silently re-keying the
  world. Example fixtures are for the boundary, where laws end.
- **L10 (Round-trip — the central theorem).** For models a, b: applying
  gen(diff(a, b)) to a world at revision(a) lands it at revision(b), verified
  by reconcile — gen is a section of apply-then-introspect up to ≈_syn. This
  is THE specification of diff and generate; preconditions, reconcile, and
  pure generation are scaffolding around this one equation. Checked as a
  randomized DB-backed property test (generate model pairs, diff, apply,
  reconcile) plus its corollaries: diff(a,a) = empty (pinned);
  squash-commutation (apply the consolidation vs apply the sequence — same
  schema-state); and diff MINIMALITY as a non-normative quality property
  (mutation-tested: delete any op, reconcile must fail).

## The boundary doctrine

The system is a THREE-WAY partition, and defects are triaged accordingly:

1. **The kernel** — law-governed. Every law names its property tests (L9), so
   "a law was implemented wrong" is a CHECKABLE claim against a stated
   property, never a rhetorical escape hatch. Defects here are implementation
   errors; the fix is in the kernel and the property suite gains the case.
2. **The enumerated boundary** (Part IV) — everything we do not control:
   Postgres's runtime semantics and crash timing, the filesystem, git's merge
   behavior, six consumer languages, consumer code. Defect classes here cannot
   be made unrepresentable — only checked, by fault injection, conformance
   matrices, and compile checks.
3. **Plain engineering outside the algebra** — phase 9's presentation work,
   CLI ergonomics, doc wording, seed statistical quality. Ordinary bugs, no
   doctrinal claim; forcing the formalism onto them would be ceremony.

The boundary list may GROW, but only with a post-mortem answering "why was
this not derivable from the laws?" — growth is permitted; unexplained growth
is not. (An earlier "the list is closed" absolutism was withdrawn as
unfalsifiable: with no law formally verified, any defect could be retro-labeled
an implementation error.)

The critique rounds support the shape empirically — findings migrated outward
from policy (round 1) to mechanism (round 2) to specification (round 3) to
boundary protocols (round 4) to the formalism itself (round 5) — an induction
over five points, weighted accordingly, not proof.

---

# Part II — Decisions as derivations

Provenance convention: `[deliberate]` = the owner's own axioms (fixed).
`[law]` = a consequence of Part I — originally trust-adopted (%%) during the
design sessions, now DERIVED; reversing one requires rejecting a law, not just
changing a preference. `[%%]` = genuinely free choices (names, layouts,
per-language mechanics) that the laws do not determine — weakly held,
reversible, never to be cited as deliberate intent.

## Owner axioms `[deliberate]`

- No rename; the project stays pgdesign.
- ONE release for the whole roadmap, at the very end (global rule).
- No backward compatibility, ever, for pre-stable projects (global rule).
  (Note how this axiom and L2 reinforce each other: compat is keeping two
  identities for one content; extensionality has no such operation.)

## Consequences `[law]`

- Append-only STORE CONTENT (objects, manifests) — L2 structurally; append-only
  CHAIN-EDGE FILES — checked policy via the consistency checker (they are
  location-addressed; the math round corrected an overreach here). Archived
  originals; unconditional checksums on the apply surface — L2. (The
  "checksums on rollback" clause died because post-journal rollback reads no
  files; a checksum surface cannot exist there.)
- Squash = a consolidation edge (composition), never a rewrite — L3.
- Consolidation downs derived by manifest diff ONLY for fully-invertible
  ranges; DML/RawSQL-containing ranges compose recorded downs — L4.
- Pure migration generation = diff(head manifest, current model); its
  specification is L10's round-trip — L5+L10. (WHICH pure emission policy to
  use is NOT law-derived; the always-large-table-safe choice is a free choice
  — see the [%%] section.)
- Precondition drift = hard error, always; reconcile after apply; adoption of
  intentional drift only via explicit baseline — L5.
- Journal records op identity AND serialized down-op; rollback is DB-driven;
  pre-upgrade prefix and baselines are rollback-frozen (their journal rows
  cannot contain executable inverses — honest scoping, not compat) — L5+L4.
- Ops reference their objects and transitive type closure BY CONTENT ID into
  the store; no inline blobs, no lossy mirrors (degraded ops unrepresentable)
  — L1+L2.
- Revision = hash of canonical bytes; every artifact stamped with a producing
  revision; enforcement taxonomy (full regenerators / partial writers / source
  editors) derived from provenance totality — L6. (The stamp's SCOPE —
  full-project rather than per-output — is NOT law-derived; L6 is satisfied by
  either; the full-project choice is engineering, justified by the
  filtered-output paradox — see the [%%] section.)
- Opaque Revision type; registry-absent marker INSIDE the hashed bytes;
  cross-algebra comparison errors — L7.
- Intent/confirm journaling for non-transactional ops, with resume protocols
  defined against Postgres's state model (pg_index.indisvalid for interrupted
  CREATE INDEX CONCURRENTLY; IF EXISTS added to DROP INDEX CONCURRENTLY) — L8.
- One normalization primitive consumed by differ, predicates, upgrade
  reconcile, and shadow test; predicate IR = one structured definition with a
  Go executor (structured diagnostics; shares introspect's catalog-query
  layer) and a SQL renderer, conformance-matrixed — L1. (A critique proposal
  to drop the Go executor was REJECTED: the executor exists for structured
  object/expected/found diagnostics, not DB-freedom; the matrix exists because
  the SQL renderer is a second computation of ≈ in another language — an
  irreducible boundary item.)
- One canonical serializer everywhere (generate json = serve payload = import
  surface = op bodies = revision manifests); the encoder is a dedicated
  canonical encoder with a reflection-based field-coverage guard (totality is
  mechanically checked, not hoped) — L1+L9.
- verify-then-stamp `migrate upgrade`: single DB transaction (lock; snapshot
  applied set; build journal/view/position; ASSERT view reproduces snapshot;
  DROP old table; COMMIT), content-addressed file writes idempotent and
  BEFORE the commit (the reverse window is harmless by L2's idempotence) —
  L5+L8+L2.
- Compiler/live seam (build and generation pure; DB work in a distinct tier);
  live-only analyses (TANE, pg_stat) are DB-tier and non-retroactive — L5.
  (That pure analyses BLOCK in revise's pure tier is NOT an L5 theorem; it is
  a policy derived from the owner's hard-constraints philosophy — see the
  [%%] section.)
- Fail-closed imports: owned tables in Tables, imported in ImportedTables;
  every consumer iterating Tables is correct by omission; the union is wired
  at the ENUMERATED resolution sites (buildTablesByName, BuildFKGraph, seed
  pools, D2/GraphQL edge emitters) — L6-style totality applied to name
  resolution.
- Header/stamp grammar with one writer and one reader (pkg/genkit); one
  wording, adopted in a single pass (the byte-preserve-then-reword staging
  died with the one-release axiom — its justification was multi-release) —
  L6+L9.
- Property/fault verification style throughout: multi-iteration determinism
  tests, encoder coverage guard, conformance matrix, fault-injection matrix,
  DB-free compile checks of generated fixtures — L9.

## Free choices `[%%]`

Names and layouts the laws do not determine: `pgdesign_migration_ops`,
`pgdesign_applied_migrations` (view; merit: one SQL definition of
"applied + status" for four readers), `pgdesign_chain_position`,
`migrations/chain/<seq>-<from>-<to>.json` (one file per edge; edge identity =
(sequence, from, to), so parallel edges and pure-DML endomorphisms never
collide — chosen over a single manifest for git-merge friendliness; the DAG is
the data),
`migrations/objects/`, `migrations/revisions/`, `migrations/archive/`,
`imports/<alias>/`, visible (non-dot) directory names for committed
load-bearing data, `internal/objstore`, `internal/project`,
`internal/predicate`, normalization homed in `internal/sqlparse` (the
go-pgquery leaf — necessary, since ≈ must match pg_get_* forms), sequence+slug
filenames with auto-derived slug (override flag), `import lock` /
`import update`, `migrate upgrade`, `migrate rebase`, `pgdesign revise`.
Per-language branding mechanics (boundary-empirical, not law-derived): Go
opaque struct with validating boundary and var members; Python parse() alias +
enum-typed surfaces + Row __post_init__ coercion on BOTH backends; TS
keep-the-union + parse(); Java/Kotlin value-parse + JPA AttributeConverter;
Zig wrapper struct; sqlalchemy upgraded to native sa.Enum; drizzle no change
(already pgEnum-typed); constants mode unchanged; constraints validators
re-target the branded representation. Seed import tiers (real-key pools /
count-wrapped offset subqueries / hard errors scoped to the provably-broken
cases). strictcli connection-env kind with registration-time unbound-flag
error. Partition: premake required; opt-in schedule key; unacknowledged
missing schedule = warning. pkg/diff deleted with a recorded promotion
trigger. Web UI frontend deferred. Consumer regeneration todos filed at the
single final release. Policies demoted from [law] by the math round (good
engineering rationales, honestly labeled as choices): ALWAYS-large-table-safe
generation (uniformity — a declared size hint would be equally pure);
FULL-PROJECT stamp scope (resolves the filtered-output paradox); pure
analyses BLOCK in revise's pure tier (the owner's hard-constraints
philosophy applied — analysis that can block must block).

## Withdrawn along the way (each a law violation in hindsight)

Compat-named view (owner axiom violation); header staging (multi-release
assumption); checksums-on-rollback (L2 surface that doesn't exist); plain
Python Enum and construction-closing machinery (chased a phantom — native
validation already exists); TS nominal brand (regressed compile-closure);
@Enumerated(STRING) (persists names, not values); registry builtin-inclusion
special case (redundant — builtin-derived domains materialize into model
collections that L1 already covers); manifest.jsonl and whole-model snapshots
and dot-dirs (superseded by per-edge chain + object store + visible names);
snapshot-diff downs for DML ranges (L4 violation); rejecting (rather than
validating) Go boundaries (breaks scans); row-count-conditional generation
(L5 violation); the closed-boundary-list absolutism (unfalsifiable — replaced
by three-way triage with growth-on-post-mortem); L4's "IFF" (claimed a false
converse — composites can be semantically invertible when components are
not); "squash IS composition definitionally" (empty without a morphism
congruence — replaced by the free-category framing plus the CHECKED
squash-commutation property); "one ≈" stated without naming ≈_syn vs ≈_pg
(the iff was unachievable for expressions against pg_get_* forms by any pure
string normalizer).

---

# Part III — Where the codebase violates the laws today (grounding)

Source-verified across four critique rounds. Organized by law; file references
are load-bearing for implementors.

**L1 violations (no canonical form, no single ≈):**
- resolveTable ranges Go maps — raw model order nondeterministic; only some
  emitters re-sort (7 Sorted* helpers); matview indexes unsorted everywhere;
  luck-stable raw-map emitters: gorm, drizzle, jpa, sqlalchemy, validator
  policy extraction, python query-layer (~12 sites). enrich() appends auto-FK
  indexes after resolveTable (sorts must run post-enrich). Topological
  ordering exists in FOUR places (model table topo in build.go; generate.go;
  python_ddl.go; internal/format's alphabetical-pre-sort + TopoSort — the
  last is the tie-break blueprint to reuse). Topo tie-break is input-order:
  TOML declaration vs introspect ORDER BY name diverge; introspected
  functions lack DependsOn; Extensions ordering diverges the same way (and
  Extensions are in identity). Top-level type collections: DDL
  declaration-order vs JSON name-sorted. The DDL emitter's determinism rests
  on ~7 inline Sorted* calls. The existing determinism test hand-builds
  structs and can never fail.
- Two divergent JSON serializers (generate json sorts; serve emits raw).
- The differ compares expressions by RAW STRING against PG-rewritten forms
  (pg_get_constraintdef/expr/indexdef) — live false-drift bug on
  CHECKs/partial indexes/policies; only types normalize. The differ is BLIND
  to PGVersion (in identity, not in SchemaDiff — pg_version changes alter
  emitted DDL invisibly to diff). The differ DOES compare comments and
  extensions. diff's normalizeDefault is ToLower(TrimSpace(...)) — UNSOUND,
  not merely incomplete: it identifies the semantically distinct defaults
  'Active' and 'active' (MISSED drift — the reverse failure mode from
  false drift; red test required before 1.2 replaces it). A SECOND live
  normalizer exists: validate.normalizeExpr (W018) sorts commutative AND/OR
  operands alphabetically and strips parens — a different equivalence than
  parse/deparse computes; 1.2's consumer list must retire it onto N (or
  explicitly scope W018 as a looser heuristic), else the system ships two
  ≈-computations — precisely what L1 prohibits.
- semtype.Registry unexported/unserializable; typeDefsEqual ignores top-level
  Comment/Source but compares nested transition comments; builtin-derived
  domains (slug/email/short_text, scalar-with-CHECK) materialize into
  schema.Domains when used — identity coverage comes from the model
  collections; TypeDef.Source doc comment stale; type extends eagerly inlined
  (closure = composition references only).
- pg_version has THREE resolution tiers (live > config > toml); the live tier
  is a DB input that cannot enter pure Build; ~10 cmd sites mutate
  schema.PGVersion post-Build plus a channel via generate.Options.PGVersion.

**L2 violations (mutable history, no content identity):**
- Squash deletes/rewrites originals (saferm + rename over <to>.toml); the
  M200 applied-version guard runs only if --db is voluntarily passed;
  tracking rows orphaned; zero squash-CLI tests; optimizeDDLOps keeps only
  the final type-change's down (reverts one step, not to pre-range type).
  The optimizer is a greedy, ONE-SHOT (not fixpoint), dependency-unaware
  rewriter: pair cancellation examines only the two endpoint ops — `add
  column x; create index on x; drop column x` cancels the add/drop pair and
  ORPHANS the index op (a live squash bug); no associativity, confluence, or
  order-independence tests exist.
- Migration checksums are recorded over file bytes and NEVER verified.
- No ledger/manifest/chain exists; discovery skips non-semver filenames; ~7
  functions rely on semver ordering; migrations-dir sentinel hardcode at 8
  migrate sites + 1 serve site (the `output` flag shows the correct
  Default(nil)+was-set pattern).

**L1+L2 violations (ops carry lossy mirrors):**
- THIRTEEN unserialized op-family concerns: nine pointer-def families +
  RawSQL (SM-trigger DDL and partman UPDATEs silently dropped on round-trip)
  + PartitionChildSpec + ParentTable + the partman-config ops that phase 0.6
  introduces (update_partman_retention/premake hit OpToSQL's default
  comment-stub TODAY). Down-ops embed def pointers too and degrade on
  rollback. create_function/create_trigger parsed from disk fall back to
  emitting the WRONG OBJECT (deny-mutation / append-only); sequences lose
  parameters. opCreateTable passes nil enum/domain lists (unqualified type
  rendering) and hardcodes pgVersion=0 despite DDLOp.PGVersion existing.

**L5 violations (the functor reads or trusts the world wrongly):**
- Generation consumes live TableStats (pg_stat_user_tables) to choose
  NOT VALID splits and expand/contract forms — deleting it also removes the
  EXPAND_CONTRACT_TYPE_NARROW advisory warning (must be relocated to diff
  classification). migrate generate requires --db + --version today.
- No preconditions anywhere; version row written LAST (committed phases and
  non-transactional ops leave real DDL with no durable record; re-apply
  restarts at op 0 and aborts forever). Rollback re-reads files, trusting
  them over the DB. Two divergent tracking write paths (state.go helpers vs
  inline SQL in apply/rollback).
- Introspect has NO table-level filtering — the tracking table introspects as
  a user table; function/trigger filters use the LEADING-underscore
  `_pgdesign_sm_%` pattern (a `pgdesign_%` table pattern does not cover it;
  view filtering needs its own treatment). Introspect constructs Schema
  directly (never Build): nil FKGraph/TablesByName, raw order; the
  copy-pasted finalize is four steps, two of which (SM transitions, group
  resolution) need raw/registry inputs introspect lacks. BASELINE exists
  (baseline.go; "baseline" checksum literal; semver-based guards to
  re-express against chain reachability). SHADOW TEST exists
  (handlers_migrate.go:987-1133). serve's handleMigrations queries the
  tracking table (existence-guarded — returns 200 with [], not a 500);
  version endpoint opens version+".toml".

**L6 violations (provenance absent or partial):**
- Headers: 36 codegen sites (5 validator helpers within them) + CLI
  planner-prepend for sql/d2/graphql (json and doc headerless) + seed's
  distinct wording; 7+ wordings; hasCommentHeader lacks `--`; Go headers
  don't match the `^// Code generated .* DO NOT EDIT\.$` tooling regex;
  genkit's Generator/MultiFileGenerator interfaces are DUPLICATED in
  internal/codegen. codegen --check is byte-exact (pkg/genkit). splitfmt
  (.sqlsplit) is SEALED (line 1 = statement count — cannot carry a header).
  fmt rewrites schema TOML (--column-order = revision change); introspect
  --output writes source. build applies per-output FilterByGroups/
  FilterBySource; standalone codegen does NOT (same artifact, two contents by
  entry point); build auto-commit warns-and-continues on safegit failure.
  Seed content depends on --seed/--counts/--mode and is never
  freshness-checked (its stamp will be honest provenance only).

**L7 territory:** introspected models lack the registry; serve returns
{schema, diagnostics} (envelope must wrap, not drop, diagnostics).

**L8 territory (the world's crash semantics):**
- IsNonTransactional: create AND drop_index_concurrently +
  version-conditional enum-add (transactional PG12+). An interrupted CREATE
  INDEX CONCURRENTLY leaves an INVALID index of the target name — IF NOT
  EXISTS would skip it forever (and sql.go's comment claiming CIC+IF NOT
  EXISTS is version-incompatible is wrong; valid since 9.5).
  drop_index_concurrently's renderer lacks IF EXISTS. The advisory lock is
  session-level, shared by apply/rollback/baseline, held across reopened
  transactions.

**Boundary facts (languages, consumers, environment):**
- Codegen enum shapes: Go `type X string` + const block (const of STRUCT
  type is illegal — branded members must be vars); TS literal union
  compile-closed (transition maps ALREADY typed Record<Status, Status[]>);
  drizzle ALREADY emits pgEnum as the column builder (typed — no change
  needed); sqlalchemy keeps str but sa.Enum(PyEnumClass) is native (upgrade,
  not exception); Python Enum.__call__ already validates (no closing
  machinery needed; residual str-structural openness unclosable); Java/
  Kotlin real enums with UPPER_SNAKE names vs raw getValue() values
  (@Enumerated(STRING) would persist NAMES); Zig string consts (transition
  maps use sanitized struct-field keys). No parse helpers anywhere. ILLEGAL
  JAVA (multiple public types per file) in THREE modes: java_jpa,
  java_types, java_constraints. The conformance tests compile HAND-AUTHORED
  templates, never codegen output, and are DB-gated — CI (go vet/test only)
  would not catch the Java bugs; DB-free generated-fixture compile checks
  are a separate deliverable. Python query-layer neither imports nor defines
  the enum classes it annotates (survives via future annotations); BOTH
  PgBackend and InMemoryBackend build rows uncoerced (Row __post_init__
  covers both); _constraints.py needs NO change. go_types and go_gorm both
  emit GenerateEnums into package schema (dedup must be co-generation-aware).
  Seed's pool-empty fallback silently emits random UUIDs; the UNIQUE dedup
  keys the concatenation of ALL constraint columns with a fixed-rowIdx retry
  then SILENT fall-through.
- strictcli: the check command builds a fully-populated *Context and
  discards it; infra roots + handshake envs hermetic-IMMUNE, flag Env()
  hermetic-SUPPRESSED (no primitive fits a connection URL); per-flag
  provenance already exists (Context.Source()). SEVENTEEN DB-URL flags (16
  --db + 1 --live), three default semantics.
- Partition bugs: python_ddl passes Retention as p_interval (sibling of the
  fixed generate path); omitted premake emits p_premake := 0 (disables
  partman); silent skip when pg_partman undeclared; maintenance + manual
  children emit contradictory DDL; PartmanRunMaintenanceCron() is
  dead-but-tested code.
- pkg/diff (exported stub): zero importers. internal/diff: ~22 model types
  consumed field-by-field by migrate. generate and migrate are siblings;
  internal/sqlparse is the go-pgquery leaf (imported by migrate, introspect,
  model, workload, testdb); sqlutil is imported by validate+codegen.
- serve: DB-coupled at construction; --timeout registered but never
  enforced; audit runs TANE synchronously; GenerateD2 called with a nil
  registry (SM diagrams silently dropped); project-loading helpers live in
  package main. serve/handlers.go is co-edited by phases 5 and 8 (ordering
  required). CI: postgres:17 + pg_partman; 11 DB-backed migrate tests of
  ~162.

---

# Part IV — The boundary (enumerated residual risk)

Everything below is irreducible — checkable, not eliminable. This list is
closed: a future defect found OUTSIDE it means a law was implemented wrong
(fix the kernel), not that the list grows.

1. **Postgres crash windows** around non-transactional DDL (CIC, drop-CIC,
   pre-PG12 enum-add). Check: fault-injection matrix incl. indisvalid
   assertions (5.5).
2. **The upgrade choreography** (DB transaction + pre-commit file writes).
   Check: crash injection on both sides of COMMIT (5.2).
3. **The SQL predicate renderer** — a second computation of ≈ in PL/pgSQL.
   Check: the conformance matrix (Go executor vs SQL renderer vs differ) —
   which is SAMPLED agreement, not proof of ≈-agreement, so it is fed
   GENERATED random expressions in addition to curated states (5.7).
4. **The ≈_syn/≈_pg gap** — pure normalization CANNOT reach PG's
   post-analysis forms (catalog-dependent rewrites); the explicit
   rewrite-rule set on the introspected side is finite and documented-
   incomplete BY DESIGN. Check: the rewrite-rule fixture suite; the golden
   normalized-expression corpus (a dependency bump that shifts ≈_syn turns
   CI red); the comprehensive fixture (CHECKs, partial indexes, policies)
   reused by diff --live, upgrade, reconcile, shadow test (1.2/5.8).
5. **Six consumer languages' semantics.** Check: DB-free compile checks of
   generated fixtures — all six mandatory (4.0).
6. **Git merge behavior** on chain files. Minimized by one-file-per-edge
   (textual conflicts impossible); semantic forks remain. Check: two-head
   detection + `migrate rebase` (5.10, 6.1).
7. **Concurrent binaries** on one database. Check: the shared session-level
   advisory lock; concurrent-apply-during-upgrade test (5.2).
8. **TOCTOU between check and apply** on a live database. Minimized:
   preconditions run inside each op's transaction (5.7).
9. **Filesystem atomicity** for store writes. Minimized: content-addressed
   idempotent writes; consistency checker (5.2, invoked by 6.2 and 7.2).
10. **Consumer adaptation** to the breaking release. Check: filed todos +
    consumers' drift-check scripts (4.3).
11. **External milestone**: strictcli must ship the connection-env kind
    before phases 6.1/7.4/seed-tier-1 finalize (todo filed at phase-0
    start).

---

# Part V — Phases

Phase numbering: 0 = substrate repairs; 1 = the kernel; 2 and 4-9 = boundary
functors and adoption, keeping their pre-reframe numbers where content is
unchanged. Phase 3 is RETIRED: the identity work it held (whole-model form,
revision hash, one serializer) was absorbed into kernel subphases 1.4/1.5, and
the number is not reused. Every subphase cites its laws.

## Phase 0 — Substrate repairs (make the codebase law-capable)

Bug fixes and consolidations that must precede the kernel; none depend on it.
Build order: 0.1 -> 0.2 -> 0.3; 0.4/0.5 after 0.2; 0.6 anytime (matching the
DAG). The strictcli todo (boundary item 11) is filed at phase-0 start.

### 0.1 Header unification + stamp grammar — L6
- **What:** One shared parameterized header helper homed in pkg/genkit
  (writer, reader, and stamp grammar co-located; seed must stamp and cannot
  import internal/codegen; genkit's duplicated Generator/MultiFileGenerator
  interfaces absorbed). Final wording adopted immediately in ONE pass —
  `Code generated by pgdesign. DO NOT EDIT.` (Go-tooling-recognized),
  per-language comment prefix, free-text parameter for seed — routed through
  all 36 codegen sites, the CLI planner-prepend path (sql/d2/graphql),
  codegenHeader/hasCommentHeader (learns `--`), and seed. The stamp grammar
  (format + parser, format-versioned) is designed now; the revision line
  lands later as a helper-INTERNAL addition (zero site re-touches).
- **Why:** L6 needs one writer and one reader of provenance; 40+ literals
  with 7+ wordings is that many chances for an artifact invisible to
  enforcement. One pass because the one-release axiom makes staging pure
  double work.
- **Verify:** Zero header literals outside the helper (grep) AND the
  positive invariant: every generator output parses as beginning with the
  canonical stamp via genkit's parser; goldens updated once; Go headers
  match the tooling regex.

### 0.2 Canonicalize() — L1 prerequisite (determinism)
- **What:** A shared finalize routine invoked by Build, BuildMulti,
  Introspect, and FilterByGroups/FilterBySource: alphabetical ordering for
  per-table collections (incl. matview indexes), top-level type collections,
  and Extensions; topological ordering with ALPHABETICAL tie-break for
  tables/views/matviews/functions — reusing internal/format's
  pre-sort-then-TopoSort pattern; all FOUR topo paths collapse here
  (CycleGroups semantics preserved); introspected functions (no DependsOn)
  fall back to alphabetical; columns source-ordered; derived structures
  (FKGraph, TablesByName) built here. Scope split: Canonicalize owns
  ordering + derived structures; the raw/registry-dependent finalize steps
  (SM transitions, group resolution) stay Build-side. pg_version: config+toml
  tiers resolve inside Build; a post-Build live-override seam replaces the
  ~10 scattered mutations and the Options.PGVersion channel. Sorts run
  post-enrich. Delete the 7 Sorted* helpers and ALL emitter-side sorting
  (incl. the SQL emitter's ~7 inline sites — DDL fixtures must cover this).
  Fix the luck-stable emitters. Multi-iteration TOML->Build->serialize->
  compare CI determinism test (pinned iterations; >=2 entries per map-sourced
  collection) + Canonicalize postcondition. Goldens change once.
- **Why:** L1 is impossible over nondeterministic bytes. A shared finalize
  makes INTROSPECTED schemas canonical (L5's codomain checks and L7's
  algebra both need it) and collapses four topo implementations.
- **Verify:** Determinism test red before/green after over DDL AND JSON;
  CANONICALITY, not just repeatability: shuffled-declaration-order fixture
  pairs (permuting canonical-only collections — checks, indexes, uniques,
  policies — never columns or enum values, whose order is semantic) produce
  identical output; view-references-view fixture dependency-ordered;
  introspected schemas pass the postcondition; no emitter-side sorting by
  grep; filtered schemas carry recomputed graphs.

### 0.3 Schema-qualified identity + final graph API — L1/L6 substrate
- **What:** The FKGraph/walker end-state API in ONE pass: FKEdge gains
  schema qualification AND `Imported bool`; keys become (schema, name) —
  reconciling TablesByName's "schema.name" (and its ".name" empty-schema
  artifact) with FKGraph's bare names under one keying rule; group
  resolution rekeyed with a stated bare-to-qualified rule; cascade walkers
  gain a depth-bounded signature. Plus the FKGraph PROJECTION SERIALIZER
  (deterministic, (schema,name)-keyed; excluded from identity, included in
  the API payload) — owned here.
- **Why:** Two identity schemes for one object is a latent bug today and a
  guaranteed one under imports; single-pass API design prevents three
  planned rounds of churn (the collapse-multi-pass rule).
- **Verify:** Red-green: same-named tables in two schemas through cascade
  checks (W013/14/15), workload, group filtering; depth-bounded walk tested;
  projection serializer deterministic and reconstructable.

### 0.4 Introspect filters managed objects — L5 hygiene
- **What:** One isManagedObjectName() predicate: `pgdesign_%` for tables and
  views (view filtering designed here) and the legacy `_pgdesign_sm_%`
  function/trigger prefix. Reserved-name user objects trigger a diagnostic.
- **Why:** L5's codomain check ("introspect, diff, expect empty") is
  unusable if the functor's own trace registers as drift; pattern filtering
  makes future managed objects inherit coverage.
- **Verify:** Phase-0 scope: pattern filtering against SYNTHETIC
  reserved-name objects + the diagnostic (the real managed objects arrive in
  phase 5; their introspect-cleanliness assertion lives in 5.2/5.8).

### 0.5 One write path; filtering unified; sentinel fix — L6 substrate
- **What:** Consolidate multi-file write + owned-dir/orphan bookkeeping onto
  the planner; standalone codegen becomes a thin caller AND gains build's
  per-output FilterByGroups/FilterBySource. Fix the migrations-dir sentinel
  at all NINE sites via one shared helper (Default(nil)+was-set pattern).
- **Why:** L6 enforcement must guard every write; divergent write paths and
  filtering are guards that drift.
- **Verify:** Standalone codegen and build byte-identical incl. under
  filters; identical orphan behavior; sentinel red-green at all nine sites.

### 0.6 Ground-clearing bug fixes (pre-law repairs)
- **What:** (a) Delete pkg/diff (zero importers; changelog records the
  promotion trigger: a public differ only when a second flat-schema consumer
  exists). (b) Partition red-green fixes: python_ddl Retention-as-p_interval;
  premake REQUIRED (silent zero disables partman); hard errors for
  non-RANGE+maintenance, undeclared pg_partman, maintenance+manual children;
  part_config query failure becomes a diagnostic. (c) Partition lifecycle
  completion: introspection reads interval/premake/retention from
  part_config; diff distinguishes initial setup / retention-premake updates
  (Safe, risk-classified UPDATE part_config ops — a NEW op family that
  phase 5's op rewrite must absorb; today such ops would hit OpToSQL's
  comment-stub default) / interval changes (hard error + guidance); migrate
  guards extension presence; `schedule` key wires the dead pg_cron helper
  (pg_cron declared or hard error); unacknowledged missing schedule =
  warning. (d) Squash safety stopgap until phase 5: --db and the M200 check
  mandatory (stated limits: blocks offline squash of never-applied ranges;
  doesn't fix the rewrite mechanics); first squash-CLI test.
- **Why:** All are live silent-degradation defects (the class L5/L6 exist to
  kill) or misleading API surface; none depend on the kernel; every one
  removed is one less thing later phases interact with.
- **Verify:** Failing test first per bug; CI pg_partman coverage; partman
  ops render (no comment-stub); squash without --db hard-errors.

## Phase 1 — The kernel (pure, law-tested, no Postgres, no CLI)

The algebra as packages, property-tested per L9. Everything later is an
adapter around this.

### 1.1 enc: the canonical encoder — L1
- **What:** A DEDICATED canonical encoder (explicit field ordering; per-field
  presence semantics distinguishing unset from zero, normalizing to pointers
  where needed; deliberate key-sorting for map-typed fields — Index
  Opclasses/Collations/With, Schema.Groups, NamedTransition.Requires, and
  state-machine transition maps in the type-definition path — the schema-side
  StateMachineTransitions duplicate stays excluded per 1.5) producing
  per-object canonical JSON for every schema object, with a TOTAL DECODER
  (decode∘enc = id on canonicalized models — 5.9 and 7.2 deserialize, so
  decodability is load-bearing, not optional). The ORDER-SEMANTICS TABLE is
  written as part of the format spec: per collection, whether order is
  semantic (columns, enum values) or canonical-only (checks, indexes,
  uniques, policies, triggers) — this table is the definition of ≈_syn on
  the structural sublanguage. Type identity from MODEL-LEVEL collections (both
  construction paths populate them; builtin-derived domains materialize
  there, so builtin changes flip identity with no special case). The
  registry snapshot shrinks to serializing whatever has NO model
  representation (confirmed at implementation; main role = import-surface
  reconstruction), with an explicit field policy (semantic + all comments;
  Source excluded) and the stale Source doc comment fixed. MECHANICAL
  TOTALITY GUARD: a reflection-based test asserting every exported field of
  every DDL-reaching model struct is either encoded or on an explicit
  exclusion allowlist with a reason (CycleGroups, StateMachineTransitions,
  SourceFile, caches).
- **Why:** L1 is the kernel's foundation; the totality guard converts "the
  encoder is complete" from a review hope into a checked law (L9) — the
  highest-value single mechanism in the plan.
- **Verify:** Property tests: per-object bytes independent of neighbors and
  struct-field-order refactors; decode∘enc = id round-trip; coverage test red
  when a field is added unencoded; map-key ordering deterministic;
  shuffled-declaration-order convergence (≈_syn-equal inputs, identical
  bytes); builtin email-regex change flips identity; Source relabeling does
  not; nested transition comments do.

### 1.2 ≈: the normalization primitive — L1
- **What:** The normalizer N — types, defaults, expressions (parse/deparse) —
  homed in internal/sqlparse (the go-pgquery leaf). ≈_syn = kernel of N.
  The ≈_pg GAP is bridged per L1(c): an explicit, finite rewrite-rule set
  applied to the INTROSPECTED side on live paths (IN -> = ANY(ARRAY[...]),
  literal-cast peeling — extending introspect's existing parseSimpleDefault
  idea), fixture-checked, incompleteness documented. The differ adopts N
  IMMEDIATELY (red-green: introspect->diff over CHECKs/partial
  indexes/policies reports false drift today — a live bug), replacing BOTH
  existing normalizers: diff's unsound lowercasing normalizeDefault (red
  test for the 'Active'/'active' missed-drift case FIRST) and
  validate.normalizeExpr (W018 retired onto N, or explicitly scoped as a
  looser heuristic with a comment). Later consumers: upgrade reconcile,
  predicates, reconcile-verify, shadow test.
- **Why:** L1(b): every comparison engine must compute the same ≈_syn — two
  disagreeing normalizers already ship today, and the differ additionally
  disagrees with Postgres's rewriter. L1(c): the gap is real mathematics
  (catalog-dependent rewrites are unreachable by pure string normalization)
  and must be bridged explicitly, not papered over with "approximates."
- **Verify:** Red-green on the false-drift fixture AND the missed-drift
  default fixture; N∘N = N idempotence over a generated expression corpus;
  the GOLDEN CORPUS of normalized forms committed (a go-pgquery bump that
  shifts ≈_syn — hence identity — turns CI red); the rewrite-rule suite;
  diff --live clean on the comprehensive fixture (reused verbatim by 5.8);
  grep: no normalizer outside internal/sqlparse.

### 1.3 store: internal/objstore — L2
- **What:** The content-addressed store package: hash-keyed put/get, dedup,
  layout; multiple roots (migrations/objects/ now; imports/<alias>/ later).
  Property tests: get∘put = identity; put idempotent; ids location-free.
- **Why:** L2 as code; one package so a third store implementation can never
  arise (ops, revision manifests, and import surfaces all reference it).
- **Verify:** Property suite green; concurrent idempotent-put test.

### 1.4 chain: revisions, edges, composition, inverses — L3+L4
- **What:** Revision manifests as NAME-SORTED MAPS object-name -> id
  (comparison = key-wise symmetric difference; the Merkle dividends of Part
  I). Parent-linked edge model, edge identity = (sequence, from, to) —
  parallel edges and pure-DML endomorphisms representable; head/find-heads
  (genesis: null parent); composition = path concatenation (free category —
  identities are VIRTUAL empty paths, never files, never applied). THREE-WAY
  typed invertibility per L4 (mechanical / declared-inverse incl. vacuous
  DML / non-invertible); composite inverse defined WHEN all components have
  one; manifest-diff downs representable ONLY for fully-mechanically-
  invertible ranges. Revision = hash of manifest; Revision is an OPAQUE TYPE
  indexed by model class (registry-present / registry-absent — the marker
  lives INSIDE the hashed bytes; cross-class comparison is a type error).
  Per-object-id diff fast path; diff(a,a) = empty pinned; the conformance
  pair: revision-equal implies diff-empty as the initial gate (differ's
  PGVersion blindness fixed first — pg_version joins SchemaDiff); the
  REVERSE direction adopted as the end-state invariant once the differ fully
  adopts N.
- **Why:** L3/L4/L7 as code; squash, rollback, pure generation, and
  enforcement all become path operations and lookups over this structure.
  The free-category framing puts the trivially-true laws where they belong
  (by construction) and the real risk where it lives (squash soundness —
  checked in 5.3, not asserted here).
- **Verify:** Property tests: inverse laws on fully-invertible composites;
  non-invertible-containing composites have no manifest-diff inverse BY
  TYPE; edge-identity uniqueness under parallel edges and endomorphisms;
  opaque-Revision cross-class comparison errors; diff(a,a) empty;
  conformance direction in CI; sensitivity tests (comment/column/type/
  pg_version/extension changes flip revisions; no-op rebuilds don't). (No
  associativity/identity-edge tests — trivially true in a free category;
  vacuous tests deleted.)

### 1.5 Whole-model form, envelope, one serializer — L1+L7
- **What:** Whole-model form = versioned preamble + ordered concatenation of
  per-object forms. Semantic-only policy: type identity from model
  collections; StateMachineTransitions + CycleGroups excluded as derived;
  FKGraph/TablesByName/caches excluded; Extensions (ordered) + PGVersion
  INCLUDED; object comments IN, TOML-formatting comments OUT; [suppress] and
  extension-registry data OUT (extension DDL-name resolution stays
  emitter-side, byte-compare-covered; baking it into the model is the
  recorded summit alternative). The JSON artifact is an envelope
  {format_version, revision, model, diagnostics?} with canonical bytes
  embedded VERBATIM (raw-message; re-encoding would break
  revision == hash(model)); serve's {schema, diagnostics} shape is WRAPPED.
  generate json and serve schema responses call THE SAME envelope function;
  the divergent serializers die. Revision printed by validate/build. Stated
  policy: a pgdesign upgrade that changes the model schema flips all
  revisions — one coordinated regeneration (the existing consumer
  convention, now load-bearing).
- **Why:** L1 demands one serializer; L7 demands the in-bytes marker; the
  envelope resolves in-band-stamp circularity (bytes cannot contain their
  own hash).
- **Verify:** generate json and serve bodies identical for the same schema;
  envelope revision verifies against embedded bytes; marker present on the
  introspect path; diagnostics preserved; goldens updated once.

## Phase 2 — Environment boundary (strictcli) [external milestone]

### 2.1 Connection-env kind + check context access
- **What:** Third env primitive — hermetic-SUPPRESSED, lazily read, no
  implicit default — plus a REGISTRATION-TIME hard error for DB-URL-class
  flags not bound to a declared connection env. CheckContext interface
  widening + reconciling the two context construction paths (the check
  command builds a full *Context and discards it). Provenance via existing
  Context.Source(). Execution: generically-worded todo filed at phase-0
  start; a strictcli session implements and releases; pgdesign bumps.
- **Why:** A connection URL is precisely what --hermetic should suppress;
  neither existing primitive fits; registration-time enforcement replaces
  review hopes with a mechanical guarantee (L9 in spirit, applied to the
  framework).
- **Verify:** strictcli tests: declaration, lazy read, hermetic
  suppression, check-side access, registration-time unbound-flag error.

### 2.2 pgdesign adoption
- **What:** Declare PGDESIGN_DB once; bind ALL SEVENTEEN DB-URL flags (16
  --db + 1 --live; three default semantics normalized); checks read via the
  framework; --hermetic makes DB checks skip visibly; config [database].url
  stays a documented separate layer (cli > env > config). Not a leaf: every
  later DB entrypoint (revise's DB tier, import lock/update, live
  verification, seed tier-1) binds from birth, enforced by 2.1's
  registration error.
- **Why:** One variable, one story; without the non-leaf edges the
  pathology regrows.
- **Verify:** Env-only invocation on every DB command with provenance;
  hermetic skips; raw os.Getenv gone from cmd/ (test harness excepted);
  precedence test.

## Phase 4 — Language functors (codegen) [numbering retained from v6]

### 4.0 Compile checks + CI toolchains (two deliverables) — L9 at the boundary
- **What:** (a) NEW DB-free generated-fixture compile checks: go build, tsc
  --noEmit, javac, kotlinc, zig build-obj, python type-check over freshly
  generated fixtures for every language-mode — ALL SIX mandatory, no
  "where feasible". The known illegal-Java output (multiple public types
  per file in java_jpa AND java_types AND java_constraints) is fixed IN THE
  SAME CHANGE as the javac check lands — main never red. (b) CI toolchain
  provisioning so the existing DB conformance suite stops self-skipping.
- **Why:** Boundary item 5: claims about six languages are backed by
  nothing today (CI runs go vet/test; the conformance tests compile
  hand-authored templates, not codegen output).
- **Verify:** All six compile checks run in CI on generated fixtures; the
  Java fix lands with its check in one commit; conformance suite runs
  unskipped.

### 4.1 Branding per language — validating boundaries
- **What:** Shared mechanism first (extend the enum_gen dispatch seam;
  enum-emission dedup CO-GENERATION-AWARE — gorm-only consumers rely on
  gorm's block). Go: opaque struct (unexported value field); package-level
  VAR members (const of struct type is illegal; deliberate reassignment
  documented out-of-scope); Parse errors on unknowns; UnmarshalJSON/
  UnmarshalText/sql.Scanner IMPLEMENTED VIA Parse (validating — generated
  structs live in DB-scanned/JSON-round-tripped positions; gorm rides the
  same Scanner/Valuer); Valuer/MarshalJSON/Stringer; zero value detectably
  invalid. Python: parse() classmethod as ergonomic typed alias (native
  Enum.__call__ already validates); query-layer + validator signatures
  enum-typed; the query-layer package gains real enum imports/definitions;
  the ROW DATACLASS gains __post_init__ coercion (idempotent) covering BOTH
  PgBackend AND InMemoryBackend; _constraints.py explicitly unchanged. TS:
  keep the literal union; add parse() at boundaries (transition maps
  already typed). Java/Kotlin: value-based parse; JPA gains a generated
  AttributeConverter (@Convert) backed by getValue()/fromValue() — never
  @Enumerated(STRING); java_jpa, java_types, java_constraints move to
  MultiFileGenerator (one public type per file); JPA gains its missing
  enum-column branch. Zig: wrapper struct + parse; transition maps
  re-keyed. sqlalchemy: UPGRADED to native sa.Enum(PyEnumClass). drizzle:
  NO change (already pgEnum-typed). Constants mode unchanged. Constraints
  validators re-target the branded representation (Go switch on .String();
  Java contains(getValue()); Kotlin equivalents).
- **Why:** The same validating-boundary discipline as L7's opaque Revision,
  applied per language: invalid values cannot be named (compile) or
  smuggled (every ingress validates), with the DB CHECK as backstop. The
  per-language mechanics are boundary-empirical — chosen by what each
  language can express, verified by 4.0.
- **Verify:** Under 4.0's compile checks: invalid values fail at the
  earliest boundary with errors; Go all-ingress round-trip; Java persisted
  value == getValue(); Python both backends yield enum-typed fields, pickle
  round-trips; TS exhaustive switches compile; constraints validators pass
  against branded fixtures; sqlalchemy models carry sa.Enum.

### 4.2 Revision stamping — L6
- **What:** Helper-internal addition (zero site re-touches): revision line +
  stamp format-version via the genkit grammar. Artifact classes:
  comment-stamped = sql, d2, graphql, codegen, doc, seed (seed's stamp is
  honest provenance ONLY — content depends on --seed/--counts/--mode and is
  never freshness-checked; stated); in-band = json (envelope field); stdout
  of generate = stamped in-band, freshness-exempt (stated); structurally
  exempt = svg (non-deterministic) and .sqlsplit (sealed format). Stamp =
  FULL-PROJECT revision always; filtered outputs carry the full-project
  stamp (provenance, not content — byte-compare owns content). Stated cost:
  one schema edit re-stamps every generated file (intended).
- **Why:** L6: artifacts must say which revision produced them; full-project
  stamping resolves the filtered-output paradox; naming the exempt classes
  prevents unclassified stamp-disagreement sources.
- **Verify:** Rebuild-without-change stays green; a schema edit flips every
  stampable output exactly once; doc stamped; .sqlsplit byte-stable and
  Decode-able; json envelope carries revision; filtered outputs carry the
  full-project stamp.

### 4.3 Breaking-change packaging (in the single final release)
- **What:** Breaking-typed changelog entries: per-language branding, header
  wording, revision stamps, generate --idempotent RAISE-on-mismatch (5.7),
  constraints-validator re-targeting, Java one-type-per-file layout, gorm
  branded fields, sqlalchemy sa.Enum columns. Consumer todos filed at THE
  release with regeneration + adaptation notes (Python raw-string
  construction -> parse()/members; Go string-literal comparisons -> branded
  type; TS parse() at boundaries; JPA converters; Java layout).
- **Why:** All consumer-visible changes, one break, one adaptation, honest
  handoff (boundary item 10).
- **Verify:** rlsbl changelog coverage passes with breaking entries;
  consumer todos filed; consumer drift-checks pass after regeneration.

## Phase 5 — The database functor (migrate)

The chain category (kernel 1.4) instantiated on disk, and apply made a real
functor with a recoverable trace. Design gate first; land order strictly as
numbered; nothing ships mid-phase (single-release axiom).

### 5.0 Schema and format design (design gate)
- **What:** Complete designs before implementation: pgdesign_migration_ops
  (op identity: migration ref, phase, sequence, op kind, target; serialized
  down-op; intent/confirm status), pgdesign_applied_migrations (MUST carry a
  migration-level checksum column — serve selects it),
  pgdesign_chain_position (current revision, in-progress edge ref,
  per-database boundary), migration file format (sequence+slug; from/to
  revision; ops referencing store objects by id), chain-edge file format
  (migrations/chain/<seq>-<from>-<to>.json — edge identity is the triple),
  store roots (migrations/objects/, migrations/revisions/ — manifests as
  name-sorted maps), archive layout (migrations/archive/). The two
  divergent tracking write paths (state.go helpers vs inline SQL) reconcile
  onto one. Labeled honestly: a human design gate with one mechanical check.
- **Why:** 5.2 migrates rows INTO these schemas; designs precede the
  implementation order (planning discipline).
- **Verify:** Design fixtures round-trip through the 1.1 encoder; schema
  DDL fixtures reviewed before 5.1 starts.

### 5.1 Self-contained ops via the store — L1+L2
- **What:** Every pointer-def op REFERENCES its target object + the
  transitive composition-closure of type definitions BY CONTENT ID
  (objstore). All THIRTEEN families: nine pointer-def + RawSQL +
  PartitionChildSpec + ParentTable + the 0.6-introduced partman-config ops.
  DOWN-ops identical treatment. Comment-stub no-ops and wrong-object
  fallbacks (deny-mutation / append-only) DELETED; sequences keep
  parameters; opCreateTable passes op.PGVersion (hardcoded 0 today) and
  resolves enum/domain qualification from the closure. Invertibility DECLARED
  per op kind (kernel 1.4's type). Table-driven round-trip test per family —
  up AND down — on a fixture with an enum column, a domain column, and a
  version-gated generated column, asserting rendered SQL equals generate's.
- **Why:** L1 totality for ops (a migration that renders wrong SQL — empty
  or the WRONG OBJECT — is the worst artifact possible; today actual for
  several families); L2 keeps ops thin, reviewable, deduplicated.
- **Verify:** Round-trip table test covers all thirteen families up and
  down; fallbacks gone; the mixed fixture renders byte-identically.

### 5.2 Chain on disk + `migrate upgrade` — L2+L5+L8
- **What:** Chain edges one-file-per-edge in migrations/chain/; revision
  manifests in migrations/revisions/; head/find-heads via kernel 1.4.
  Discovery/ordering rewritten off semver. Filenames: sequence +
  auto-derived slug (override flag). `migrate upgrade` (one-time, explicit):
  requires clean schema files per git when in a repo (stated caveat
  outside); acquires THE session-level advisory lock (shared with
  apply/rollback/baseline; concurrent-apply-during-upgrade is a verify
  case); content-addressed file writes land idempotently BEFORE the DB
  transaction; then ONE transaction: snapshot old applied set -> create
  journal/view/position -> migrate tracking rows -> ASSERT view reproduces
  the snapshot -> DROP old table -> COMMIT (sole commit point; the reverse
  window is harmless BY L2 idempotence, stated as a property; on-disk state
  reconciles from chain position on next run). Verify-then-stamp: clean
  TOML<->DB reconcile (kernel 1.2) or refusal with the drift report;
  per-database boundary stamped into chain_position. Multi-database rule:
  synthetic-prefix revisions are per-database stamps; shared prefix files
  are the union; databases at different boundaries are supported. Existing
  semver files become the linear prefix with synthetic checksum-verified
  revisions. serve updated (BEFORE phase 8's rework — the files are
  co-edited): handleMigrations repointed to the view; version endpoint
  updated for sequence+slug names. Store<->chain<->files consistency check
  (THE shared integrity checker; 6.2 and 7.2 invoke the same one).
- **Why:** L3 needs the chain physically; L8 dictates the choreography
  (assert-before-DROP; lock; idempotent-files-then-atomic-commit); L5's
  verify-then-stamp makes the boundary a verified fact, not an assertion.
- **Verify:** Crash injection both sides of COMMIT (boundary item 2);
  dirty-tree refusal; mid-edit TOML cannot stamp; drift report on unclean
  reconcile; consistency check red on tamper; concurrent apply blocked.

### 5.3 Squash = composition — L3+L4
- **What:** Consolidation = an ADDITIONAL chain edge; superseded files
  retire intact to migrations/archive/, reachable via their edges
  (mid-range databases apply remaining originals via chain_position edge
  selection). Consolidation downs: by manifest diff for fully-mechanically-
  invertible ranges; ranges containing declared-inverse/DML ops compose the
  originals' recorded downs (vacuous where declared so — L4's three-way
  type decides, no runtime judgment). The op-list optimizer is specified as
  a TERMINATING REWRITING SYSTEM: cancellation carries the side condition
  "no intervening op references the cancelled object" (today's greedy
  endpoint-only matching orphans dependent ops — a live bug); each rule
  strictly decreases a stated measure (termination); the ~12 rule types'
  critical pairs are enumerated and both resolutions tested to converge —
  termination + local confluence gives UNIQUE NORMAL FORMS (Newman), which
  is what makes consolidation well-defined. SQUASH-COMMUTATION (the L5/L10
  functor equation) is a named test: apply(consolidation) and
  apply(sequence) land on the same introspected schema-state. The
  rollback-equivalence invariant is STRUCTURAL (revision equality says
  nothing about data). Tracking/journal lineage handled; no orphaned rows;
  files never rewritten.
- **Why:** L3 makes squash a checked normalization, not a definition; L4
  makes the round-4 data-loss hole (a DOWN recreating a dropped column
  empty) unrepresentable; the rewriting-system spec replaces "we hope pass
  order doesn't matter" with a finite, decidable check — the one place a
  textbook result (Newman's lemma) changes what gets built.
- **Verify:** Squash of applied migrations via consolidation; mid-range DB
  resumes via archived originals; SQUASH-COMMUTATION on the comprehensive
  fixture (apply-composite vs apply-sequence, compared through reconcile);
  rollback-equivalence on structural AND merged-type-change fixtures; a
  DML-containing range takes the composed-downs form BY TYPE; critical-pair
  convergence suite green; the orphaned-index fixture (add/index/drop)
  refuses cancellation; no orphaned rows.

### 5.4 Unconditional checksums (apply surface) — L2
- **What:** After 5.2/5.3: checksum verification unconditional ON APPLY —
  including archived-original applies. Mismatch = corruption, hard error
  naming the file. Prefix files' synthetic revisions checksum-verified.
  (No rollback surface exists: post-5.6 rollback reads no files.)
- **Why:** Under L2, a mismatch has exactly one meaning; enforcing before
  the format existed would have bricked users (the adoption path is 5.2).
- **Verify:** Tamper tests on active and archived files refuse apply with
  precise reports; upgraded fixture applies cleanly.

### 5.5 The journal (apply's trace) — L5+L8
- **What:** pgdesign_migration_ops + pgdesign_applied_migrations per 5.0
  (one write path). Records op identity AND serialized down-op (via the
  store). TIMING: transactional ops journal INSIDE the op's transaction;
  non-transactional ops (create AND drop index concurrently;
  version-conditional enum-add) use INTENT-then-CONFIRM rows with
  class-specific resume protocols defined in Postgres's own state model:
  resume of an unconfirmed create-index intent checks pg_index.indisvalid
  (an interrupted CIC leaves an INVALID index that IF NOT EXISTS would
  skip forever) and drop-rebuilds; drop-index gains IF EXISTS; enum-add is
  already idempotent. (Fix sql.go's wrong CIC+IF NOT EXISTS version
  comment.) The same protocols govern rollback of non-transactional
  down-ops. chain_position updates in the same transaction as each
  edge-completing journal write. Re-apply resumes by skipping confirmed
  ops. AppliedVersions/status/serve read the view.
- **Why:** L5: the trace is what makes retries a resume instead of a
  gamble (today the version row is written last and partial state is
  unrecorded — the original abort-loop bug). L8: the trace write itself
  has a crash window one level down; intent/confirm + world-model
  idempotence closes it, including the hole INSIDE the recovery protocol.
- **Verify:** Fault-injection matrix (boundary item 1): mid-phase; after
  CIC (resumed index is VALID); after drop-CIC; around enum-add on both
  PG classes; view semantics equal the old applied-set; single write path
  by grep.

### 5.6 Journal-driven rollback (scoped) — L5+L4
- **What:** Rollback executes recorded down-ops in reverse journal order —
  files never consulted. MID-EDGE semantics: when chain_position shows an
  in-progress edge, reverse confirmed ops (class-specific protocols for
  unconfirmed non-transactional intents); the reversibility pre-check runs
  against JOURNALED ops, not file ops. SCOPE: guaranteed from the upgrade
  boundary forward; pre-upgrade prefix + baselines ROLLBACK-FROZEN
  (crossing = hard error naming the boundary).
- **Why:** L5: rollback inverts recorded reality, never assumed intent
  (today it trusts files absolutely — the DROP-COLUMN data-loss case).
  L4: only journaled invertible ops have inverses to run.
- **Verify:** Rollback after partial apply drops nothing it didn't create;
  works with files archived; mid-edge rollback incl. an unconfirmed CIC
  intent; boundary-crossing refuses; journal-based pre-check tested.

### 5.7 Preconditions + predicate IR — L5+L1
- **What:** Per-op-class predicates against pg_catalog (absent for
  creates; present-and-matching via ≈ for alters/drops); unexpected state
  = hard error naming object/expected/found. DML ops precondition-free
  (arbitrary SQL has no catalog precondition). IR = structured data in
  internal/predicate; the Go executor SHARES introspect's catalog-query
  layer (extracted — introspect already contains every needed query); only
  the pgx executor lives in migrate; the SQL renderer compiles the same
  structures into DO-blocks for generate --idempotent (RAISE on mismatch —
  4.3's breaking notes). CI conformance matrix: both backends + the differ
  where classes overlap, against live states, identical verdicts.
- **Why:** L5's domain check, computed with L1's single ≈. The SQL
  renderer is boundary item 3 (a second computation of ≈ in another
  language); the matrix is its law-check. The Go executor exists for
  structured diagnostics — this was challenged and REJECTED.
- **Verify:** DB-backed matrix per op class; golden idempotent SQL;
  mismatch RAISEs, match no-ops; conformance green; shared catalog layer
  by import graph.

### 5.8 Post-apply reconcile — L5
- **What:** After apply: introspect (0.4 exclusions; canonical via 0.2) +
  ≈-normalized diff against the target model; residual mismatch = hard
  error listing every object. Reconcile does not auto-add imported
  schemas. SM-vs-enum introspection lossiness documented. Asserts
  revision-equal-implies-diff-empty on the comprehensive fixture; the
  managed-objects introspect-cleanliness assertion (deferred from 0.4)
  lives here.
- **Why:** L5's codomain check: preconditions verify each morphism step
  locally; reconcile verifies the functor landed on R_to globally,
  reusing the real differ for complete coverage.
- **Verify:** Clean apply over the comprehensive fixture reports empty;
  out-of-band ALTER mid-migration surfaces; managed objects invisible; the
  L10 ROUND-TRIP as a randomized DB-backed property test — generate random
  model pairs (reusing the seed package's type-aware generation machinery
  for schema generation), diff, apply, reconcile-empty — the single
  highest-value DB-backed property test available to the plan.

### 5.9 Pure generation — L5
- **What:** migrate generate = diff(deserialize(head manifest via
  objstore), current model) — pure, no DB. ALWAYS emits large-table-safe
  forms (NOT VALID + VALIDATE; backfill-then-set-not-null;
  expand/contract phasing); QueryTableStats and generate-path stats
  plumbing deleted; the EXPAND_CONTRACT_TYPE_NARROW advisory RELOCATED to
  diff classification (the one user-visible behavior that would silently
  vanish). Drift caught at apply; adoption via baseline (which writes
  chain_position + a revision manifest).
- **Why:** L5: generation never reads the world — same TOML edit, same
  migration, regardless of DB state; the always-safe form is what makes
  that possible (a manifest has no row counts).
- **Verify:** Generation without any DB; FK add emits two-step NOT VALID
  with no DB; a drifted DB does not alter output but fails apply; stats
  plumbing gone; the advisory still appears; diff MINIMALITY as a quality
  property (mutation test: delete any generated op, reconcile must fail —
  non-normative, L10's corollary).

### 5.10 Fork resolution + ecosystem alignment — L3
- **What:** `migrate rebase <head>`: re-parents a fork's tail, recomputes
  revisions, re-derives manifests (per-edge files make forks semantic,
  not textual — boundary item 6). Rebased-away edges RETIRE to
  migrations/archive/ exactly like superseded squash originals — rebase
  never rewrites or deletes history (L2's spirit; the consistency checker
  treats them as reachable history). Baseline's semver guards re-expressed
  against chain reachability. Shadow test, squash CLI, docs updated;
  migration-guide rewritten.
- **Why:** Two branches each appending an edge is normal; detection
  without resolution is a dead end; baseline references a version scheme
  that no longer exists.
- **Verify:** Fork fixture: rebase re-parents, revisions recomputed,
  store consistent; rebased-away edges present in archive and reachable
  via the checker; baseline guards fire on chain-unreachable states;
  shadow test passes; full migrate suite green.

## Phase 6 — Orchestration and provenance enforcement

### 6.1 pgdesign revise — L5+L6
- **What:** PURE tier: build planner + 5.9 generation + PURE checks —
  static NF audit (pure; --strict-nf blocks today and must not regress)
  and structural workload — all BLOCKING. DB tier (phase-2 env): live
  import verification (7.4) + LIVE analyses (TANE, pg_stat) —
  NON-RETROACTIVE (fail loudly; the committed migration stands; the next
  revise incorporates fixes). Chain head from the chain files; two heads
  = hard error naming both + pointing at migrate rebase; genesis handled.
  Separate safegit commits (pure outputs; then migration+chain+store) via
  ONE shared commit helper; commit failure = hard error — build's
  warn-and-continue flipped in the same pass. Partial failure keeps
  committed pure outputs, exits non-zero naming the skipped tier.
- **Why:** The forgotten-step failure mode dies in one command without
  eroding the seam: with L5's pure generation, even the migration is pure,
  so the DB tier is exactly the genuinely-live work. Commit-before-DB-tier
  is sound: the migration is repo-level and pure; per-database
  applicability is re-checked fail-closed at apply.
- **Verify:** End-to-end: edit -> revise -> outputs + chained migration +
  two commits, one revision everywhere; BCNF violation with strict-nf
  blocks the pure tier; DB-unreachable keeps pure outputs, non-zero,
  names the skipped tier; two-head fixture points at rebase;
  commit-failure hard-errors.

### 6.2 Revision enforcement — L6
- **What:** Invariant (derived, not legislated): all regenerable
  planner-set artifacts carry the ONE full-project revision after any
  write. Writer taxonomy: FULL regenerators (build, revise) always
  allowed; PARTIAL writers — exactly one exists (codegen --output) —
  refuse when non-rewritten siblings differ, and the taxonomy PRE-COMMITS
  that any future file-writing generate mode registers as full-or-banned;
  SOURCE-EDITING writers (fmt, introspect --output) are outside the
  invariant but CHANGE the revision — they print a follow-up notice and
  the check catches staleness. The partial-writer refusal and the
  revision check regenerate through the SAME per-output filters from
  [output] config (0.5's unification). Outside the invariant, stated:
  migrations + chain + store (append-only; covered by INVOKING 5.2's
  consistency checker), seed output (stamped, unenforced provenance),
  stdout (check-time only). Missing/old-format stamps = stale. The
  revision CHECK (error severity): chain/store integrity via the shared
  checker, cross-artifact stamp agreement, standalone artifacts. genkit
  stamp-extractor complements byte-compare (stamp says "a sibling is at a
  different revision"; bytes say "this file isn't what the model
  produces").
- **Why:** L6: divergence is created by partial writes and source edits,
  resolved by full ones; every writer class's obligations derive from
  provenance totality; one integrity checker, invoked twice.
- **Verify:** TOML edit then build succeeds; then codegen --output of one
  output refuses naming stale siblings (group-filtered fixture); fmt
  prints the notice and the check goes stale; tampered header caught;
  chain violation caught via the shared checker; seed/migrations/stdout
  never flagged.

## Phase 7 — Cross-repository algebra (imports)

### 7.1 Declaration and reference syntax
- **What:** [imports] parsing (alias -> git URL + ref + target PG
  schema); `alias:table` ONLY in FK ref_table; alias resolution BEFORE
  dot-split; aliases elsewhere = hard error naming supported sites.
  Diagnostics: unknown alias, unresolvable target, collisions.
- **Why:** References name the DEPENDENCY; a typo'd alias is a resolution
  error, not a phantom schema.
- **Verify:** Parse/build tests; typo -> resolution error; precedence
  test; alias-in-depends_on -> scoping error.

### 7.2 Surface snapshot and pinning — L1+L2 across repos
- **What:** `import lock`: resolve the pin (git URL + ref; git plumbing;
  no DB), parse the framework's TOML, vendor the surface into
  imports/<alias>/ via THE SAME objstore package (one package, multiple
  roots): referenced tables + transitive composition-closure of type
  definitions, each with its per-object id, plus a lockfile entry (URL,
  ref, resolved commit, surface hash). `import update` re-pins.
  `check --tag imports`: re-derive and report SEMANTIC drift at column
  level ("framework column X changed uuid->bigint, breaks
  app.users.principal_id"), hard-failing CI — built on the same
  store-integrity primitive as 5.2's checker. Requirements: extensions
  inferred per referenced object; pg_version floor carried (consumer
  re-declares >=).
- **Why:** The import surface IS a sub-model under L1's encoder and L2's
  store — reproducible offline builds and column-level semantic drift
  fall out of the kernel rather than needing their own machinery.
- **Verify:** Two-project fixture: drifted column type -> exact
  column+FK error; unreferenced changes silent; offline build;
  per-object ids stable; enum closure usable.

### 7.3 Model integration — fail-closed union
- **What:** ImportedTables split slice. Union wired at the COMPLETE
  enumerated resolution sites: buildTablesByName (E204/TableByName —
  without it FK validation, migrate FK qualification, and check C104
  break), BuildFKGraph (edges keyed (schema,name), Imported=true), seed
  FQN pools, AND the D2/GraphQL edge emitters (they emit edges by
  target-name string; D2 also drops fk.RefSchema — fixed here). Registry
  collisions = hard error naming both sources; imported enums usable;
  extension/pg_version re-declaration enforced.
- **Why:** Fail-closed by construction — consumers iterating Tables are
  correct BY OMISSION — but only where resolution funnels through the
  union; the four bypass sites are named because each otherwise produces
  spurious errors, phantom nodes, dangling seeds, or dangling edges.
- **Verify:** NO spurious E204 on imported FKs (explicit test); FKGraph
  nodes keyed and flagged; seed resolves imported pools; D2 edges
  schema-qualified; DDL/audit/codegen outputs contain zero imported
  artifacts; collision and re-declaration tests.

### 7.4 Downstream sweep
- **What:** App-only DDL with schema-qualified FKs. Diff/migrate exclude
  imported tables; reconcile does not auto-add imported schemas. Live
  import verification consumes the 5.7 predicate executor (phase-2 env).
  Audit, design checks, orphan warnings, codegen skip imported tables.
  Seed tiers in FK-value resolution — the existing silent-UUID
  pool-empty fallback made UNREACHABLE for imported FKs (routed
  exclusively through tiers; fallback hard-errors): tier 1 (DB):
  real-key pools, deterministic sorted selection, Zipf + COPY unchanged;
  tier 2 (offline): count-wrapped ordered-offset subqueries in INSERT
  mode, Zipf dropped (stated); tier-2 hard error scoped to UNIQUE
  constraints where the imported FK is the sole distinguishing column
  (composite UNIQUEs with an offline-distinct local column are fine);
  the fixed-rowIdx silent fall-through in the dedup retry fixed
  alongside; tier 3: hard error offline+COPY+NOT-NULL naming all three
  constraints. D2/GraphQL render imported tables as minimal reference
  shapes (a first-class shape class phase 9 preserves).
- **Why:** Imported rows are facts — never regenerated, audited, or
  fabricated; the error surface bans exactly the impossible and the
  silently-wrong (L5's spirit applied to test data).
- **Verify:** Per-package fixtures; live verification via the executor;
  seed tier tests incl. determinism, offset wrap, the rescoped UNIQUE
  error (composite-UNIQUE fixture passes), fallback unreachability, the
  triple-constraint error; D2 golden compiles.

## Phase 8 — Kernel exposure over HTTP (serve)

### 8.1 DB-free serve mode
- **What:** Pool optional; --db optional in project-schema mode. The
  shared project loader lands in internal/project — (schema, registry,
  cfg) — used by build/codegen/revise/serve (serve's registry-discarding
  loader dies). ORDERING: 5.2's serve edits land first; 8.1 routes them
  through internal/project (co-edited file — NOT parallel with 5).
  Schema endpoint = THE canonical envelope function (kernel 1.5):
  revision + FKGraph projection (0.3) + diagnostics wrapped.
  Nil-registry SM-drop fixed. DB-only endpoints degrade explicitly.
- **Why:** The compiler/live seam made real; the endpoint is literally
  the same function as the json output, so it can never drift (L1).
- **Verify:** serve starts without a database and answers
  (byte-consistent with generate json incl. diagnostics); SM diagrams
  render; DB-only endpoints degrade explicitly.

### 8.2 API hygiene
- **What:** --timeout becomes request-context enforcement; audit becomes
  job-start/poll (cancellable); doc endpoint added.
- **Why:** A dead flag is a lie in the CLI surface; an unbounded
  synchronous endpoint is a self-DoS button.
- **Verify:** Slow-audit observes timeout/cancel; doc endpoint matches
  generate's doc.

## Phase 9 — Presentation (explicitly outside the algebra)

Diagram work gains nothing from the formalism; it is deliberately plain.
Only its INPUTS are law-governed (canonical model, finalized graph API,
reference shapes).

### 9.1 Options plumbing (split dependency)
- **What:** D2 options from config (after phase 0); serve query-param
  plumbing (after phase 8). RenderSVG parameterized: dagre/elk (TALA
  excluded — not in the OSS library), theme, direction.
- **Verify:** Config round-trip; elk golden; serve params post-8.

### 9.2 Enrichment
- **What:** Conditional-generation layers: index/unique markers, nullable
  indicator, comments as tooltips, checks as notes, RLS/append-only
  markers, enums as rectangles with values. Imported reference shapes
  preserved. The column/table presentation logic FACTORED into a shared
  helper consumed by BOTH doc.go and d2.go (doc already derives all of
  this — no second derivation).
- **Verify:** Golden per layer; independently disableable; goldens
  compile; reference shapes survive all layer combinations; one shared
  helper.

### 9.3 Filtering
- **What:** Include/exclude globs; include-dependencies depth via 0.3's
  depth-bounded walker; summary mode; edges to excluded tables skipped;
  self-FKs preserved; filtered schemas canonical via 0.2.
- **Verify:** Goldens per mode; filtered output compiles; depth semantics
  match the walker.

### 9.4 Cardinality
- **What:** Edge blocks with native crow's-foot arrowheads; 1:1 via
  unique/PK detection, 1:N default, M:N strict junction heuristic
  (exactly two FKs = whole PK, no other columns).
- **Verify:** Golden per class; junction-with-extra-column NOT collapsed.

### 9.5 Heat maps and live stats
- **What:** Fan-in/out on a fixed colorblind-safe stroke scale; live
  annotations as caller-provided data; generate stays DB-free (L5).
- **Verify:** Goldens; injected-stats test; no DB import in generate.

## Phase 10 — Deferred horizon

The interactive frontend on the phase-8 contract. Unplanned by design.

---

## Dependency DAG

- Phase 0 internal: 0.1 -> 0.2 -> 0.3; 0.4/0.5 after 0.2; 0.6 anytime
  (0.6's partman op family must exist before 5.1 absorbs it: 0.6 -> 5.1).
  strictcli todo filed at phase-0 start (boundary item 11; phase 2 is an
  EXTERNAL milestone gating {6.1, 7.4, seed tier-1}).
- 0 -> {1, 2, 9.1-config-half}; 0.1 -> 4.1; {0.1, 1.4, 1.5} -> 4.2;
  4.0 precedes 4.1's verify (Java fixes land WITH the javac check);
  4.1+4.2 -> 4.3; 4.2 -> 6.2.
- Kernel: 1.1 -> {1.4, 1.5, 5.1, 7.2}; 1.2 -> {1.4-conformance, 5.2,
  5.7, 5.8}; 1.3 -> {5.1, 5.2, 7.2}; 1.4 -> {5.2, 5.3, 5.9, 6.1};
  1.5 -> {4.2-json, 8.1}.
- 0.3 -> {7.3, 8.1-projection, 9.3}; 5 internal: 5.0 -> 5.1 -> 5.2 ->
  5.3 -> 5.4 -> 5.5 -> 5.6 -> 5.7 -> 5.8 -> 5.9 -> 5.10.
  {5, 0.5, 1, 4.2} -> 6; 5.7 -> 7.4; 7.4 -> 9.2; 5.2-serve-edits -> 8.1
  (co-edited file — phases 5 and 8 NOT parallel); 8 -> 9.1-serve-half.
- Parallelizable after phase 1: 4.1 (already after 0.1), 5, 7 (through
  7.2). Phase 8 follows 5.2's serve edits.

## Relationship to existing todos

- infra-env-db-locator.md — superseded by phase 2.
- migrate-add-column-missing-if-not-exists.md — superseded by phase 5.
- genericize-diff-library.md — resolved by 0.6(a).
- partition-lifecycle-and-diff-library.md — Part 1 = 0.6(b,c); Part 2 =
  the recorded promotion trigger.
- cross-framework-schema-composition.md — core = phase 7.
- orxtra-codegen-deferred-remaining.md — item 17 via phase 4 + DB CHECK;
  item 18 = kernel + phase 6; item 20 = phase 6; item 19 dropped; items
  21/22 out of scope.
- visualization-and-web-ui.md — its phases 1-5 = phase 9; web UI = 8/10.
- rename-to-strictpg.md — in todo/.obsolete/ per the no-rename axiom.

## Out of scope, pending their own design rounds

Test schema mode. N-project topology. Manifest + per-language linter
ecosystem (evidence-gated). Recorded summit alternatives: declarative
catalog reconciliation for migrate (the kernel's L5 machinery is its
stepping stone); structural semantics/metadata split in the model (the
encoder's semantic-only policy already produces its bytes); registry
materialization into Schema as sole type-truth; extension-DDL-name
resolution baked into the model; DB/boot-time revision binding; the
reverse conformance invariant as primary (activated once the differ fully
adopts N); LIVE ROUND-TRIP NORMALIZATION as the exactness alternative to
the ≈_pg rewrite-rule set (round-trip desired-side expressions through the
DB on live paths — exact where the rule set is documented-incomplete);
THREE-WAY MODEL MERGE (pushout over a common-ancestor revision — per-object
join with change/change conflicts detected by id inequality against base;
the kernel makes it nearly free) as the recorded alternative to
rebase-only fork resolution.

## Effort

Phase 0: 2-3 sessions. Phase 1 (kernel): 2-3 sessions — pure Go,
property-tested, no DB; front-loaded because everything else adapts it.
Phase 2: 1-2 (externally gated). Phase 4: 3-4 (incl. 4.0's two
deliverables). Phase 5: 4-6 (thinned — the chain/invertibility/store
machinery moved into the kernel). Phase 6: 1-2. Phase 7: 3-4. Phase 8:
1 (after 5.2's serve edits). Phase 9: 2-3. Parallelization per the DAG.

Release: exactly ONE rlsbl release at the very end (owner axiom);
everything accumulates unreleased; consumer todos filed at that release.
No intermediate state can reach a consumer.
