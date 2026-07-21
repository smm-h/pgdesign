# pgdesign roadmap: the kernel and its boundary

Consolidated implementation plan: determinism, schema identity, migrate
integrity, orchestration, imports, API, visualization. This file is exempted
from todo immutability by explicit owner authorization — it is a living plan.
Fully self-contained.

The design rests on a small algebra — a content-addressed object model wrapped
around a category of schema migrations, governed by one equivalence relation.
Every defect class this plan guards against is a violation of one of the laws
below. The laws come first and the plan derives from them, so the work
eliminates defect classes by construction rather than case-by-case, and any
future proposal is judged the same way: which law does it implement, check, or
violate?

---

# Part I — The algebra

## Objects and primitives

- **Model**: the fully-resolved schema IR (tables, views, matviews, functions,
  sequences, types, extensions, pg_version, comments, groups — everything that
  determines generated artifacts, DDL and beyond).
- **enc**: the canonical per-object encoder, Model-object -> canonical bytes;
  **decode** is its inverse on canonicalized models (decode∘enc = id is a
  checked property — 5.9 and 7.2 deserialize, so decodability is load-bearing).
  Every encoded form carries a CODEC-VERSION (epoch) field — ids are
  epoch-relative (see L2).
- **N**: the normalizer (types, defaults, expressions via parse/deparse, PLUS
  the catalog-independent foldings both directions — IN <-> = ANY(ARRAY[...]),
  array-literal forms — applied to BOTH sides always); **≈_syn** := the kernel
  of N (a ≈_syn b iff N(a) = N(b)) — an equivalence relation BY CONSTRUCTION.
  **≈_pg** := Postgres's semantic equality — a distinct, richer relation we do
  not compute (see L1).
- **hash**: SHA-256; **id** = hash(enc(x)); **revision** = id of a whole-model
  manifest — a SORTED MAP of KIND-QUALIFIED KEYS (kind, schema, name, and for
  functions the argument signature — overloads are distinct entries; a table
  `x` and a function `x` never collide) -> object-id. Renames are therefore
  delete+add by construction — a DOCUMENTED limitation (diff emits DROP+CREATE;
  for tables/columns that is data-destroying; rename-hint ops are a recorded
  alternative). Store + manifests form a two-level Merkle DAG: manifest
  comparison is key-wise symmetric difference; the shared consistency checker
  IS Merkle closure verification (every id in every reachable manifest
  resolves in the store) PLUS edge-endpoint consistency (an edge's ops,
  simulated, map its from-manifest to its to-manifest); diff gains an
  O(changed-objects) fast path by comparing per-object ids before deep
  comparison.
- **store**: content-addressed map id -> bytes (put/get; puts idempotent).
- **chain**: parent-linked edges between revisions; an edge is a **migration**
  whose identity is CONTENT-DERIVED (its file is named by an edge-content hash
  prefix plus a human slug; a display SEQUENCE is derived from topology for
  listings, never stored as identity) — parallel edges and endomorphisms
  (pure-DML migrations, R -> R) never collide, and concurrent branches cannot
  race a counter.
- **diff**: (Model, Model) -> Delta. A Delta is a flat description of change,
  NOT a morphism: Deltas do not compose or invert; all composition happens on
  op-lists. diff's specification is L10; diff(a,a) = empty is a pinned test.
- **gen**: Delta -> op-list, the lowering whose contract IS L10 (a primitive,
  since the round-trip theorem specifies it jointly with diff); ops carry
  their L4 invertibility class and reference objects by content id.
- **apply**: the map from chain edges into the world (codomain defined in L5).
- **journal**: the durable trace of apply's actions, with recorded inverses.
- **stamp**: artifact -> revision-that-produced-it (provenance).
- **modelgen**: a pure random generator of valid Models (well-formed FK
  graphs, type closures, version-gated features) — the input source L9's
  property tests and L10's round-trip test require (kernel 1.6; the seed
  package generates row DATA and cannot serve this role).

## The laws (L1-L10)

- **L1 (One canonical form — with honest status tags).** N is the normalizer;
  ≈_syn is its kernel, an equivalence by construction. (a) enc encodes
  N-normal forms, so enc(a) = enc(b) iff a ≈_syn b. STATUS: structural-only
  until N lands — 1.1 delivers the structural sublanguage with expression
  leaves opaque; expressions enter normal form when Canonicalize begins
  N-normalizing expression fields into the IR (0.2's extension point,
  activated with 1.2) — full L1(a) holds from then on. (b) Single-≈: every
  comparison engine (encoder, differ, predicates) computes ≈_syn — enforced
  progressively via the conformance pair: revision-equal implies diff-empty
  (initial gate); diff-empty implies revision-equal (end-state; its
  obligations include pg_version joining SchemaDiff, since under-reporting
  breaks precisely this direction). (c) Boundary conjecture: ≈_syn ⊆ ≈_pg,
  KNOWN INCOMPLETE. The catalog-independent part of PG's rewriting lives
  INSIDE N (both sides — one-sided rewriting would false-drift a user who
  writes = ANY(ARRAY[...]) directly). The residue is CATALOG-DEPENDENT cast
  materialization, unreachable by any pure normalizer; it is bridged on live
  paths by forward-simulation rules on introspected text (fixture-checked,
  incompleteness documented; the exactness alternative — live round-trip
  normalization through the DB — is recorded in the alternatives list).
  Structural sublanguage: the ORDER-SEMANTICS TABLE (exhaustive over the
  Model, two columns per collection: collection order and intra-object order
  — columns and enum values semantic; composite-type fields, function args,
  partition key columns, FK column correspondence, and index key-column
  order semantic INTRA-object; checks, indexes, uniques, policies, triggers
  canonical-only as collections) is part of the format spec; that table IS
  the definition of ≈_syn on structure.
- **L2 (Content identity / extensionality).** id = hash∘enc (id equality
  implies content equality MODULO SHA-256 collision resistance — a stated
  assumption, since id-equality fast paths skip byte comparison; boundary
  item 14); get(put(x)) = x; puts idempotent; identity location-free;
  decode∘enc = id on canonicalized models. Content ids are EPOCH-RELATIVE:
  every stored form carries its codec version, and a change to enc or N
  re-keys the world — recovered by `migrate rekey`, the ONE sanctioned
  rewrite, whose mutation scope is TOTAL: store objects, manifests, AND
  chain-edge files all re-encode under the new epoch (a partial re-key would
  break Merkle closure — edges would reference old-epoch ids). rekey and
  rebase write into ONE unified revision-remap table in the chain, consulted
  by both apply and the consistency checker, so a database whose
  chain_position holds a pre-rewrite revision (rekeyed OR rebased-away) is
  served forward, never orphaned. Outside an epoch bump, mutation of STORE
  CONTENT (objects, manifests) is not an operation this structure has.
  Chain-edge FILES are location-addressed — their append-onlyness is CHECKED
  POLICY (the consistency checker, incl. its edge-endpoint check), not
  structural impossibility.
- **L3 (The chain is the free category on the edge graph).** Composition =
  path concatenation; identities = empty paths — VIRTUAL: never files, never
  applied (these laws hold trivially and are not what needs testing). The
  real content: (a) edge identity is content-derived — the hom-set question
  is answered explicitly, parallel edges and pure-DML endomorphisms are
  legal; (b) SQUASH SOUNDNESS — a consolidation edge is a NEW edge whose ops
  must be apply-equivalent to the path it supersedes: a CHECKED property (the
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
  property-based tests over GENERATED inputs (modelgen, kernel 1.6 — the
  generator is a deliverable, not an assumption): encoder totality (the
  reflection coverage guard, extended to the registry snapshot); decode∘enc =
  id; normalizer idempotence N∘N = N over a generated expression corpus;
  SHUFFLED-DECLARATION-ORDER convergence (≈_syn-equal inputs — permuted
  canonical-only collections — encode to identical revisions: canonicality,
  not mere repeatability); semantic-order collections are SLICES, never
  sorted maps (a semantic order accidentally modeled as a map would be
  silently destroyed by key-sorting); squash-rewriting confluence (finite
  critical-pair enumeration + a termination measure, hence unique normal
  forms by Newman's lemma); the L10 round-trip; and a GOLDEN CORPUS of
  normalized expressions committed so that a go-pgquery dependency bump that
  shifts ≈_syn — which under L1 shifts IDENTITY — turns CI red (recovery:
  `migrate rekey`, per L2). Example fixtures are for the boundary, where laws
  end.
- **L10 (Round-trip — the central theorem).** For models a, b: applying
  gen(diff(a, b)) to a world at revision(a) lands it at revision(b) —
  gen is a section of apply-then-introspect up to ≈_syn. This is THE
  specification of diff and generate; preconditions, reconcile, and pure
  generation are scaffolding around this one equation. SOUNDNESS CAVEAT,
  stated: certification-by-reconcile additionally requires (i) introspection
  injective up to ≈_syn on the states exercised (it is NOT globally — SM
  types introspect as plain enums), and (ii) bridge completeness on the
  expressions exercised (the bridge is documented-incomplete). Therefore the
  randomized test (modelgen pairs -> diff -> apply -> verify) splits its
  oracles by soundness domain: the MANIFEST oracle (recorded to-revision
  manifest, compared object-by-object — not lossy) runs over the UNRESTRICTED
  generator, giving SM types randomized coverage; only the RE-INTROSPECTION
  oracle restricts to the injective, bridge-proven fragment. Corollaries:
  diff(a,a) = empty (pinned);
  squash-commutation; diff MINIMALITY as a non-normative quality property
  (mutation-tested: delete any op, the oracle must fail).

## The boundary doctrine

The system is a THREE-WAY partition, and defects are triaged accordingly:

1. **The kernel** — law-governed. Every law names its property tests (L9), so
   "a law was implemented wrong" is a CHECKABLE claim against a stated
   property, never a rhetorical escape hatch. Defects here are implementation
   errors; the fix is in the kernel and the property suite gains the case.
2. **The enumerated boundary** (Part IV) — everything we do not control:
   Postgres's runtime semantics and crash timing, the filesystem, git's merge
   behavior, six consumer languages, consumer code, the parser dependency.
   Defect classes here cannot be made unrepresentable — only checked, by
   fault injection, conformance matrices, and compile checks.
3. **Plain engineering outside the algebra** — phase 9's presentation work,
   CLI ergonomics, doc wording, seed statistical quality. Ordinary bugs, no
   doctrinal claim; forcing the formalism onto them would be ceremony.

Boundary membership is BIDIRECTIONAL: the list may grow only with a
post-mortem containing a POSITIVE impossibility argument (why no pure
property test could catch the class — not merely "we didn't derive it"), and
a boundary item that becomes property-checkable is DEMOTED into the kernel.
(A closed list would be unfalsifiable in one direction; a grow-only list is
unfalsifiable in the other.)

---

# Part II — Decisions as derivations

Provenance convention: `[deliberate]` = the owner's own axioms (fixed).
`[law]` = a consequence of Part I; reversing one requires rejecting a law, not
just changing a preference. `[%%]` = genuinely free choices (names, layouts,
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
  CHAIN-EDGE FILES — checked policy via the consistency checker's closure AND
  edge-endpoint checks (they are location-addressed, so extensionality cannot
  cover them). Archived originals; unconditional checksums on the apply
  surface — L2. (Checksums exist ONLY on the apply surface: post-5.6,
  journal-driven rollback reads no files, so no rollback checksum surface
  exists.)
- Content ids are EPOCH-RELATIVE; an enc/N change re-keys the world; recovery
  is `migrate rekey` with TOTAL mutation scope (store objects + manifests +
  chain-edge files re-encode under the new epoch; a recorded remap) — the ONE
  sanctioned exception to append-onlyness — L2+L9. Every stored form carries
  its codec version so the store is self-describing enough to drive the
  re-key. The rekey remap and the rebase remap are ONE mechanism: a unified
  revision-remap table in the chain, consulted by apply and the consistency
  checker alike.
- Squash = a consolidation edge (composition), never a rewrite — L3.
- Consolidation downs derived by manifest diff ONLY for fully-mechanically-
  invertible ranges; ranges containing declared-inverse ops (incl. vacuous
  DML inverses) or RawSQL compose the originals' recorded downs — L4. And
  consolidations PRESERVE every DML/RawSQL op of the superseded path (no
  drop, no fold-across — checkable on op-lists), because commutation is
  schema-only and cannot certify data equivalence for fresh databases — L5's
  codomain choice made explicit on the UP direction.
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
  the store; no lossy structured mirrors (degraded ops unrepresentable);
  RawSQL/DML bodies — opaque SQL by nature — are stored as content-addressed
  opaque blobs (which satisfies by-content-id; "no inline blobs" scopes to
  STRUCTURED op payloads) — L1+L2.
- Revision = hash of canonical bytes; every artifact stamped with a producing
  revision; enforcement taxonomy (SIX writer classes, enumerated totally:
  full regenerators / partial writers / source editors / scaffolding writers /
  stamped-unenforced writers (seed) / append-only stores (checker-covered))
  derived from provenance totality — L6. (The stamp's SCOPE — full-project rather than per-output — is NOT
  law-derived; L6 is satisfied by either; the full-project choice is
  engineering, justified by the filtered-output paradox — see the [%%]
  section.)
- Opaque Revision type; registry-absent marker INSIDE the hashed bytes;
  cross-class comparison errors — L7.
- Intent/confirm journaling for non-transactional ops, with resume protocols
  defined against Postgres's state model (pg_index.indisvalid for interrupted
  CREATE INDEX CONCURRENTLY; IF EXISTS added to DROP INDEX CONCURRENTLY) — L8.
- One normalization primitive consumed by differ, predicates, upgrade
  reconcile, and shadow test; predicate IR = one structured definition with a
  Go executor (structured diagnostics; shares a catalog-query layer with
  introspect, and gates version-conditional queries through the EXISTING
  internal/pgcap capability registry — one version->capability truth) and a
  SQL renderer, conformance-matrixed — L1. (The Go executor
  exists for structured object/expected/found diagnostics, not DB-freedom —
  dropping it in favor of SQL-only evaluation is ruled out; the matrix exists
  because the SQL renderer is a second computation of ≈_syn in another
  language — an irreducible boundary item.)
- One canonical serializer everywhere (generate json = serve payload = import
  surface = op bodies = revision manifests); the encoder is a dedicated
  canonical encoder with reflection-based field-coverage guards over BOTH the
  model structs and the registry snapshot (totality is mechanically checked,
  not hoped) — L1+L9.
- verify-then-stamp `migrate upgrade`: single DB transaction (lock; snapshot
  applied set; build journal/view/position; ASSERT view reproduces snapshot;
  DROP old table; COMMIT), content-addressed file writes idempotent and
  BEFORE the commit (the reverse window is harmless by L2's idempotence) —
  L5+L8+L2.
- Compiler/live seam (build and generation pure; DB work in a distinct tier);
  live-only analyses (FD discovery, pg_stat) are DB-tier and non-retroactive —
  L5. (That pure analyses BLOCK in revise's pure tier is NOT an L5 theorem; it
  is a policy derived from the owner's hard-constraints philosophy — see the
  [%%] section.)
- Fail-closed imports: owned tables in Tables, imported in ImportedTables;
  every consumer iterating Tables is correct by omission; the union is wired
  at the ENUMERATED resolution sites (buildTablesByName, BuildFKGraph, seed
  pools, D2/GraphQL edge emitters, W002 orphan detection, C103 orphan check,
  the I002 dead-column referenced-set) — L6-style totality applied to name
  resolution.
- Header/stamp grammar with one writer and one reader (pkg/genkit); one
  wording, adopted in a single pass (the one-release axiom makes any staged
  transition pure double work) — L6+L9.
- Property/fault verification style throughout: multi-iteration determinism
  tests, encoder coverage guards, conformance matrix, fault-injection matrix,
  DB-free compile checks of generated fixtures — L9.

## Free choices `[%%]`

Names and layouts the laws do not determine: `pgdesign_migration_ops`,
`pgdesign_applied_migrations` (view; merit: one SQL definition of
"applied + status" for four readers; carries version, applied_at,
description, checksum — all four columns serve reads),
`pgdesign_chain_position`, chain-edge files named by edge-content hash prefix
plus slug in `migrations/chain/` (content-derived: parallel edges,
endomorphisms, and concurrent branch allocation can never collide; the
display sequence is derived from topology at listing time),
`migrations/objects/`, `migrations/revisions/`, `migrations/archive/`,
`imports/<alias>/`, visible (non-dot) directory names for committed
load-bearing data, `internal/objstore`, `internal/project`,
`internal/predicate`, `internal/catalog` (the scoped pg_catalog query layer
shared by introspect and the predicate executor), normalization homed in
`internal/sqlparse` (the go-pgquery leaf — necessary: N and the ≈_pg bridge
are both built on its parse/deparse), migration file display names carrying
an auto-derived slug (override flag), `import lock` / `import update`,
`migrate upgrade`, `migrate rebase`, `migrate rekey`, `pgdesign revise`.
Per-language branding mechanics (boundary-empirical, not law-derived): Go
opaque struct with validating boundary and var members; Python parse() alias +
enum-typed surfaces + Row __post_init__ coercion on BOTH backends; TS
keep-the-union + parse(); Java/Kotlin value-parse (net-new fromValue) + JPA
AttributeConverter; Zig wrapper struct; sqlalchemy upgraded from
sa.Enum(string literals) to sa.Enum(PyEnumClass) (requires the generator to
gain enum imports/definitions); drizzle no change (already pgEnum-typed);
constants mode unchanged; constraints validators re-target the branded
representation. Seed import tiers (real-key pools / count-wrapped offset
subqueries / hard errors scoped to the provably-broken cases). strictcli
connection-env kind with registration-time unbound-flag error. Partition:
premake required; opt-in schedule key; unacknowledged missing schedule =
warning. pkg/diff deleted with a recorded promotion trigger. Web UI frontend
deferred. Consumer regeneration todos filed at the single final release.
Renames documented as delete+add (rename-hint ops are the recorded
alternative). Policies that are deliberate engineering choices, NOT law
consequences (the laws admit alternatives; these are chosen on the merits):
ALWAYS-large-table-safe generation (uniformity — a declared size hint would
be equally pure); FULL-PROJECT stamp scope (resolves the filtered-output
paradox); pure analyses BLOCK in revise's pure tier (the owner's
hard-constraints philosophy — analysis that can block must block).

## Ruled-out designs — do not resurrect (law/axiom violations, or strictly dominated alternatives)

Compat-named DB objects or dual recognition of old names (owner axiom).
Staged/multi-pass header transitions (the one-release axiom makes them double
work). Checksums on the rollback path (post-5.6, no such surface exists —
rollback reads no files). Replacing StrEnum with plain Enum, or
construction-closing machinery in Python (native Enum validation already
rejects invalid values). A nominal TS brand (regresses the union's
compile-closure and exhaustiveness narrowing). @Enumerated(STRING) in JPA
(persists constant NAMES, not DB values). A registry builtin-inclusion
special case in identity (redundant — builtin-derived domains materialize
into the model collections L1 covers). A single append-only manifest file,
whole-model snapshots, dot-directories for load-bearing data, or
counter-allocated edge filenames (git-merge conflicts at EOF; massive
duplication; invisibility of committed artifacts; cross-branch counter races
— the per-edge content-derived chain, object store, and visible names
dominate). Manifest-diff downs for ranges containing data-bearing ops (L4
violation — a structural down would recreate a dropped column empty).
Consolidations that fold across DML/RawSQL ops (schema-commutation cannot
see the data divergence they cause for fresh databases). "Net manifest
delta" as the invertibility criterion (a trap: DROP populated column then
ADD column has an empty net delta and destroys data — per-op typing is
correct). Rejecting (rather than validating) Go unmarshal/scan boundaries
(breaks every DB-scanned struct). Row-count-conditional generation (L5
violation — generation must not read the world). A closed boundary list AND
a grow-only boundary list (each unfalsifiable in one direction — the
bidirectional rule with demotion is the sound form). An iff form of L4's
composite-inverse rule (false converse: composites can be semantically
invertible when components are not). "Squash is composition by definition"
(empty without a morphism congruence — soundness is the CHECKED
squash-commutation property). A single undifferentiated ≈ (unachievable:
pg_get_* cast materialization is catalog-dependent and unreachable by pure
normalization — hence ≈_syn with the foldings inside N, and the
forward-simulation bridge for the residue). One-sided expression rewriting
(false drift for users who write PG's own forms directly — foldings must
apply to both sides, inside N).

---

# Part III — Where the codebase violates the laws today (grounding)

Source-verified. Organized by law; file references are load-bearing for
implementors.

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
  Extensions are in identity). Top-level type collections: THREE ordering
  schemes, not two — DDL declaration-order, JSON name-sorted, and domain
  DDL in first-use-across-tables order. The DDL emitter's determinism rests
  on ~7 inline Sorted* calls; generateJSON additionally carries its own
  copy-and-sort block (generate.go:56-99) that dies with Canonicalize. The
  existing determinism test hand-builds structs and can never fail.
  Constraint auto-naming is content-derived (build.go:1350-1376), never
  positional — alphabetizing canonical-only collections is name-stable (one
  assertion retires the reorder-renames-constraints hazard).
- Two divergent JSON serializers (generate json sorts; serve emits raw —
  and serve's raw emitter also serves the TOML-build path via its local
  parseAndBuild, not only the introspect endpoint). Desired content-derived
  constraint/index names can exceed 63 bytes while introspected names are
  NAMEDATALEN-truncated — a name-matching false-drift class N alone cannot
  fix (1.2 adds truncation-aware matching).
- The differ compares expressions by RAW STRING against PG-rewritten forms
  (pg_get_constraintdef/expr/indexdef) — live false-drift bug on
  CHECKs/partial indexes/policies; only types normalize. The differ is BLIND
  to PGVersion (in identity, not in SchemaDiff — pg_version changes alter
  emitted DDL invisibly to diff; fixing this belongs to the REVERSE
  conformance direction, since under-reporting breaks diff-empty-implies-
  revision-equal, not the forward gate). The differ DOES compare comments
  and extensions. diff's normalizeDefault is ToLower(TrimSpace(...)) —
  UNSOUND, not merely incomplete: it identifies the semantically distinct
  defaults 'Active' and 'active' (MISSED drift — the reverse failure mode
  from false drift; red test required before 1.2 replaces it). A SECOND live
  normalizer exists: validate.normalizeExpr (W018) sorts commutative AND/OR
  operands alphabetically and strips parens — a different equivalence than
  parse/deparse computes; 1.2's consumer list must move it onto N (or
  explicitly scope W018 as a looser heuristic), else the system ships two
  ≈-computations — precisely what L1 prohibits.
- FilterByGroups/FilterBySource call only buildTablesByName, never
  BuildFKGraph (model.go:138,166) — filtered schemas carry the parent's
  STALE graph with edges to filtered-out tables: a live latent bug (red test
  before 0.2), not a canonicalization nicety.
- semtype.Registry unexported/unserializable; typeDefsEqual ignores top-level
  Comment/Source but compares nested transition comments; builtin-derived
  domains (slug/email/short_text, scalar-with-CHECK) materialize into
  schema.Domains when used — identity coverage comes from the model
  collections; TypeDef.Source doc comment stale; type extends eagerly inlined
  (closure = composition references only).
- pg_version has THREE resolution tiers (live > config > toml); the live tier
  is a DB input that cannot enter pure Build; NINE cmd sites mutate
  schema.PGVersion post-Build (checks.go:84,343,502; handlers_codegen.go:48;
  handlers_generate.go:49; handlers_migrate.go:102,291,1118;
  handlers_build.go:88) plus a channel via generate.Options.PGVersion.

**L2 violations (mutable history, no content identity):**
- Squash deletes/rewrites originals (saferm + rename over <to>.toml); the
  M200 applied-version guard runs only if --db is voluntarily passed;
  tracking rows orphaned; zero squash-CLI tests; optimizeDDLOps keeps only
  the final type-change's down (reverts one step, not to pre-range type).
  The optimizer is a greedy, ONE-SHOT (not fixpoint), dependency-unaware
  rewriter: pair cancellation examines only the two endpoint ops via
  sameTarget (squash.go:131-146,249) — `add column x; create index on x;
  drop column x` cancels the add/drop pair and ORPHANS the index op (a live
  squash bug; a second orphan variant: duplicate `add column x` — the first
  add pairs with the drop, the second survives); the "references" relation needed for the side condition spans
  RefTable/RefCols, trigger function, view/function bodies, and depends_on
  (DDLOp carries these as distinct fields a naive name check misses); no
  associativity, confluence, or order-independence tests exist.
  SquashMigrations(dir, from, to) has no DB parameter — the 0.6(d) stopgap
  changes its signature.
- Migration checksums are recorded over file bytes and NEVER verified —
  historical files may legitimately mismatch what a database recorded
  (post-apply edits), so the 5.2 prefix fold needs an explicit
  amnesty-or-refuse policy.
- No ledger/manifest/chain exists; discovery skips non-semver filenames; ~7
  functions rely on semver ordering; migrations-dir sentinel hardcode at 8
  migrate sites + 1 serve site (the `output` flag shows the correct
  Default(nil)+was-set pattern).

**L1+L2 violations (ops carry lossy mirrors):**
- THIRTEEN unserialized op-family concerns: nine pointer-def families +
  RawSQL (SM-trigger DDL silently dropped on round-trip) + PartitionChildSpec
  + ParentTable + the partman-config ops — which EXIST TODAY and are broken
  AT APPLY TIME, not merely on round-trip: update_partman_retention/premake
  are emitted (generate.go:1004-1009,1024-1029) but OpToSQL has no case for
  them, so they fall to the default "-- unknown op" comment stub
  (sql_gen.go:147-148) in memory, at generation and apply alike; the
  regression test never calls OpToSQL, so the silent no-op is live and
  untested. Down-ops embed def pointers too and degrade on rollback.
  create_function/create_trigger parsed from disk fall back to emitting the
  WRONG OBJECT (deny-mutation / append-only); sequences lose parameters.
  opCreateTable passes nil enum/domain lists (unqualified type rendering)
  and hardcodes pgVersion=0 despite DDLOp.PGVersion existing — and OpToSQL
  consumes PGVersion INCONSISTENTLY (honored for some ops, hardcoded zero
  for others).

**L5 violations (the functor reads or trusts the world wrongly):**
- Generation consumes live TableStats (pg_stat_user_tables) to choose
  NOT VALID splits and expand/contract forms. The EXPAND_CONTRACT_TYPE_NARROW
  advisory (generate.go:565-574) fires only when EstimatedRows exceeds a
  threshold AND the change narrows — relocating it to diff classification
  DROPS the row-count gate (the advisory becomes narrowing-always; a stated
  behavior change, chosen deliberately). migrate generate requires --db +
  --version today; migrate plan diffs TOML vs live DB (its post-5.9 meaning
  — pure preview from the chain — must be assigned); migrate test --shadow
  replays files and must replay edges post-5.2.
- No preconditions anywhere; version row written LAST (committed phases and
  non-transactional ops leave real DDL with no durable record; re-apply
  restarts at op 0 and aborts forever). Rollback re-reads files, trusting
  them over the DB — including AFTER BASELINE: baseline records checksum
  literal "baseline" with no ops (baseline.go:120), yet rollback loads
  version+".toml" and runs its file down-ops regardless (rollback.go:41,207)
  — rollback-after-baseline executes DROPs against objects pgdesign never
  created (a live data-loss bug; red test in 5.6). Tracking writes:
  state.go's RecordMigration/RemoveMigration helpers vs inline SQL in
  apply/rollback — RemoveMigration has zero production callers (dead;
  RecordMigration is alive — baseline calls it). PGVersion is assigned
  AFTER diff.Diff runs in migrate plan/generate — version-dependent diff
  classification runs at version 0 today (red test rides 0.2's pg_version
  seam). Two dead CLI artifacts: apply --timeout is registered but never
  read (lock timeout comes from config), and baseline --adopt is named in
  an error message but never registered (a phantom flag).
- Introspect has NO table-level filtering — the tracking table introspects as
  a user table; the TRIGGER filter uses the LEADING-underscore
  `_pgdesign_sm_%` pattern while FUNCTIONS are excluded differently (by
  trigger return type plus a pgdesign_deny_mutation name check) — 0.4's
  one-predicate unification covers both plus the missing table/view classes. Introspect constructs Schema
  directly (never Build): nil FKGraph/TablesByName, raw order; the
  copy-pasted finalize is four steps, two of which (SM transitions, group
  resolution) need raw/registry inputs introspect lacks. Introspect's ~45
  query functions are private whole-object bulk extractors; per-op
  preconditions need scoped attribute checks — a different granularity (the
  shared layer is an extraction into a new package, not a reuse of existing
  functions). checkNF (checks.go:134-170) resolves a DB URL, connects, and
  runs FD discovery when reachable, and reports via WarnReporter — it is
  NEITHER pure NOR blocking; the only blocking NF gate today is generate's
  --strict-nf (handlers_generate.go:31-40). SHADOW TEST exists
  (cmd/pgdesign/handlers_migrate.go:987-1133). serve's handleMigrations
  queries the tracking table (existence-guarded — returns 200 with [], not a
  500) and selects version, applied_at, description, checksum ORDER BY
  applied_at (handlers.go:125-127) — the replacement view needs all four
  columns and a defined applied_at derivation; the version endpoint opens
  version+".toml".

**L6 violations (provenance absent or partial):**
- Headers: 36 codegen sites (5 validator helpers within them) + CLI
  planner-prepend for sql/d2/graphql (json and doc headerless) + seed's
  distinct wording; 7+ wordings; hasCommentHeader lacks `--`; Go headers
  don't match the `^// Code generated .* DO NOT EDIT\.$` tooling regex;
  genkit's Generator/MultiFileGenerator interfaces are DUPLICATED in
  internal/codegen — and the signatures differ (genkit takes
  schema interface{}; codegen takes *model.Schema), so 0.1's absorption is
  an interface-unification decision, not a mechanical move; genkit has
  exactly one consumer today (cmd/pgdesign/freshness.go); the stamp helper
  must stay a pure string formatter (seed uses internal/diagnostic, not
  pkg/diagnostic — dragging diagnostics across that boundary is the hazard).
  codegen --check is byte-exact (pkg/genkit). splitfmt (.sqlsplit) is SEALED
  (line 1 = statement count — cannot carry a header). fmt rewrites schema
  TOML (--column-order = revision change); introspect --output writes a NEW
  candidate source file to an arbitrary path (scaffold output, not source
  editing); testdb init writes language wrappers and a CI workflow
  (handlers_testdb.go:308,345) — a file-writer the enforcement taxonomy must
  classify. build applies per-output FilterByGroups/FilterBySource;
  standalone codegen does NOT (same artifact, two contents by entry point);
  build auto-commit warns-and-continues on safegit failure. Seed content
  depends on --seed/--counts/--mode and is never freshness-checked (its
  stamp will be honest provenance only).

**L7 territory:** introspected models lack the registry; serve returns
{schema, diagnostics} (the envelope must wrap, not drop, diagnostics — and
serve's payload key changes are an API-consumer-visible change to note in
4.3's packaging).

**L8 territory (the world's crash semantics):**
- IsNonTransactional: create AND drop_index_concurrently +
  version-conditional enum-add (transactional PG12+). An interrupted CREATE
  INDEX CONCURRENTLY leaves an INVALID index of the target name — IF NOT
  EXISTS would skip it forever. TWO wrong version comments in sql.go: the
  claim that CIC+IF NOT EXISTS is version-incompatible (valid since 9.5) and
  sql_gen.go:554's "PG 9.3+" for ADD VALUE IF NOT EXISTS (it is 9.6+).
  drop_index_concurrently's renderer lacks IF EXISTS. The advisory lock is
  session-level, shared by apply/rollback/baseline, held across reopened
  transactions.

**Boundary facts (languages, consumers, environment):**
- Codegen enum shapes: Go `type X string` + const block (const of STRUCT
  type is illegal — branded members must be vars); TS literal union
  compile-closed (transition maps ALREADY typed Record<Status, Status[]>);
  drizzle ALREADY emits pgEnum as the column builder (typed — no change
  needed); sqlalchemy already emits sa.Enum("v1","v2",name=...) — the
  columns are native enums; only the Python-side annotation is str, and the
  upgrade to sa.Enum(PyEnumClass) requires enum imports/definitions the
  generator has never emitted; Python Enum.__call__ already validates (no
  closing machinery needed; residual str-structural openness unclosable);
  Java/Kotlin real enums with UPPER_SNAKE names vs raw getValue() values
  (@Enumerated(STRING) would persist NAMES; fromValue() is NET-NEW — only
  getValue() exists); Zig string consts (transition maps use sanitized
  struct-field keys). No parse helpers anywhere. go_constraints emits
  `package constraints` referencing row structs from package schema by bare
  name with no import, switches on raw string cases, and zero-value checks
  via == "" (go_constraints.go:23,109-121) — branding requires an import,
  .String() switches, AND a rework of the NOT-NULL zero-value check. ILLEGAL
  JAVA (multiple public types per file) in THREE modes: java_jpa,
  java_types, java_constraints. The conformance tests compile HAND-AUTHORED
  templates for all six languages (Java/Kotlin toolchain-gated; Zig renders
  then skips), NEVER codegen output, and are DB-gated AND self-skip without
  python3/psycopg/node — the true fact is stronger than any per-language
  count: NO codegen output is compiled anywhere today, for ANY language, so
  4.0's compile checks are new for all six. Python
  query-layer neither imports nor defines the enum classes it annotates
  (survives via future annotations); BOTH PgBackend and InMemoryBackend
  build rows uncoerced (Row __post_init__ covers both); _constraints.py
  needs NO change. go_types and go_gorm both emit GenerateEnums into package
  schema (dedup must be co-generation-aware). Seed's pool-empty fallback
  silently emits random UUIDs; the UNIQUE dedup keys the concatenation of
  ALL constraint columns with a fixed-rowIdx retry then SILENT fall-through.
  W002 orphan detection builds its referenced-set from raw
  fk.RefSchema+"."+fk.RefTable strings, bypassing TableByName
  (validate.go:1054-1063); GraphQL relation fields use bare fk.RefTable
  (graphql.go:164-165) — both are import-resolution sites beyond the D2 one;
  so are the C103 orphan check (checks.go:266-294 — W002's twin in a
  separate path) and the I002 dead-column referenced-set
  (validate.go:2264-2273 — bare RefTable, RefSchema ignored).
  C104 continues (silently skips) on unresolved refs (checks.go:218-220).
- strictcli: the check command builds a fully-populated *Context and
  discards it; infra roots + handshake envs hermetic-IMMUNE, flag Env()
  hermetic-SUPPRESSED (no primitive fits a connection URL); per-flag
  provenance already exists (Context.Source()). SEVENTEEN DB-URL flags (16
  --db + 1 --live), three default semantics — the adoption target is the
  mechanical assertion "no DB-URL flag registers unbound," not a count.
  internal/dbutil.ResolveURL is a DEAD first-non-empty URL resolver with
  zero production callers (2.2 absorbs or deletes it). internal/pgcap is a
  LIVE 10-capability version-gate registry imported by risk, sql,
  introspect, validate, migrate, testdb — the predicate IR and
  internal/catalog must gate version-conditional queries through it, not
  regrow capability logic.
- Partition bugs: python_ddl passes Retention as p_interval (sibling of the
  fixed generate path); omitted premake emits p_premake := 0 (disables
  partman); silent skip when pg_partman undeclared; maintenance + manual
  children emit contradictory DDL; PartmanRunMaintenanceCron() is
  dead-but-tested code.
- pkg/diff (exported stub): zero importers. internal/diff: 21 model types
  consumed field-by-field by migrate. generate and migrate are siblings;
  internal/sqlparse is the go-pgquery leaf (imported by migrate, introspect,
  model, workload, testdb); sqlutil is imported by validate+codegen. The
  go-pgquery dependency is pinned to a pseudo-version; its deparse output
  DEFINES N and hence identity — its cross-version stability is itself a
  boundary item, checked by the golden corpus.
- serve: DB-coupled at construction; --timeout registered but never
  enforced; audit runs TANE synchronously; GenerateD2 called with a nil
  registry (SM diagrams silently dropped); serve's own parseAndBuild
  (internal/serve/handlers.go:471-493) discards the registry, applies no
  config extensions (builtin registry only) and no pg_version resolution;
  the /schema endpoint INTROSPECTS the live DB — a project-mode branch does
  not exist and must be built for DB-free serving. serve/handlers.go is
  co-edited by phases 5 and 8 (ordering required). CI: postgres:17 +
  pg_partman; 10 DB-backed migrate tests of ~162.

---

# Part IV — The boundary (enumerated residual risk)

Everything below is irreducible — checkable, not eliminable. Per the boundary
doctrine's triage rule: a defect in kernel territory is an implementation
error against a stated law property; a defect here is checked by the named
mechanism; a defect in plain-engineering territory is an ordinary bug. This
list may grow only with a post-mortem containing a positive impossibility
argument, and items that become property-checkable are demoted to the kernel.

1. **Postgres crash windows** around non-transactional DDL (CIC, drop-CIC,
   pre-PG12 enum-add). Check: fault-injection matrix incl. indisvalid
   assertions (5.5).
2. **The upgrade choreography** (DB transaction + pre-commit file writes).
   Check: crash injection on both sides of COMMIT (5.2).
3. **The SQL predicate renderer** — a second computation of ≈_syn in
   PL/pgSQL. Check: the conformance matrix (Go executor vs SQL renderer vs
   differ) — which is SAMPLED agreement, not proof of ≈-agreement, so it is
   fed GENERATED random expressions in addition to curated states (5.7).
4. **The ≈_syn/≈_pg residue** — catalog-dependent cast materialization is
   unreachable by pure normalization; the forward-simulation rules on
   introspected text are finite and documented-incomplete BY DESIGN. Check:
   the rule fixture suite; the comprehensive fixture (CHECKs, partial
   indexes, policies) reused by diff --live, upgrade, reconcile, shadow test
   (1.2/5.8).
5. **Six consumer languages' semantics.** Check: DB-free compile checks of
   generated fixtures — all six mandatory (4.0).
6. **Git merge behavior** on chain files. Minimized by one-file-per-edge
   with content-derived names (textual AND allocation conflicts impossible);
   semantic forks remain. Check: two-head detection + `migrate rebase`
   (5.10, 6.1).
7. **Concurrent binaries** on one database. Check: the shared session-level
   advisory lock; concurrent-apply-during-upgrade test (5.2).
8. **TOCTOU between check and apply** on a live database. Minimized:
   preconditions run inside each op's transaction (5.7).
9. **Filesystem atomicity** for store writes. Minimized: content-addressed
   idempotent writes; consistency checker (5.2, invoked by 6.2 and 7.2).
10. **Consumer adaptation** to the breaking release. Check: filed todos
    containing scripted `pgdesign codegen --check` invocations; the pass
    itself is the consumers' half (4.3).
11. **External milestone**: strictcli must ship the connection-env kind
    before the DB TIERS of 6.1/7.4/seed-tier-1 finalize (the pure tiers
    have no phase-2 dependency; todo filed at phase-0 start).
12. **go-pgquery deparse stability** — N (and hence identity) is DEFINED by
    an externally-pinned parser's deparse output; a version bump can shift
    ≈_syn. Check: the golden normalized-expression corpus (CI-red on shift);
    recovery: `migrate rekey` (L2's epoch protocol).
13. **Git plumbing for import fetches** (ref resolution, auth, remote
    availability) — distinct from item 6's merge behavior. Check: import
    lock/update error-path tests; offline builds never need the remote
    (vendored surface).
14. **SHA-256 collision resistance** — L2's stated assumption; id-equality
    fast paths skip byte comparison. Check: none (cryptographic assumption,
    named so it is never silently strengthened into "proven").

---

# Part V — Phases

Phase numbering: 0 = substrate repairs; 1 = the kernel; 2 and 4-9 = boundary
phases and adoption. The number 3 is intentionally unassigned (the identity
work — whole-model form, revision hash, one serializer — lives in kernel
subphases 1.4/1.5). Every subphase cites its laws.

## Phase 0 — Substrate repairs (make the codebase law-capable)

Bug fixes and consolidations that must precede the kernel; none depend on it.
Build order: 0.1 and 0.2 are PARALLELIZABLE (disjoint code; their golden
churns land as ONE regeneration sweep); 0.2 -> 0.3 (0.3 rekeys the graph 0.2
relocates); 0.4/0.5 after 0.2; 0.6 anytime. The strictcli todo (boundary
item 11) is filed at phase-0 start.

### 0.1 Header unification + stamp grammar — L6
- **What:** One shared parameterized header helper homed in pkg/genkit
  (writer, reader, and stamp grammar co-located; seed must stamp and cannot
  import internal/codegen; the stamp helper is a PURE STRING FORMATTER — it
  must not drag diagnostics types across the internal/pkg boundary).
  Absorbing genkit's duplicated Generator/MultiFileGenerator interfaces is an
  INTERFACE-UNIFICATION decision, DECIDED: unify on Generate(*model.Schema)
  (genkit's schema interface{} exists for one consumer — freshness.go — and
  concrete typing is strictly better; genkit may import model). Final wording adopted immediately in ONE pass —
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
  canonical stamp via genkit's parser; goldens regenerated in the shared
  0.1+0.2 sweep; Go headers match the tooling regex.

### 0.2 Canonicalize() — L1 prerequisite (determinism)
- **What:** A shared finalize routine invoked by Build, BuildMulti,
  Introspect, and FilterByGroups/FilterBySource: alphabetical ordering for
  per-table collections (incl. matview indexes), top-level type collections,
  and Extensions; topological ordering with ALPHABETICAL tie-break for
  tables/views/matviews/functions — reusing internal/format's
  pre-sort-then-TopoSort pattern; THREE topo paths collapse here (build.go,
  generate.go, python_ddl.go — CycleGroups semantics preserved);
  internal/format's path CANNOT collapse (it sorts pre-Build parse.RawTable
  for fmt) and shares only the tie-break helper; semtype's extends-resolution
  TopoSort is a distinct domain, identity-irrelevant today because extends is
  eagerly inlined — noted so 1.1's composition-closure work re-checks it;
  introspected functions (no DependsOn)
  fall back to alphabetical; columns source-ordered; derived structures
  (FKGraph, TablesByName) built here — WITH a red test first for the live
  filter bug (FilterByGroups/FilterBySource never call BuildFKGraph today;
  filtered schemas carry stale graphs). generateJSON's now-redundant
  copy-and-sort block is DELETED in the same pass. Scope split: Canonicalize
  owns ordering + derived structures; the raw/registry-dependent finalize
  steps (SM transitions, group resolution) stay Build-side; an EXTENSION
  POINT is declared where Canonicalize will N-normalize expression fields
  into the IR once 1.2 lands (activating full L1(a) — until then identity is
  structural with opaque expression leaves). pg_version: config+toml tiers
  resolve inside Build; a post-Build live-override seam replaces the NINE
  scattered mutations and the Options.PGVersion channel. Sorts run
  post-enrich. Delete the 7 Sorted* helpers and ALL emitter-side sorting
  (incl. the SQL emitter's ~7 inline sites — DDL fixtures must cover this).
  Fix the luck-stable emitters. Multi-iteration TOML->Build->serialize->
  compare CI determinism test (pinned iterations; >=2 entries per map-sourced
  collection) + Canonicalize postcondition.
- **Why:** L1 is impossible over nondeterministic bytes. A shared finalize
  makes INTROSPECTED schemas canonical (L5's codomain checks and L7's model
  classes both need it) and collapses four topo implementations.
- **Verify:** Determinism test red before/green after over DDL AND JSON;
  CANONICALITY, not just repeatability: shuffled-declaration-order fixture
  pairs (permuting canonical-only collections — checks, indexes, uniques,
  policies — never columns or enum values, whose order is semantic) produce
  identical output; constraint auto-names STABLE under reordering (they are
  content-derived, never positional — one assertion retires the hazard);
  view-references-view fixture dependency-ordered; introspected schemas pass
  the postcondition; no emitter-side sorting by grep; filtered schemas carry
  RECOMPUTED graphs (the red test goes green).

### 0.3 Schema-qualified identity + final graph API — L1/L6 substrate
- **What:** The FKGraph/walker end-state API in ONE pass: FKEdge gains
  schema qualification AND `Imported bool`; keys become (schema, name) —
  reconciling TablesByName's "schema.name" (and its ".name" empty-schema
  artifact) with FKGraph's bare names under one keying rule; group
  resolution rekeyed with a stated bare-to-qualified rule; cascade walkers
  gain a depth-bounded signature. Plus the FKGraph PROJECTION SERIALIZER
  (deterministic, (schema,name)-keyed; excluded from identity, included in
  the API payload) — owned here. BLAST RADIUS NAMED: every consumer of
  FKGraph.Reverse[name] rekeys — 8 codegen files (gorm, jpa, sqlalchemy,
  drizzle, the python query-layer family, querylayer constraints) plus
  generate/graphql.go plus workload's N+1 analysis — not just the
  validate/check consumers. Rekeying FKEdge.FromTable/ToTable also changes
  CascadeChain's RETURNED STRINGS, rippling into W013/W014/W015 diagnostic
  text — a behavior change this subphase's verify pins.
- **Why:** Two identity schemes for one object is a latent bug today and a
  guaranteed one under imports; designing the end-state API in one pass
  prevents repeated churn over the same surface (the collapse-multi-pass
  rule).
- **Verify:** Red-green: same-named tables in two schemas through cascade
  checks (W013/14/15), workload, group filtering, AND full codegen (the
  two-schemas fixture must pass every generator, not just the checks);
  depth-bounded walk tested; projection serializer deterministic and
  reconstructable.

### 0.4 Introspect filters managed objects — L5 hygiene
- **What:** One isManagedObjectName() predicate: `pgdesign_%` for tables and
  views (view filtering designed here) and the legacy `_pgdesign_sm_%`
  function/trigger prefix. Reserved-name user objects trigger a diagnostic.
- **Why:** L5's codomain check ("introspect, diff, expect empty") is
  unusable if the functor's own trace registers as drift; pattern filtering
  makes future managed objects inherit coverage.
- **Verify:** Phase-0 scope: pattern filtering against SYNTHETIC
  reserved-name objects + the diagnostic. (The real managed objects arrive
  in phase 5; introspect-side WIRING for them is a 5.2 PREREQUISITE — the
  upgrade's own reconcile gate would otherwise refuse itself on the tables
  it just created; the broader cleanliness assertion lives in 5.8.)

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
  (Safe, risk-classified UPDATE part_config ops) / interval changes (hard
  error + guidance); migrate guards extension presence; `schedule` key wires
  the dead pg_cron helper (pg_cron declared or hard error); unacknowledged
  missing schedule = warning. The partman-config op family EXISTS TODAY and
  is silently broken AT APPLY TIME (OpToSQL renders "-- unknown op" for it)
  — the fix is red-green against OpToSQL output, not a greenfield addition;
  phase 5's op rewrite then absorbs the family. (d) Squash safety stopgap
  until phase 5: --db and the M200 check mandatory (a SIGNATURE change —
  SquashMigrations gains a DB parameter; stated limits: blocks offline
  squash of never-applied ranges; doesn't fix the rewrite mechanics); first
  squash-CLI test. (e) Serve/apply hardening that must not wait for phase
  5: the migration-version endpoint joins a RAW path parameter into
  migrationsDir with no containment check — a PATH TRAVERSAL (../../
  escapes) fixed now; lock_timeout is interpolated into SET via Sprintf
  from unvalidated config (validate or parameterize); the dead apply
  --timeout flag and the phantom baseline --adopt flag are deleted.
- **Why:** All are live silent-degradation defects (the class L5/L6 exist to
  kill) or misleading API surface; none depend on the kernel; every one
  removed is one less thing later phases interact with.
- **Verify:** Failing test first per bug — incl. the OpToSQL partman-op red
  test; CI pg_partman coverage; squash without --db hard-errors.

## Phase 1 — The kernel (pure, law-tested, no Postgres, no CLI)

The algebra as packages, property-tested per L9. Everything later is an
adapter around this. LAND ORDER: 1.3 (store) and 1.6 (modelgen) first — no
kernel dependencies — then 1.1, then {1.2, 1.5}, then 1.4 (whose property
tests consume modelgen and whose conformance work consumes N).

### 1.1 enc: the canonical encoder — L1
- **What:** A DEDICATED canonical encoder (explicit field ordering; per-field
  presence semantics distinguishing unset from zero, normalizing to pointers
  where needed; deliberate key-sorting for map-typed fields — Index
  Opclasses/Collations/With, Schema.Groups, NamedTransition.Requires, and
  state-machine transition maps in the type-definition path — the schema-side
  StateMachineTransitions duplicate stays excluded per 1.5) producing
  per-object canonical JSON for every schema object, each form carrying its
  CODEC VERSION (epoch — L2), with a TOTAL DECODER (decode∘enc = id on
  canonicalized models — 5.9 and 7.2 deserialize, so decodability is
  load-bearing, not optional). The ORDER-SEMANTICS TABLE is written as part
  of the format spec, EXHAUSTIVE over the Model with two columns per
  collection (collection order; intra-object order): columns and enum values
  semantic; composite-type fields, function args, partition key columns, FK
  column correspondence, and index key-column order semantic INTRA-object;
  checks, indexes, uniques, policies, triggers canonical-only; exclusion
  elements and trigger events classified explicitly — this table is the
  definition of ≈_syn on the structural sublanguage (and PG fires same-event
  triggers in name order, so trigger name-canonicalization coincides with
  firing order). Type identity from MODEL-LEVEL collections (both
  construction paths populate them; builtin-derived domains materialize
  there, so builtin changes flip identity with no special case). The
  registry snapshot's scope is DECIDED: it serializes only what has no model
  representation — expected EMPTY for identity purposes (a verification test
  asserts it; if the test ever finds semantic registry state missing from the
  model collections, that state is added to the model, not to identity via
  the snapshot); its sole role is import-surface reconstruction — with an
  explicit field policy (semantic + all comments; Source excluded) and the
  stale Source doc comment fixed. Allowlist completed: Index.IsAutoFK
  (excluded — enrich-derived), Table.PartmanManaged and Table.PartmanParent
  (excluded — introspect-path facts, not desired-model semantics; they never
  appear in TOML-built models). MECHANICAL
  TOTALITY GUARDS: reflection-based tests asserting every exported field of
  every DDL-reaching model struct AND every registry-snapshot struct is
  either encoded or on an explicit exclusion allowlist with a reason
  (CycleGroups, StateMachineTransitions, SourceFile, caches).
- **Why:** L1 is the kernel's foundation; the totality guards convert "the
  encoder is complete" from a review hope into a checked law (L9) — the
  highest-value single mechanism in the plan. L1(a) is structural-only at
  this point (expression leaves opaque) — full activation comes when 0.2's
  extension point N-normalizes expressions into the IR (with 1.2).
- **Verify:** Property tests over modelgen inputs (1.6): per-object bytes
  independent of neighbors and struct-field-order refactors; decode∘enc =
  id round-trip; coverage tests red when a field is added unencoded (model
  AND registry-snapshot); map-key ordering deterministic; semantic-order
  collections are slices, never sorted maps; shuffled-declaration-order
  convergence (≈_syn-equal inputs, identical bytes); manifest keys
  kind-qualified (a same-named table+function coexist; two function
  overloads are distinct entries); builtin email-regex change flips
  identity; Source relabeling does not; nested transition comments do.

### 1.2 N: the normalization primitive — L1
- **What:** The normalizer N — types, defaults, expressions (parse/deparse)
  PLUS the catalog-independent foldings applied to BOTH SIDES (IN <-> = ANY
  (ARRAY[...]), array-literal forms — inside N, per L1(c); one-sided
  rewriting is ruled out) — homed in internal/sqlparse (the go-pgquery
  leaf). ≈_syn = kernel of N. The ≈_pg RESIDUE (catalog-dependent cast
  materialization) is bridged by forward-simulation rules on the
  INTROSPECTED side on live paths, fixture-checked, incompleteness
  documented. Canonicalize's extension point begins N-normalizing expression
  fields into the IR (full L1(a) activates). The differ adopts N IMMEDIATELY
  (red-green: introspect->diff over CHECKs/partial indexes/policies reports
  false drift today — a live bug), replacing BOTH existing normalizers:
  diff's unsound lowercasing normalizeDefault (red test for the
  'Active'/'active' missed-drift case FIRST) and validate.normalizeExpr
  (W018 moves onto N — DECIDED: a looser second equivalence is exactly what
  L1(b) prohibits). NAME-MATCHING drift joins N's scope: desired
  content-derived constraint/index names can exceed 63 bytes while
  introspected names are NAMEDATALEN-truncated — the differ's name matching
  gains truncation awareness (red fixtures; expression normalization alone
  never touches this class, and it hits 5.8's reconcile gate directly).
  Stated boundary: internal/sqlexpr survives as a structural-EXTRACTION
  engine (E213 column refs, CHECK-pattern extraction), not a ≈-engine — the
  no-normalizer-outside-sqlparse grep has a principled exception. Later
  consumers: upgrade reconcile, predicates, reconcile-verify, shadow test,
  import drift (7.2).
- **Why:** L1(b): every comparison engine must compute the same ≈_syn — two
  disagreeing normalizers already ship today, and the differ additionally
  disagrees with Postgres's rewriter. L1(c): the catalog-independent
  foldings belong inside N precisely because a user may write PG's own
  forms directly; only the catalog-dependent residue needs a bridge.
- **Verify:** Red-green on the false-drift fixture AND the missed-drift
  default fixture; N∘N = N idempotence over a generated expression corpus;
  folding symmetry (IN-form and =ANY-form of the same predicate normalize
  identically FROM EITHER SIDE); the GOLDEN CORPUS of normalized forms
  committed (a go-pgquery bump that shifts ≈_syn — hence identity — turns
  CI red; boundary item 12); the residue-rule suite; diff --live clean on
  the comprehensive fixture (reused verbatim by 5.8); grep: no normalizer
  outside internal/sqlparse.

### 1.3 store: internal/objstore — L2
- **What:** The content-addressed store package: hash-keyed put/get, dedup,
  layout, epoch awareness (codec version travels with content); multiple
  roots (migrations/objects/ now; imports/<alias>/ later). Property tests:
  get∘put = identity; put idempotent; ids location-free.
- **Why:** L2 as code; one package so a third store implementation can never
  arise (ops, revision manifests, and import surfaces all reference it).
- **Verify:** Property suite green; concurrent idempotent-put test;
  epoch-mismatch reads error rather than mis-decode.

### 1.4 chain: revisions, edges, composition, inverses — L3+L4
- **What:** Revision manifests as SORTED MAPS of kind-qualified keys
  (kind, schema, name[, arg-signature]) -> id (comparison = key-wise
  symmetric difference; the Merkle dividends of Part I; renames are
  delete+add — documented, with rename-hint ops as the recorded
  alternative). Parent-linked edge model, edge identity CONTENT-DERIVED
  (file = edge-content hash prefix + slug; display sequence computed from
  topology at listing time) — parallel edges, pure-DML endomorphisms, and
  concurrent branch allocation can never collide; head/find-heads (genesis:
  null parent); composition = path concatenation (free category — identities
  are VIRTUAL empty paths, never files, never applied). THREE-WAY typed
  invertibility per L4 (mechanical / declared-inverse incl. vacuous DML /
  non-invertible); composite inverse defined WHEN all components have one;
  manifest-diff downs representable ONLY for fully-mechanically-invertible
  ranges. Revision = hash of manifest; Revision is an OPAQUE TYPE indexed by
  model class (registry-present / registry-absent — the marker lives INSIDE
  the hashed bytes; cross-class comparison is a type error). Per-object-id
  diff fast path; diff(a,a) = empty pinned; the conformance pair:
  revision-equal implies diff-empty as the initial gate; the REVERSE
  direction adopted as the end-state invariant once the differ fully adopts
  N — its obligations include pg_version AND Groups joining SchemaDiff
  (both are in identity, both invisible to diff today; under-reporting
  breaks the reverse direction, not the forward gate — whose only real
  content is catching diff reading non-encoded state). The reverse
  direction's PRIMARY enforcement is the DIFF-TOTALITY MUTATION GUARD:
  perturb any single ENCODED field (driven by the encoder's own field
  registry) and assert diff is non-empty — retiring the under-reporting
  defect class by construction rather than field-by-field discovery. An
  ABSTRACT OP interface lands here (kind, target, invertibility class,
  payload-by-content-id) — 5.1's concrete families implement it, keeping
  the kernel free of migrate imports — with a small op-list generator over
  the abstract vocabulary for the inverse-law property tests (modelgen
  makes models, not op-lists).
- **Why:** L3/L4/L7 as code; squash, rollback, pure generation, and
  enforcement all become path operations and lookups over this structure.
  The free-category framing puts the trivially-true laws where they belong
  (by construction) and the real risk where it lives (squash soundness —
  checked in 5.3, not asserted here). The abstract op boundary keeps the
  dependency direction kernel <- adapter.
- **Verify:** Property tests over modelgen inputs: inverse laws on
  fully-mechanically-invertible composites; any composite NOT fully
  mechanically invertible has no manifest-diff inverse BY TYPE
  (declared-inverse-containing included); edge-identity uniqueness under
  parallel edges, endomorphisms, and simulated concurrent allocation;
  opaque-Revision cross-class comparison errors; diff(a,a) empty;
  kind-qualified key collision tests; conformance direction in CI; the
  diff-totality mutation guard red on an unperturbable-by-diff field;
  sensitivity tests (comment/column/type/pg_version/extension/GROUPS
  changes flip revisions; no-op rebuilds don't). (No associativity or
  identity-edge tests — trivially true in a free category; none are
  needed.)

### 1.5 Whole-model form, envelope, one serializer — L1+L7
- **What:** Whole-model form = versioned preamble + ordered concatenation of
  per-object forms (the preamble carries format_version AND codec epoch).
  Semantic-only policy: type identity from model collections;
  StateMachineTransitions + CycleGroups excluded as derived;
  FKGraph/TablesByName/caches excluded; Extensions (ordered) + PGVersion
  INCLUDED; object comments IN, TOML-formatting comments OUT; [suppress] and
  extension-registry data OUT (extension DDL-name resolution stays
  emitter-side, byte-compare-covered; baking it into the model is the
  recorded summit alternative). The JSON artifact is an envelope
  {format_version, revision, model, diagnostics?} with canonical bytes
  embedded VERBATIM (raw-message; re-encoding would break
  revision == hash(model)); serve's {schema, diagnostics} shape is WRAPPED
  (the payload-key change is consumer-visible — noted in 4.3). generate
  json and serve schema responses call THE SAME envelope function; the
  divergent serializers die. Revision printed by validate/build. Stated
  policy: a pgdesign upgrade that changes the model schema or the codec
  flips all revisions — derived artifacts regenerate once (the existing
  consumer convention, now load-bearing); HISTORY re-keys via `migrate
  rekey` (L2's epoch protocol — history is not a derived artifact and
  cannot be regenerated).
- **Why:** L1 demands one serializer; L7 demands the in-bytes marker; the
  envelope resolves in-band-stamp circularity (bytes cannot contain their
  own hash); the epoch policy separates what regeneration can fix (derived
  artifacts) from what needs the sanctioned re-key (history).
- **Verify:** generate json and serve bodies identical for the same schema;
  envelope revision verifies against embedded bytes; marker present on the
  introspect path; diagnostics preserved; goldens updated once.

### 1.6 modelgen: the model generator — L9's input source
- **What:** A pure random generator of VALID models: well-formed FK graphs
  (incl. cycles where legal), full type closures (enums/domains/composites/
  SM types), version-gated features, multi-schema layouts, canonical-only
  collection permutations. Tunable fragments: the INJECTIVE fragment (no SM
  types — introspection lossiness excluded) and the BRIDGE-PROVEN expression
  fragment, for L10's restricted generator. No DB, no CLI.
- **Why:** L9 demands property tests over GENERATED inputs and L10's
  round-trip test consumes generated model pairs — but nothing in the
  codebase produces models (the seed package generates row DATA). Without
  this deliverable the kernel's verification doctrine is aspirational.
  Built once, consumed by 1.1, 1.2 (expression corpus), 1.4, 5.3
  (critical-pair inputs), and 5.8 (L10).
- **Verify:** Generated models Build+Canonicalize cleanly at all sizes;
  fragment restrictions honored; deterministic under a seed; shrinking
  produces minimal counterexamples.

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
- **What:** Declare PGDESIGN_DB once; bind every DB-URL flag (--db and
  --live families; three default semantics normalized); checks read via the
  framework; --hermetic makes DB checks skip visibly; config [database].url
  stays a documented separate layer (cli > env > config). Not a leaf: every
  later DB entrypoint (revise's DB tier, import lock/update, live
  verification, seed tier-1, migrate upgrade/rekey) binds from birth when
  phase 2 has landed; when a phase-5 command lands first, 2.2's sweep picks
  it up and 2.1's registration error prevents regressions thereafter.
  internal/dbutil.ResolveURL — a dead first-non-empty URL resolver with
  zero production callers, doing exactly this subphase's job — is absorbed
  as the layered resolver or deleted (dead-code policy; the roadmap's
  resolution layer supersedes it).
- **Why:** One variable, one story; without the non-leaf edges the
  pathology regrows.
- **Verify:** The MECHANICAL assertion "no DB-URL flag registers unbound"
  (not a count — counts rot); env-only invocation on every DB command with
  provenance; hermetic skips; raw os.Getenv gone from cmd/ (test harness
  excepted); precedence test.

## Phase 4 — Language functors (codegen)

### 4.0 Compile checks + CI toolchains (two deliverables) — L9 at the boundary
- **What:** (a) NEW DB-free generated-fixture compile checks: go build, tsc
  --noEmit, javac, kotlinc, zig build-obj, python type-check over freshly
  generated fixtures for every language-mode — ALL SIX mandatory, no
  "where feasible"; these are the FIRST-EVER validation for four of the six
  languages (today's conformance suite covers Python+TS only). The known
  illegal-Java output (multiple public types per file in java_jpa AND
  java_types AND java_constraints) is fixed IN THE SAME CHANGE as the javac
  check lands — main never red. (b) CI toolchain provisioning so the
  existing DB conformance suite stops self-skipping — including
  python3/psycopg/pytest and node/npx (it skips on those too, not only the
  DB).
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
  invalid. go_constraints reworked fully: it gains the schema-package
  import it currently lacks, switches move to .String(), and the NOT-NULL
  zero-value check (== "") is redesigned for the struct type. Python:
  parse() classmethod as ergonomic typed alias (native Enum.__call__
  already validates); query-layer + validator signatures enum-typed; the
  query-layer package gains real enum imports/definitions; the ROW
  DATACLASS gains __post_init__ coercion (idempotent) covering BOTH
  PgBackend AND InMemoryBackend; _constraints.py explicitly unchanged. TS:
  keep the literal union; add parse() at boundaries (transition maps
  already typed). Java/Kotlin: value-based parse — fromValue() is NET-NEW
  (only getValue() exists today); JPA gains a generated AttributeConverter
  (@Convert) backed by getValue()/fromValue() — never @Enumerated(STRING);
  java_jpa, java_types, java_constraints move to MultiFileGenerator (one
  public type per file); JPA gains its missing enum-column branch. Zig:
  wrapper struct + parse; transition maps re-keyed. sqlalchemy: upgraded
  from sa.Enum(string literals) to sa.Enum(PyEnumClass) — requires the
  generator to gain enum imports/definitions it has never emitted. drizzle:
  NO change (already pgEnum-typed). Constants mode unchanged. Constraints
  validators re-target the branded representation (Go .String() switches;
  Java contains(getValue()); Kotlin equivalents).
- **Why:** The same validating-boundary discipline as L7's opaque Revision,
  applied per language: invalid values cannot be named (compile) or
  smuggled (every ingress validates), with the DB CHECK as backstop. The
  per-language mechanics are boundary-empirical — chosen by what each
  language can express, verified by 4.0.
- **Verify:** Under 4.0's compile checks: invalid values fail at the
  earliest boundary with errors; Go all-ingress round-trip; go_constraints
  compiles and validates against branded fixtures; Java persisted value ==
  getValue(); Python both backends yield enum-typed fields, pickle
  round-trips; TS exhaustive switches compile; sqlalchemy models carry
  sa.Enum(PyEnumClass).

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
  branded fields, sqlalchemy sa.Enum(PyEnumClass) columns, serve payload-key
  changes (envelope). Consumer todos filed at THE release with regeneration
  + adaptation notes (Python raw-string construction -> parse()/members; Go
  string-literal comparisons -> branded type; TS parse() at boundaries; JPA
  converters; Java layout) — each todo contains a SCRIPTED
  `pgdesign codegen --check` invocation (the mechanical half of boundary
  item 10).
- **Why:** All consumer-visible changes, one break, one adaptation, honest
  handoff.
- **Verify:** rlsbl changelog coverage passes with breaking entries;
  consumer todos filed with the scripted check; consumer drift-checks
  passing is the consumers' half (boundary item 10).

## Phase 5 — The database functor (migrate)

The chain category (kernel 1.4) instantiated on disk, and apply made a real
functor with a recoverable trace. Design gate first. LAND ORDER (regrouped so
the apply loop is rewritten ONCE, per the collapse-multi-pass rule): 5.0 ->
5.1 -> 5.2; then TRACK A: {5.5 + 5.7 TOGETHER — the single apply-loop pass:
precondition -> execute -> journal} -> 5.6 -> 5.8; TRACK B (parallel): 5.3 ->
5.4; 5.9 after 5.2 (it deserializes head manifests, which exist only once
5.2 creates migrations/revisions/); 5.10 after 5.3. Nothing ships mid-phase
(single-release axiom).

### 5.0 Schema and format design (design gate)
- **What:** Complete designs before implementation: pgdesign_migration_ops
  (op identity: migration ref, phase, sequence, op kind, target; serialized
  down-op; intent/confirm status), pgdesign_applied_migrations (ALL FOUR
  columns serve reads — version, applied_at, description, checksum — with a
  DEFINED applied_at derivation: edge-completion journal time FOR
  POST-UPGRADE edges; prefix rows migrated by `migrate upgrade` preserve
  applied_at AND checksum VERBATIM from the old table, or the upgrade's own
  ASSERT-view-reproduces-snapshot step would fail on its own columns),
  pgdesign_chain_position (current revision, in-progress edge ref,
  per-database boundary), and THE EDGE ARTIFACT — decided: ONE file per
  edge (content-derived filename: edge-content hash prefix + slug) carrying
  from/to revisions, the op list referencing store objects by id, and the
  display slug — there is no separate "migration file" vs "chain-edge file";
  one concept, one artifact. Store roots
  (migrations/objects/, migrations/revisions/ — manifests as kind-qualified
  sorted maps), archive layout (migrations/archive/). THE PATH-FINDER
  specified here as a DETERMINISTIC TOTAL RULE: apply performs reachability
  search from chain_position's revision (through the unified remap table)
  to a head, ARCHIVE-INCLUSIVE, choosing the SHORTEST edge-count path with
  consolidation-edge preference as the tie-break; OVERLAPPING consolidation
  ranges are FORBIDDEN AT CREATION (a cheap structural invariant that
  eliminates the ambiguous nested/overlapping cases outright) — a named
  algorithm replacing today's flat semver sort. A COMMAND-SURFACE
  DISPOSITION TABLE covering every existing migrate flag and subcommand:
  generate --version (obsolete — content-derived identity; flag removed),
  plan/generate --db (dropped for generation, per 5.9), apply --dry-run
  (retained; previews the path-finder's chosen edges), rollback --to
  (retargeted to a revision, resolved via journal + remap), migrate test
  non-shadow (retained; replays edges), status (pending = chain enumeration
  via the path-finder), apply --timeout (DEAD today — registered, never
  read; deleted in 0.6), baseline --adopt (PHANTOM today — named in an
  error message, never registered; deleted in 0.6). The two divergent
  tracking write paths (state.go helpers vs inline SQL; RemoveMigration is
  dead code) reconcile onto one. Labeled honestly: a human design gate with
  one mechanical check.
- **Why:** 5.2 migrates rows INTO these schemas and apply navigates the
  chain by this algorithm; designs precede the implementation order
  (planning discipline).
- **Verify:** Design fixtures round-trip through the 1.1 encoder; schema
  DDL fixtures reviewed before 5.1 starts.

### 5.1 Self-contained ops via the store — L1+L2
- **What:** Every pointer-def op REFERENCES its target object + the
  transitive composition-closure of type definitions BY CONTENT ID
  (objstore). All THIRTEEN families: nine pointer-def + RawSQL +
  PartitionChildSpec + ParentTable + the partman-config ops (whose apply-
  time rendering 0.6 fixed). RawSQL/DML bodies are content-addressed opaque
  blobs (satisfies by-content-id; the no-lossy-mirrors rule scopes to
  structured payloads). DOWN-ops identical treatment. Comment-stub no-ops
  and wrong-object fallbacks (deny-mutation / append-only) DELETED;
  sequences keep parameters; opCreateTable passes op.PGVersion (hardcoded 0
  today) and resolves enum/domain qualification from the closure.
  Invertibility DECLARED per op kind (kernel 1.4's type). Table-driven
  round-trip test per family — up AND down — on a fixture with an enum
  column, a domain column, and a version-gated generated column, asserting
  rendered SQL equals generate's.
- **Why:** L1 totality for ops (a migration that renders wrong SQL — empty
  or the WRONG OBJECT — is the worst artifact possible; today actual for
  several families); L2 keeps ops thin, reviewable, deduplicated.
- **Verify:** Round-trip table test covers all thirteen families up and
  down; fallbacks gone; the mixed fixture renders byte-identically.

### 5.2 Chain on disk + `migrate upgrade` — L2+L5+L8
- **What:** Chain edges one-file-per-edge in migrations/chain/
  (content-derived names); revision manifests in migrations/revisions/;
  head/find-heads via kernel 1.4; the 5.0 path-finder wired into apply.
  Discovery/ordering rewritten off semver. PREREQUISITE WIRED HERE:
  introspect-side filtering of the three managed tables (0.4's predicate) —
  the upgrade's own reconcile gate would otherwise refuse itself on the
  tables it creates in the same transaction. `migrate upgrade` (one-time,
  explicit): requires clean schema files per git when in a repo (stated
  caveat outside); acquires THE session-level advisory lock (shared with
  apply/rollback/baseline; concurrent-apply-during-upgrade is a verify
  case); content-addressed file writes land idempotently BEFORE the DB
  transaction; then ONE transaction: snapshot old applied set -> create
  journal/view/position -> migrate tracking rows -> ASSERT view reproduces
  the snapshot -> DROP old table -> COMMIT (sole commit point; the reverse
  window is harmless BY L2 idempotence, stated as a property; on-disk state
  reconciles from chain position on next run). Verify-then-stamp: clean
  TOML<->DB reconcile (1.2) or refusal with the drift report; per-database
  boundary stamped into chain_position. CHECKSUM AMNESTY, explicit: the
  prefix fold compares each file's bytes against the database's recorded
  checksum and emits a NAMED AMNESTY REPORT for mismatches (historical
  post-apply edits are a known legitimate state; the fold proceeds by
  content, the report preserves the evidence — never silent, never
  blocking). Multi-database rule: synthetic-prefix revisions are
  per-database stamps; shared prefix files are the union; databases at
  different boundaries are supported. Existing semver files become the
  linear prefix with synthetic checksum-verified revisions. serve updated
  (BEFORE phase 8's rework — the files are co-edited): handleMigrations
  repointed to the view; version endpoint updated for the new naming.
  Store<->chain<->files consistency check = Merkle closure PLUS
  edge-endpoint consistency (simulate each edge's ops; assert
  from-manifest -> to-manifest); 6.2 and 7.2 invoke the same checker.
- **Why:** L3 needs the chain physically; L8 dictates the choreography
  (assert-before-DROP; lock; idempotent-files-then-atomic-commit); L5's
  verify-then-stamp makes the boundary a verified fact, not an assertion.
- **Verify:** Crash injection both sides of COMMIT (boundary item 2);
  dirty-tree refusal; mid-edit TOML cannot stamp; drift report on unclean
  reconcile; amnesty report on historical checksum mismatch (fold
  proceeds); consistency check red on tamper AND on an edge whose ops
  don't map from->to; concurrent apply blocked; upgrade's reconcile does
  NOT flag the just-created managed tables.

### 5.3 Squash = composition — L3+L4
- **What:** Consolidation = an ADDITIONAL chain edge; superseded files
  retire intact to migrations/archive/, reachable via their edges
  (mid-range databases apply remaining originals via the 5.0 path-finder).
  Consolidation downs: by manifest diff for fully-mechanically-invertible
  ranges; ranges containing declared-inverse/DML ops compose the
  originals' recorded downs (vacuous where declared so — L4's three-way
  type decides, no runtime judgment). Consolidations PRESERVE every
  DML/RawSQL op of the superseded path (no drop, no fold-across — a
  structural side-condition on op-lists; schema-commutation cannot see the
  data divergence folding would cause for fresh databases). The op-list
  optimizer is specified as a TERMINATING REWRITING SYSTEM: cancellation
  carries the side condition "no intervening op REFERENCES the cancelled
  object" — where the references relation spans the full dependency
  surface (RefTable/RefCols, trigger function, view/function bodies,
  depends_on; DML ops reference their tables); each rule strictly
  decreases a stated measure (termination); critical pairs are enumerated
  across ALL THREE rule classes — the 12 cancellation pairs, sequential
  type-change merging, and create_table folding (the cross-class pairs are
  the interesting ones) — and both resolutions tested to converge (modelgen
  supplies inputs) — termination + local confluence gives UNIQUE NORMAL
  FORMS (Newman), which is what makes consolidation well-defined. Named gaps
  the rewrite fills: no cancellation pairs exist today for exclusions,
  views, matviews, sequences, composite types, domains, or policies. The
  references relation is only computable AFTER 5.1's content-addressed
  closure (the fields it spans live inside today's unserialized pointer
  defs) — the stated reason for the 5.1 -> 5.3 land-order edge.
  SQUASH-COMMUTATION (the L5/L10 functor equation) is a named test:
  apply(consolidation) and apply(sequence) land on the same introspected
  schema-state. The rollback-equivalence invariant is STRUCTURAL (revision
  equality says nothing about data). Tracking/journal lineage handled; no
  orphaned rows; files never rewritten.
- **Why:** L3 makes squash a checked normalization, not a definition; L4
  makes the data-loss hole (a DOWN recreating a dropped column empty)
  unrepresentable; the DML-preservation side-condition closes the UP
  direction the commutation test cannot see; the rewriting-system spec
  replaces "we hope pass order doesn't matter" with a finite, decidable
  check.
- **Verify:** Squash of applied migrations via consolidation; mid-range DB
  resumes via archived originals; SQUASH-COMMUTATION on the comprehensive
  fixture; rollback-equivalence on structural AND merged-type-change
  fixtures; a DML-containing range takes the composed-downs form BY TYPE
  and PRESERVES its DML ops; critical-pair convergence suite green; the
  orphaned-index fixture (add/index/drop) refuses cancellation; an
  FK-RefTable critical pair converges; no orphaned rows.

### 5.4 Unconditional checksums (apply surface) — L2
- **What:** After 5.2/5.3: checksum verification unconditional ON APPLY —
  including archived-original applies. Mismatch = corruption, hard error
  naming the file. Prefix files' synthetic revisions checksum-verified.
  (No rollback surface exists post-5.6: rollback reads no files.)
- **Why:** Under L2, a mismatch has exactly one meaning; enforcing before
  the format existed would have bricked users (the adoption path is 5.2,
  whose amnesty report handles pre-existing historical mismatches).
- **Verify:** Tamper tests on active and archived files refuse apply with
  precise reports; upgraded fixture applies cleanly.

### 5.5 + 5.7 The apply-loop pass: preconditions + journal — L5+L8+L1
(One pass over the apply loop: precondition -> execute -> journal. Two
subphase numbers retained for reference; they land together.)
- **What (5.7 preconditions + predicate IR):** Per-op-class predicates
  against pg_catalog (absent for creates; present-and-matching via N/≈_syn
  — with the ≈_pg residue rules on introspected text — for alters/drops);
  unexpected state = hard error naming object/expected/found. DML ops
  precondition-free (arbitrary SQL has no catalog precondition). IR =
  structured data in internal/predicate; the Go executor consumes the NEW
  shared scoped-catalog-query layer `internal/catalog` (extracted for this
  purpose — introspect's ~45 private bulk extractors are the wrong
  granularity; introspect adopts the shared layer where natural); only the
  pgx executor lives in migrate; the SQL renderer compiles the same
  structures into DO-blocks for generate --idempotent (RAISE on mismatch —
  4.3's breaking notes). CI conformance matrix: both backends + the differ
  where classes overlap, against live states AND generated expressions,
  identical verdicts.
- **What (5.5 journal):** pgdesign_migration_ops + pgdesign_applied_
  migrations per 5.0 (one write path). Records op identity AND serialized
  down-op (via the store; RawSQL/DML downs as opaque blobs). TIMING:
  transactional ops journal INSIDE the op's transaction; non-transactional
  ops (create AND drop index concurrently; version-conditional enum-add)
  use INTENT-then-CONFIRM rows with class-specific resume protocols defined
  in Postgres's own state model: resume of an unconfirmed create-index
  intent checks pg_index.indisvalid (an interrupted CIC leaves an INVALID
  index that IF NOT EXISTS would skip forever) and drop-rebuilds;
  drop-index gains IF EXISTS; enum-add is already idempotent. (Fix BOTH
  wrong version comments: sql.go's CIC+IF NOT EXISTS claim and
  sql_gen.go:554's "9.3+" for ADD VALUE IF NOT EXISTS — it is 9.6+.) The
  same protocols govern rollback of non-transactional down-ops.
  chain_position updates in the same transaction as each edge-completing
  journal write — DEFINED: the transaction containing the edge's FINAL
  op's confirm row (an edge spanning expand/migrate/contract phases or a
  non-transactional breakout completes in whichever transaction confirms
  its last op). A batched backfill journals as ONE op with one down
  (consistent with the single-DownOp model). Re-apply resumes by skipping
  confirmed ops. AppliedVersions/status/serve read the view.
- **Why:** L5's domain check and trace, landed as ONE rewrite of the loop
  (the collapse-multi-pass rule); L8 closes the crash window INSIDE the
  recovery protocol; L1's single ≈_syn via the shared normalization; the
  shared catalog layer prevents a second divergent set of pg_catalog
  queries — the divergence bug class 0.4 exists to kill.
- **Verify:** DB-backed precondition matrix per op class (wrong-type
  column, missing table, mismatched constraint — each precise); golden
  idempotent SQL; mismatch RAISEs, match no-ops; conformance matrix green;
  shared catalog layer by import graph; fault-injection matrix (boundary
  item 1): mid-phase; after CIC (resumed index is VALID); after drop-CIC;
  around enum-add on both PG classes; view semantics equal the old
  applied-set; single write path by grep.

### 5.6 Journal-driven rollback (scoped) — L5+L4
- **What:** Rollback executes recorded down-ops in reverse journal order —
  files never consulted. RED-GREEN FIRST for the live baseline data-loss
  bug: rollback today loads version+".toml" and runs its down-ops even for
  a baseline row that applied nothing — executing DROPs against objects
  pgdesign never created. MID-EDGE semantics: when chain_position shows an
  in-progress edge, reverse confirmed ops (class-specific protocols for
  unconfirmed non-transactional intents); the reversibility pre-check runs
  against JOURNALED ops, not file ops. SCOPE: guaranteed from the upgrade
  boundary forward; pre-upgrade prefix + baselines ROLLBACK-FROZEN
  (crossing = hard error naming the boundary).
- **Why:** L5: rollback inverts recorded reality, never assumed intent
  (today it trusts files absolutely — the DROP-COLUMN data-loss case, and
  the baseline case is its live instance). L4: only journaled invertible
  ops have inverses to run.
- **Verify:** The baseline-rollback red test goes green (frozen, refuses);
  rollback after partial apply drops nothing it didn't create; works with
  files archived; mid-edge rollback incl. an unconfirmed CIC intent;
  boundary-crossing refuses; journal-based pre-check tested.

### 5.8 Post-apply reconcile — L5+L10
- **What:** After apply: introspect (0.4 exclusions; canonical via 0.2) +
  N-normalized diff (with the ≈_pg residue rules on the introspected side)
  against the target model; residual mismatch = hard error listing every
  object. Designed as a FOLD of 5.7's per-object predicate comparator plus
  an orphan check (one comparison unit shared with the conformance matrix,
  not a second full-model comparison path). Reconcile does not auto-add
  imported schemas. SM-vs-enum introspection lossiness documented. The L10
  ROUND-TRIP as a randomized DB-backed property test with SPLIT ORACLES per
  L10: the manifest oracle (recorded to-revision manifest, object-by-object)
  over the UNRESTRICTED modelgen generator — SM types get randomized
  coverage; the re-introspection oracle over the injective, bridge-proven
  fragment only.
- **Why:** L5's codomain check; L10's caveat makes the dual oracle
  necessary (introspection alone passes vacuously where it is lossy, and
  bridge gaps would spurious-red a correct apply); the predicate-comparator
  fold keeps one comparison engine.
- **Verify:** Clean apply over the comprehensive fixture reports empty;
  out-of-band ALTER mid-migration surfaces; managed objects invisible; the
  dual-oracle L10 property test green over generated pairs.

### 5.9 Pure generation — L5
- **What:** migrate generate = diff(deserialize(head manifest via
  objstore), current model) — pure, no DB. ALWAYS emits large-table-safe
  forms (NOT VALID + VALIDATE; backfill-then-set-not-null;
  expand/contract phasing); QueryTableStats and generate-path stats
  plumbing deleted. The EXPAND_CONTRACT_TYPE_NARROW advisory is RELOCATED
  to diff classification WITH A STATED BEHAVIOR CHANGE: its row-count gate
  is dropped (diff has no row counts) — it becomes narrowing-always, chosen
  deliberately. `migrate plan` is ASSIGNED here: it becomes the pure
  preview of pending chain edges (drift preview is diff --live's job).
  Drift caught at apply; adoption via baseline (5.10).
- **Why:** L5: generation never reads the world — same TOML edit, same
  migration, regardless of DB state; the always-safe form is what makes
  that possible (a manifest has no row counts).
- **Verify:** Generation without any DB; FK add emits two-step NOT VALID
  with no DB; a drifted DB does not alter output but fails apply; stats
  plumbing gone; the advisory fires on narrowing (gate change asserted);
  plan output is pure and DB-free; diff MINIMALITY as a quality property
  (mutation test: delete any generated op, the L10 oracle must fail —
  non-normative).

### 5.10 Fork resolution, re-keying, baseline + ecosystem alignment — L3+L2
- **What:** `migrate rebase <head>`: re-parents a fork's tail, recomputes
  revisions, re-derives manifests; rebased-away edges RETIRE to
  migrations/archive/ (never rewritten or deleted); the UNIFIED
  revision-remap table (per L2 — ONE mechanism for rebase and rekey) is
  written so databases stamped at rebased-away revisions are SERVED, not
  orphaned (apply consults the remap before declaring a position
  unreachable). `migrate rekey` (L2's epoch protocol): TOTAL-scope
  re-encode — store objects, manifests, AND chain-edge files under the new
  codec, writing the same unified remap — triggered by golden-corpus
  CI-red (boundary item 12). BASELINE post-chain: synthesizes a revision
  manifest FROM INTROSPECTION (its attachment specified: a genesis-parented
  edge carrying the introspected manifest), writes chain_position, and its
  two semver guards are re-expressed EXPLICITLY: the divergence check
  becomes "the stamped position is chain-reachable"; the out-of-order guard
  (meaningless under content ids) becomes "the baseline target is reachable
  from the stamped position." `migrate test --shadow`
  replays EDGES (not files). Docs updated — MECHANIZED verify: grep-assert
  zero retired vocabulary (semver filenames, file-trusting rollback) and
  every guide-named command exists in CLI registration.
- **Why:** Two branches each appending an edge is normal; detection
  without resolution is a dead end; a rebase that orphans stamped
  databases merely relocates the dead end; history cannot be regenerated,
  so codec changes need the sanctioned re-key; baseline is the adoption
  path for intentional drift and must produce a chain-valid position.
- **Verify:** Fork fixture: rebase re-parents, revisions recomputed, store
  consistent, archived edges reachable via the checker, and a database
  stamped at a rebased-away revision APPLIES FORWARD via the remap; rekey
  fixture: corpus shift -> rekey -> consistency check green under the new
  epoch, old-id map complete; baseline fixture: manifest-from-
  introspection attaches, position written, reachability guards fire;
  shadow test passes on the comprehensive fixture; the doc greps pass;
  full migrate suite green.

## Phase 6 — Orchestration and provenance enforcement

### 6.1 pgdesign revise — L5+L6
- **What:** PURE tier: build planner + 5.9 generation + PURE checks — the
  NF audit's pure core (audit.Audit — SPLIT REQUIRED: today's checkNF
  resolves a DB URL, connects, and runs FD discovery, and never blocks;
  the split yields a pure blocking core for revise and a DB-tier
  discovery wrapper; generate's --strict-nf gate is the behavior
  preserved) and structural workload — all BLOCKING. DB tier (phase-2
  env): live import verification (7.4) + LIVE analyses (FD discovery,
  pg_stat) — NON-RETROACTIVE (fail loudly; the committed migration
  stands; the next revise incorporates fixes). Chain head from the chain
  files; two heads = hard error naming both + pointing at migrate rebase;
  genesis handled. Separate safegit commits (pure outputs; then
  migration+chain+store) via ONE shared commit helper; commit failure =
  hard error — build's warn-and-continue flipped in the same pass.
  Partial failure keeps committed pure outputs, exits non-zero naming the
  skipped tier.
- **Why:** The forgotten-step failure mode dies in one command without
  eroding the seam: with L5's pure generation, even the migration is
  pure, so the DB tier is exactly the genuinely-live work — and the
  checkNF split is what makes "pure tier" true rather than aspirational.
  Commit-before-DB-tier is sound: the migration is repo-level and pure;
  per-database applicability is re-checked fail-closed at apply.
- **Verify:** End-to-end: edit -> revise -> outputs + chained migration +
  two commits, one revision everywhere; a BCNF violation blocks the pure
  tier (via the split core); DB-unreachable keeps pure outputs, non-zero,
  names the skipped tier; two-head fixture points at rebase;
  commit-failure hard-errors.

### 6.2 Revision enforcement — L6
- **What:** Invariant (derived, not legislated): all regenerable
  planner-set artifacts carry the ONE full-project revision after any
  write. Writer taxonomy — FOUR classes, enumerated totally: FULL
  regenerators (build, revise) always allowed; PARTIAL writers — exactly
  one exists (codegen --output) — refuse when non-rewritten siblings
  differ, and the taxonomy PRE-COMMITS that any future file-writing
  generate mode registers as full-or-banned; SOURCE-EDITING writers (fmt)
  are outside the invariant but CHANGE the revision — they print a
  follow-up notice and the check catches staleness; SCAFFOLDING writers
  (testdb init — language wrappers + CI workflow; introspect --output — a
  NEW candidate source file at an arbitrary path) are outside the
  invariant, with the note that ADOPTING introspect output as project
  source changes the revision. The partial-writer refusal and the
  revision check regenerate through the SAME per-output filters from
  [output] config (0.5's unification). Outside the invariant, stated:
  migrations + chain + store (append-only; covered by INVOKING 5.2's
  consistency checker — closure AND edge-endpoint), seed output (stamped,
  unenforced provenance), stdout (check-time only). Missing/old-format
  stamps = stale. The revision CHECK (error severity): chain/store
  integrity via the shared checker, cross-artifact stamp agreement,
  standalone artifacts. genkit stamp-extractor complements byte-compare
  (stamp says "a sibling is at a different revision"; bytes say "this
  file isn't what the model produces").
- **Why:** L6: divergence is created by partial writes and source edits,
  resolved by full ones; the taxonomy's pre-commitment only works if the
  initial enumeration is actually total — hence six named classes (the
  four writer classes plus seed's stamped-unenforced class and the
  checker-covered append-only stores), not a four-class list with a shadow
  "outside" bucket.
- **Verify:** TOML edit then build succeeds; then codegen --output of one
  output refuses naming stale siblings (group-filtered fixture); fmt
  prints the notice and the check goes stale; testdb init and introspect
  --output never flagged; tampered header caught; chain violation caught
  via the shared checker; seed/migrations/stdout never flagged.

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
- **What:** `import lock`: resolve the pin (git URL + ref; git plumbing —
  boundary item 13; no DB), parse the framework's TOML, vendor the
  surface into imports/<alias>/ via THE SAME objstore package (one
  package, multiple roots): referenced tables + transitive
  composition-closure of type definitions, each with its per-object id,
  plus a lockfile entry (URL, ref, resolved commit, surface hash).
  `import update` re-pins. `check --tag imports`: re-derive and report
  SEMANTIC drift at column level via N (surface ids alone would
  false-drift on equivalently-spelled defaults — the 1.2 dependency is
  real), hard-failing CI — built on the same store-integrity primitive as
  5.2's checker. Requirements: extensions inferred per referenced object;
  pg_version floor carried (consumer re-declares >=).
- **Why:** The import surface IS a sub-model under L1's encoder and L2's
  store — reproducible offline builds and column-level semantic drift
  fall out of the kernel rather than needing their own machinery.
- **Verify:** Two-project fixture: drifted column type -> exact
  column+FK error; an equivalently-spelled default does NOT drift (N in
  the loop); unreferenced changes silent; offline build; per-object ids
  stable; enum closure usable.

### 7.3 Model integration — fail-closed union
- **What:** ImportedTables split slice. Union wired at the COMPLETE
  enumerated resolution sites — SEVEN: buildTablesByName (E204/TableByName
  — without it FK validation, migrate FK qualification, and check C104
  break; C104 today silently SKIPS unresolved refs), BuildFKGraph (edges
  keyed (schema,name), Imported=true), seed FQN pools, the D2/GraphQL
  edge emitters (both emit edges by target-name string; D2 drops
  fk.RefSchema — fixed here; GraphQL gains qualification + a golden), AND
  W002 orphan detection (it builds its referenced-set from raw
  RefSchema+"."+RefTable strings, bypassing TableByName — imported-FK
  targets would spuriously orphan), the C103 orphan check (structurally
  identical to W002 in a separate code path — ONE union-aware
  orphan-detection helper serves both, replacing two divergent raw-string
  scans), AND the I002 dead-column referenced-set (matches bare RefTable,
  ignoring RefSchema entirely). Two further resolution sites are fail-safe
  only by accident (model/topo and format's pre-Build topo — TopoSort
  silently drops unknown deps) — pinned with comments stating why they
  are safe. Registry collisions = hard error naming both sources; imported
  enums usable; extension/pg_version re-declaration enforced.
- **Why:** Fail-closed by construction — consumers iterating Tables are
  correct BY OMISSION — but only where resolution funnels through the
  union; the five bypass sites are named because each otherwise produces
  spurious errors, phantom nodes, dangling seeds, dangling edges, or
  false orphans.
- **Verify:** NO spurious E204 on imported FKs (explicit test); NO
  spurious W002 orphans; FKGraph nodes keyed and flagged; seed resolves
  imported pools; D2 AND GraphQL edges schema-qualified (goldens);
  DDL/audit/codegen outputs contain zero imported artifacts; collision
  and re-declaration tests.

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
  cfg) — used by build/codegen/revise/serve, RECONCILING what serve's
  own loader skips today: config [[extensions]] registration (serve uses
  the builtin registry only), pg_version resolution, and group/source
  filters. The /schema endpoint gains a PROJECT-MODE BRANCH (it only
  introspects today — the DB-free path does not exist and is built
  here), returning THE canonical envelope function's output (kernel
  1.5): revision + FKGraph projection (0.3) + diagnostics wrapped.
  ORDERING: 5.2's serve edits land first; 8.1 routes them through
  internal/project (co-edited file — NOT parallel with 5). Nil-registry
  SM-drop fixed. DB-only endpoints degrade explicitly.
- **Why:** The compiler/live seam made real; the endpoint is literally
  the same function as the json output, so it can never drift (L1).
- **Verify:** serve starts without a database and answers project-mode
  /schema byte-consistent with generate json (incl. diagnostics); SM
  diagrams render; DB-only endpoints degrade explicitly; extension and
  pg_version handling matches build's on the same project.

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

- Phase 0 internal: 0.1 ∥ 0.2 (disjoint code; goldens in one sweep);
  0.2 -> 0.3; 0.4/0.5 after 0.2; 0.6 anytime (0.6's partman apply-time fix
  precedes 5.1's absorption of the family: 0.6 -> 5.1). strictcli todo
  filed at phase-0 start (boundary item 11; phase 2 is an EXTERNAL
  milestone gating {6.1, 7.4, seed tier-1}; phase-5 commands landing
  before phase 2 are swept by 2.2 and locked by 2.1's registration error
  thereafter).
- 0 -> {1, 2, 9.1-config-half}; 0.1 -> 4.1; {0.1, 1.4, 1.5} -> 4.2;
  4.0 precedes 4.1's verify (Java fixes land WITH the javac check);
  4.1+4.2 -> 4.3; 4.2 -> 6.2.
- Kernel: 1.1 -> {1.4, 1.5, 5.0 (design fixtures round-trip through the
  encoder), 5.1, 7.2}; 1.2 -> {1.4-conformance, 5.2, 5.5+5.7, 5.8, 7.2
  (semantic drift needs N)}; 1.3 -> {5.1, 5.2, 7.2}; 1.4 -> {5.2, 5.3,
  5.9, 6.1}; 1.5 -> {4.2-json, 8.1}; 1.6 -> {1.1, 1.2, 1.4, 5.3, 5.8};
  {1.1, 1.3} -> 5.9 (deserialization); 0.2 -> 7.2 (surface-hash
  reproducibility rests on Canonicalize). Phase 1 internal: {1.3, 1.6} ->
  1.1 -> {1.2, 1.5} -> 1.4.
- 0.3 -> {7.3, 8.1-projection, 9.3}; phase 5 land order: 5.0 -> 5.1 ->
  5.2; then {5.5+5.7 as one apply-loop pass} -> 5.6 -> 5.8 (track A) and
  5.3 -> 5.4 (track B, parallel); 5.9 after {5.2, 1.4} (head manifests
  exist only once 5.2 creates migrations/revisions/); 5.10 after 5.3.
- {5.9, 1.4, 0.5} -> 6.1; {5.2, 4.2, 0.5} -> 6.2 (the whole-phase edge is
  deliberately split — 6.x does not wait for 5.10); 5.7 -> 7.4; 7.4 ->
  9.2; 5.2-serve-edits -> 8.1 (co-edited file — phases 5 and 8 NOT
  parallel); 8 -> 9.1-serve-half.
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

## Out of scope, pending their own design work

Test schema mode. N-project topology. Manifest + per-language linter
ecosystem (evidence-gated). Recorded summit alternatives: declarative
catalog reconciliation for migrate (the kernel's L5 machinery is its
stepping stone); structural semantics/metadata split in the model (the
encoder's semantic-only policy already produces its bytes); registry
materialization into Schema as sole type-truth; extension-DDL-name
resolution baked into the model; DB/boot-time revision binding; the
reverse conformance invariant as primary (activated once the differ fully
adopts N); RENAME-HINT OPS (renames are delete+add today — documented);
LIVE ROUND-TRIP NORMALIZATION as the exactness alternative to the ≈_pg
residue rules (round-trip desired-side expressions through the DB on live
paths — exact where the rule set is documented-incomplete); THREE-WAY
MODEL MERGE (pushout over a common-ancestor revision — per-object join
with change/change conflicts detected by id inequality against base; the
kernel makes it nearly free) as the recorded alternative to rebase-only
fork resolution.

## Effort

Phase 0: 2-3 sessions. Phase 1 (kernel): 3-4 sessions (incl. modelgen) —
pure Go, property-tested, no DB; front-loaded because everything else
adapts it. Phase 2: 1-2 (externally gated). Phase 4: 3-4 (incl. 4.0's two
deliverables). Phase 5: 4-6 (the chain/invertibility/store machinery lives
in the kernel; the apply loop is rewritten once). Phase 6: 1-2. Phase 7:
3-4. Phase 8: 1 (after 5.2's serve edits). Phase 9: 2-3. Parallelization
per the DAG.

Release: exactly ONE rlsbl release at the very end (owner axiom);
everything accumulates unreleased; consumer todos filed at that release.
No intermediate state can reach a consumer.
