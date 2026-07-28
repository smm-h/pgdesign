---
title: "Architecture: The Laws"
description: "The algebra pgdesign is built on: its primitives, the ten laws (L1-L10) that govern the kernel, the boundary doctrine, and the ruled-out designs."
---

# Architecture: The Laws

pgdesign rests on a small algebra — a content-addressed object model wrapped
around a category of schema migrations, governed by one equivalence relation.
Every defect class the system guards against is a violation of one of the laws
below, so those defect classes are eliminated by construction rather than
case-by-case. Any future proposal is judged the same way: which law does it
implement, check, or violate?

This page is the durable statement of that algebra. It was extracted from the
build roadmap when the roadmap completed (2026-07); the as-built deviation
records — where the shipped implementation diverged from the original plan, and
why — live in the retired roadmap under `todo/.done/` for archaeology. This page
carries the design; that file carries the history.

---

## Part I — The algebra

### Objects and primitives

- **Model**: the fully-resolved schema IR (tables, views, matviews, functions,
  sequences, types, extensions, pg_version, comments, groups — everything that
  determines generated artifacts, DDL and beyond).
- **enc**: the canonical per-object encoder, Model-object -> canonical bytes;
  **decode** is its inverse on canonicalized models (`decode∘enc = id` is a
  checked property — deserialization paths depend on it, so decodability is
  load-bearing). Every encoded form carries a CODEC-VERSION (epoch) field — ids
  are epoch-relative (see L2).
- **N**: the normalizer (types, defaults, expressions via parse/deparse, plus
  the catalog-independent foldings both directions — `IN` <-> `= ANY(ARRAY[...])`,
  array-literal forms, and cast-type-name aliases via the typeinfo alias map
  (`x::int4 ≡ x::integer` — deparse verifiably does NOT normalize pg-internal
  alias names in casts) — applied to BOTH sides always); **≈_syn** := the kernel
  of N (`a ≈_syn b` iff `N(a) = N(b)`) — an equivalence relation BY CONSTRUCTION.
  **≈_pg** := Postgres's semantic equality — a distinct, richer relation the
  system does not compute (see L1).
- **hash**: SHA-256; **id** = `hash(enc(x))`; **revision** = id of a whole-model
  manifest — a SORTED MAP of KIND-QUALIFIED KEYS (kind, schema, name, and for
  functions the argument signature — overloads are distinct entries; a table
  `x` and a function `x` never collide) -> object-id. Renames are delete+add at
  the MANIFEST level by construction, but GATED: plausible-rename detection —
  tables: deleted+added manifest keys with EQUAL object content-id; columns:
  within-table deep diff, content-equal-except-name — plus a declarative
  `[renames]` section; detected-but-undeclared = hard error, declared = a
  first-class mechanically-invertible rename op (the best L4 class). Store +
  manifests form a two-level Merkle DAG: manifest comparison is key-wise
  symmetric difference; the shared consistency checker IS Merkle closure
  verification (every id in every reachable manifest resolves in the store) PLUS
  edge-endpoint consistency (an edge's ops, simulated, map its from-manifest to
  its to-manifest); diff gains an O(changed-objects) fast path by comparing
  per-object ids before deep comparison.
- **store**: content-addressed map id -> bytes (put/get; puts idempotent).
- **chain**: parent-linked edges between revisions; an edge is a **migration**
  whose identity is CONTENT-DERIVED (its file is named by an edge-content hash
  prefix plus a human slug; a display SEQUENCE is derived from topology for
  listings, never stored as identity) — parallel edges and endomorphisms
  (pure-DML migrations, R -> R) never collide, and concurrent branches cannot
  race a counter.
- **diff**: `(Model, Model) -> Delta`. A Delta is a flat description of change,
  NOT a morphism: Deltas do not compose or invert; all composition happens on
  op-lists. diff's specification is L10; `diff(a,a) = empty` is a pinned test.
- **gen**: `Delta -> op-list`, the lowering whose contract IS L10 (a primitive,
  since the round-trip theorem specifies it jointly with diff); ops carry their
  L4 invertibility class and reference objects by content id.
- **apply**: the map from chain edges into the world (codomain defined in L5).
- **journal**: the durable trace of apply's actions, with recorded inverses.
- **stamp**: artifact -> revision-that-produced-it (provenance).
- **modelgen**: a pure random generator of valid Models (well-formed FK graphs,
  type closures, version-gated features) — the input source L9's property tests
  and L10's round-trip test require (the seed package generates row DATA and
  cannot serve this role).

### The laws (L1-L10)

The law numbering is stable and referenced from code comments — do not renumber.

- **L1 (One canonical form — with honest status tags).** N is the normalizer;
  ≈_syn is its kernel, an equivalence by construction. (a) enc encodes N-normal
  forms, so `enc(a) = enc(b)` iff `a ≈_syn b`; expressions enter normal form
  through Canonicalize N-normalizing expression fields into the IR (structural
  fields plus expression leaves). (b) Single-≈: every comparison engine
  (encoder, differ, predicates) computes ≈_syn — enforced by the conformance
  pair: revision-equal implies diff-empty AND diff-empty implies revision-equal;
  the latter direction's obligations include pg_version joining the diff, since
  under-reporting breaks precisely this direction. (c) Boundary conjecture:
  `≈_syn ⊆ ≈_pg`, KNOWN INCOMPLETE. The catalog-independent part of PG's
  rewriting lives INSIDE N (both sides — one-sided rewriting would false-drift a
  user who writes `= ANY(ARRAY[...])` directly). The residue is
  CATALOG-DEPENDENT cast materialization, unreachable by any pure normalizer; on
  live paths it is resolved by LIVE ROUND-TRIP NORMALIZATION — the desired-side
  expression is round-tripped through the target database itself (throwaway temp
  object + `pg_get_*` deparse), so PG computes its own canonical form: exact by
  construction, and it absorbs any folding N lacks. Identity NEVER consumes
  round-trip output (no DB exists on the pure path). A minimal forward-simulation
  rule set survives only where round-trip cannot reach (fixture-checked).
  Structural sublanguage: the ORDER-SEMANTICS TABLE (exhaustive over the Model,
  two columns per collection: collection order and intra-object order — columns
  and enum values semantic; composite-type fields, function args, partition key
  columns, FK column correspondence, and index key-column order semantic
  INTRA-object; checks, indexes, uniques, policies, triggers canonical-only as
  collections) is part of the format spec; that table IS the definition of ≈_syn
  on structure.
- **L2 (Content identity / extensionality).** `id = hash∘enc` (id equality
  implies content equality MODULO SHA-256 collision resistance — a stated
  assumption, since id-equality fast paths skip byte comparison; boundary item
  14); `get(put(x)) = x`; puts idempotent; identity location-free;
  `decode∘enc = id` on canonicalized models. Content ids are EPOCH-RELATIVE:
  every stored form carries its codec version, and a change to enc or N re-keys
  the world. Such epoch changes are RARE, DELIBERATE BREAKING-MAJOR-RELEASE
  EVENTS (a go-pgquery bump, or any deliberate change to enc/N semantics); the
  recovery tooling is written AT EVENT TIME, not pre-built. The codec-version
  field is what keeps the store SELF-DESCRIBING enough for that tooling to be
  written when needed. The revision-remap table in the chain is REBASE-ONLY
  machinery (L3) — consulted by apply and the consistency checker, so a database
  whose chain_position holds a rebased-away revision is served forward, never
  orphaned. Outside an epoch bump, mutation of STORE CONTENT (objects,
  manifests) is not an operation this structure has. Chain-edge FILES are
  location-addressed — their append-onlyness is CHECKED POLICY (the consistency
  checker, including its edge-endpoint check), not structural impossibility.
- **L3 (The chain is the free category on the edge graph).** Composition = path
  concatenation; identities = empty paths — VIRTUAL: never files, never applied
  (these laws hold trivially and are not what needs testing). The real content:
  (a) edge identity is content-derived — the hom-set question is answered
  explicitly, parallel edges and pure-DML endomorphisms are legal; (b) SQUASH
  SOUNDNESS — a consolidation edge is a NEW edge whose ops must be
  apply-equivalent to the path it supersedes. Under the CONCATENATION FORM (the
  op-list optimizer is out of scope) the op-lists coincide and equivalence holds
  by construction; the commutation test in L10 remains as a smoke check and
  becomes substantive only if an optimizer ever lands.
- **L4 (Three-way typed invertibility).** Every primitive op is typed:
  MECHANICALLY-INVERTIBLE / DECLARED-INVERSE (including DML ops whose declared
  inverse is vacuous — data is not restored; today's reversibility semantics,
  made explicit) / NON-INVERTIBLE. The inverse of a composite is the reversed
  composition of component inverses, defined WHEN every component has one. This
  is a deliberate conservative under-approximation: a composite can be
  semantically invertible when components are not (chained type changes whose
  endpoint diff yields a clean structural down) — the manifest-diff down is used
  ONLY for fully-mechanically-invertible ranges; elsewhere recorded downs
  compose. What remains unrepresentable: a manifest-diff down for a range
  containing data-bearing ops.
- **L5 (Apply is a functor on schema-states).** The codomain is named: objects
  are ≈_syn-classes of INTROSPECTED SCHEMA STATES; morphisms are observed
  transitions. Data is deliberately OUTSIDE the codomain — which is exactly why
  rollback equivalence is structural and why apply does not preserve inverses on
  data (the caveat is a codomain choice, not an inconsistency). Preconditions
  are the domain check ("the world is at R_from"); reconcile is the codomain
  check ("the world arrived at R_to"); the journal is the trace. Drift is a
  domain error — always loud, never absorbed. Generation is a pure function of
  revisions and NEVER reads the world. The functor equation —
  `apply(consolidation)` lands where `apply(sequence)` lands — is the named
  squash-commutation test.
- **L6 (Total provenance).** Every derived artifact carries the revision that
  produced it; regeneration is re-application of a pure function; freshness is
  extensional equality. All enforcement rules are DERIVED from provenance, not
  legislated case-by-case.
- **L7 (Model classes don't cross).** A model with type information and an
  introspected model without it belong to different model classes. Their
  revisions are values of distinct types; comparing them is a type error, not a
  runtime mismatch.
- **L8 (The trace is recoverable in the world's terms).** Every journal write is
  atomic with its effect, or wrapped in an intent/confirm protocol whose resume
  is idempotent IN THE WORLD'S OWN STATE MODEL (e.g. Postgres's invalid-index
  semantics), not merely in ours.
- **L9 (Verification is law-checking).** Kernel properties are checked by
  property-based tests over GENERATED inputs (modelgen — the generator is a
  deliverable, not an assumption): encoder totality (the reflection coverage
  guard, extended to the registry snapshot); `decode∘enc = id`; normalizer
  idempotence `N∘N = N` over a generated expression corpus;
  SHUFFLED-DECLARATION-ORDER convergence (≈_syn-equal inputs — permuted
  canonical-only collections — encode to identical revisions: canonicality, not
  mere repeatability); semantic-order collections are SLICES, never sorted maps
  (a semantic order accidentally modeled as a map would be silently destroyed by
  key-sorting); the L10 round-trip; and a GOLDEN CORPUS of normalized
  expressions committed as REGRESSION FIXTURES pinning N's behavior against
  pgdesign's OWN refactors of `internal/sqlparse` (an own-code change that shifts
  ≈_syn — hence identity — turns CI red and is reverted or handled as a
  deliberate epoch event); dependency bumps are foreclosed separately by the CI
  pin guard. The corpus's negative-space companion is the N-FOLDING BACKLOG: one
  committed XFAIL fixture per KNOWN-MISSING catalog-independent folding
  (`NOT IN` <-> `<> ALL`, single-element `IN` <-> `=`, `BETWEEN`, `LIKE` <-> `~~`,
  boolean redundancy, numeric-literal forms, `COALESCE` <-> `CASE`, commutative
  ordering), each asserting the CURRENT non-convergence — if deparse or an N
  refactor starts converging one, CI goes red and the entry graduates; entries
  fold into N only at epoch events. Zero runtime code: identity-safe by
  construction, and no second ≈ can arise from documentation. Example fixtures
  are for the boundary, where laws end.
- **L10 (Round-trip — the central theorem).** For models a, b: applying
  `gen(diff(a, b))` to a world at `revision(a)` lands it at `revision(b)` — gen
  is a section of apply-then-introspect up to ≈_syn. This is THE specification of
  diff and generate; preconditions, reconcile, and pure generation are
  scaffolding around this one equation. SOUNDNESS CAVEAT, stated:
  certification-by-reconcile additionally requires (i) introspection injective
  up to ≈_syn on the states exercised (it is NOT globally — state-machine types
  introspect as plain enums), and (ii) bridge completeness on the expressions
  exercised (the bridge is documented-incomplete). Therefore the randomized test
  (modelgen pairs -> diff -> apply -> verify) splits its oracles by soundness
  domain: the MANIFEST oracle (recorded to-revision manifest, compared
  object-by-object — not lossy) runs over the UNRESTRICTED generator, giving
  state-machine types randomized coverage; only the RE-INTROSPECTION oracle
  restricts to the injective, bridge-proven fragment. Corollaries:
  `diff(a,a) = empty` (pinned); squash-commutation; diff MINIMALITY as a
  non-normative quality property (mutation-tested: delete any op, the oracle must
  fail).

---

## Part II — The boundary doctrine

The system is a THREE-WAY partition, and defects are triaged accordingly:

1. **The kernel** — law-governed. Every law names its property tests (L9), so
   "a law was implemented wrong" is a CHECKABLE claim against a stated property,
   never a rhetorical escape hatch. Defects here are implementation errors; the
   fix is in the kernel and the property suite gains the case.
2. **The enumerated boundary** (below) — everything the system does not control:
   Postgres's runtime semantics and crash timing, the filesystem, git's merge
   behavior, six consumer languages, consumer code, the parser dependency.
   Defect classes here cannot be made unrepresentable — only checked, by fault
   injection, conformance matrices, and compile checks.
3. **Plain engineering outside the algebra** — presentation work, CLI
   ergonomics, doc wording, seed statistical quality. Ordinary bugs, no
   doctrinal claim; forcing the formalism onto them would be ceremony.

ONE NAMED EXCEPTION to plain-engineering triage: validate is modelgen's validity
oracle, so validate's correctness is LOAD-BEARING for kernel verification — a
validate bug is KERNEL-ADJACENT, and its fix triggers an audit of which kernel
properties were tested over a distorted generated-input distribution (a narrowed
or skewed validity notion silently shrinks what the property suite ever
exercised).

Boundary membership is BIDIRECTIONAL: the list may grow only with a post-mortem
containing a POSITIVE impossibility argument (why no pure property test could
catch the class — not merely "we didn't derive it"), and a boundary item that
becomes property-checkable is DEMOTED into the kernel. (A closed list would be
unfalsifiable in one direction; a grow-only list is unfalsifiable in the other.)

### The enumerated boundary

Everything below is irreducible — checkable, not eliminable. A defect in kernel
territory is an implementation error against a stated law property; a defect here
is checked by the named mechanism; a defect in plain-engineering territory is an
ordinary bug.

1. **Postgres crash windows** around non-transactional DDL (CIC, drop-CIC,
   pre-PG12 enum-add). Check: fault-injection matrix incl. `indisvalid`
   assertions.
2. **The upgrade choreography** (DB transaction + pre-commit file writes). Check:
   crash injection on both sides of COMMIT.
3. **The SQL predicate renderer** — a second computation of ≈_syn in PL/pgSQL.
   Check: the conformance matrix (Go executor vs SQL renderer vs differ) — which
   is SAMPLED agreement, not proof of ≈-agreement, so it is fed GENERATED random
   expressions in addition to curated states.
4. **The ≈_syn/≈_pg residue** — catalog-dependent cast materialization is
   unreachable by pure normalization; on live paths it is resolved by LIVE
   ROUND-TRIP NORMALIZATION (desired-side expressions through the target DB —
   throwaway temp object + `pg_get_*` deparse; exact by construction, absorbs
   missing foldings), with a minimal forward-simulation rule set only where
   round-trip cannot reach. Check: the round-trip fixture suite (temp-object
   hygiene included); the comprehensive fixture (CHECKs, partial indexes,
   policies) reused by `diff --live`, upgrade, reconcile, shadow test.
5. **Six consumer languages' semantics.** Check: DB-free compile checks of
   generated fixtures — all six mandatory.
6. **Git merge behavior** on chain files. Minimized by one-file-per-edge with
   content-derived names (textual AND allocation conflicts impossible); semantic
   forks remain. Check: two-head detection + `migrate rebase`.
7. **Concurrent binaries** on one database. Check: the shared session-level
   advisory lock; concurrent-apply-during-upgrade test.
8. **TOCTOU between check and apply** on a live database. Minimized:
   preconditions run inside each op's transaction.
9. **Filesystem atomicity** for store writes. Minimized: content-addressed
   idempotent writes; consistency checker.
10. **Consumer adaptation** to the breaking release (consumers surface-verified
    2026-07). Check: filed todos containing scripted `pgdesign codegen --check`
    invocations; the pass itself is the consumers' half.
11. **External milestone**: strictcli must ship the connection-env kind before
    the DB tiers finalize (the pure tiers have no such dependency).
12. **go-pgquery deparse stability** — N (and hence identity) is DEFINED by an
    externally-pinned parser's deparse output; a version bump can shift ≈_syn.
    Check: the CI PIN GUARD makes accidental bumps STRUCTURALLY IMPOSSIBLE — the
    pin moves only by editing the recorded sanctioned version, an unmistakably
    deliberate act; N's golden REGRESSION fixtures cover pgdesign's own
    normalizer changes. Policy: essentially NEVER bump — when eventually forced
    (new PG syntax support, toolchain rot), a deliberate breaking MAJOR release
    carries the event-time procedure.
13. **Git plumbing for import fetches** (ref resolution, auth, remote
    availability) — distinct from item 6's merge behavior. Check: import
    lock/update error-path tests; offline builds never need the remote (vendored
    surface).
14. **SHA-256 collision resistance** — L2's stated assumption; id-equality fast
    paths skip byte comparison. Check: none (cryptographic assumption, named so
    it is never silently strengthened into "proven").

---

## Part III — Decision provenance

Every design decision in pgdesign has a provenance tag, so a future maintainer
can tell what is negotiable from what is not:

- **`[deliberate]`** — the owner's own axioms. Fixed.
- **`[law]`** — a consequence of Part I. Reversing one requires rejecting a law,
  not just changing a preference.
- **`[%%]`** — genuinely free choices (names, layouts, per-language mechanics)
  that the laws do not determine. Weakly held, reversible, never to be cited as
  deliberate intent.

### On the `%%` convention

`[%%]` marks a decision adopted under the owner's `%%` convention: when a
recommendation was accepted with a bare "`%%`", it meant "go with the
recommendation because I trust it," not "I deliberately decided this." Such a
decision is freely reversible — if evidence later goes against it, walk it back
without ceremony; it was never a deliberate commitment. A `[%%]` tag on a rule
below is therefore an invitation to reconsider it on the merits, never a citation
of settled intent.

### Owner axioms `[deliberate]`

- No rename; the project stays pgdesign.
- One release for the whole roadmap, at the very end.
- No backward compatibility, ever, for pre-stable projects. (This axiom and L2
  reinforce each other: compat is keeping two identities for one content;
  extensionality has no such operation.)
- Hotfix path under the one-release axiom: a first-class maintenance-release mode
  lives in rlsbl (tag-collision guard included); a documented config-route
  procedure serves until it ships. Corollary: the final roadmap release bumps
  MINOR or MAJOR, never patch — a patch bump could mint a tag an interim hotfix
  already used.

### Consequences `[law]` (condensed)

- Append-only STORE CONTENT (objects, manifests) — L2 structurally; append-only
  CHAIN-EDGE FILES — checked policy via the consistency checker (they are
  location-addressed, so extensionality cannot cover them). Checksums exist ONLY
  on the apply surface (post-journal, rollback reads no files, so no rollback
  checksum surface exists) — L2.
- Content ids are EPOCH-RELATIVE; an enc/N change re-keys the world, but epoch
  changes are RARE, DELIBERATE BREAKING-MAJOR-RELEASE EVENTS whose recovery
  tooling is written AT EVENT TIME. Pre-built now: the codec-version field on
  every stored form and the CI pin guard. The revision-remap table is REBASE-ONLY
  — L2+L9.
- Squash = a consolidation edge (composition), never a rewrite; the edge's
  op-list is the CONCATENATION of the superseded path's ops — L3. Consolidation
  downs are derived by manifest diff ONLY for fully-mechanically-invertible
  ranges; ranges containing declared-inverse or RawSQL ops compose the originals'
  recorded downs — L4. Consolidations PRESERVE every DML/RawSQL op of the
  superseded path (no drop, no fold-across) — L5's codomain choice made explicit
  on the UP direction.
- Pure migration generation = `diff(head manifest, current model)`; its
  specification is L10's round-trip — L5+L10.
- Precondition drift = hard error, always; reconcile after apply; adoption of
  intentional drift only via explicit baseline — L5.
- Journal records op identity AND serialized down-op; rollback is DB-driven;
  pre-upgrade prefix and baselines are rollback-frozen (their journal rows cannot
  contain executable inverses — honest scoping, not compat) — L5+L4.
- Ops reference their objects and transitive type closure BY CONTENT ID into the
  store; no lossy structured mirrors; RawSQL/DML bodies are stored as
  content-addressed opaque blobs — L1+L2.
- Revision = hash of canonical bytes; every artifact stamped with a producing
  revision; the enforcement taxonomy (SIX writer classes: full regenerators /
  partial writers / source editors / scaffolding writers / stamped-unenforced
  writers (seed) / append-only stores) is derived from provenance totality — L6.
- Opaque Revision type; registry-absent marker INSIDE the hashed bytes;
  cross-class comparison errors — L7.
- Intent/confirm journaling for non-transactional ops, with resume protocols
  defined against Postgres's state model (`pg_index.indisvalid` for interrupted
  CREATE INDEX CONCURRENTLY; `IF EXISTS` on DROP INDEX CONCURRENTLY) — L8.
- One normalization primitive consumed by differ, predicates, upgrade reconcile,
  and shadow test; predicate IR = one structured definition with a Go executor
  (structured diagnostics; shares a catalog-query layer with introspect, gates
  version-conditional queries through the pgcap capability registry) and a SQL
  renderer, conformance-matrixed — L1.
- One canonical serializer everywhere (generate json = serve payload = import
  surface = op bodies = revision manifests); the encoder is a dedicated canonical
  encoder with reflection-based field-coverage guards over both the model structs
  and the registry snapshot — L1+L9.
- verify-then-stamp `migrate upgrade`: single DB transaction (lock; snapshot
  applied set; build journal/view/position; ASSERT view reproduces snapshot; DROP
  old table; COMMIT), content-addressed file writes idempotent and BEFORE the
  commit — L5+L8+L2.
- Compiler/live seam (build and generation pure; DB work in a distinct tier);
  live-only analyses (FD discovery, pg_stat) are DB-tier and non-retroactive —
  L5.
- Fail-closed imports: owned tables in Tables, imported in ImportedTables; every
  consumer iterating Tables is correct by omission; the union is wired at the
  enumerated resolution sites — L6-style totality applied to name resolution.
- Header/stamp grammar with one writer and one reader (`pkg/genkit`); one
  wording, adopted in a single pass — L6+L9.
- Property/fault verification style throughout: multi-iteration determinism
  tests, encoder coverage guards, conformance matrix, fault-injection matrix,
  DB-free compile checks of generated fixtures — L9.

### Free choices `[%%]` (condensed)

Names and layouts the laws do not determine: `pgdesign_migration_ops`,
`pgdesign_applied_migrations` (view carrying version, applied_at, description,
checksum), `pgdesign_chain_position`; chain-edge files named by edge-content hash
prefix plus slug in `migrations/chain/`; `migrations/objects/`,
`migrations/revisions/`, `migrations/archive/`, `imports/<alias>/`; visible
(non-dot) directory names for committed load-bearing data; `internal/objstore`,
`internal/project`, `internal/predicate`, `internal/catalog`; normalization homed
in `internal/sqlparse` (the go-pgquery leaf); migration file display names
carrying an auto-derived slug (override flag); the command names `import lock` /
`import update`, `migrate upgrade`, `migrate rebase`, `pgdesign revise`.

Per-language branding mechanics (boundary-empirical, not law-derived): Go opaque
struct with validating boundary and var members; Python `parse()` alias +
enum-typed surfaces + Row `__post_init__` coercion on both backends; TS
keep-the-union + `parse()`; Java/Kotlin value-parse (net-new `fromValue`) + JPA
AttributeConverter; Zig wrapper struct; sqlalchemy upgraded to
`sa.Enum(PyEnumClass)`; drizzle unchanged (already pgEnum-typed); constants mode
unchanged; constraints validators re-target the branded representation.

Policy choices that are deliberate engineering, NOT law consequences (the laws
admit alternatives; these are chosen on the merits): ALWAYS-large-table-safe
generation (uniformity — a declared size hint would be equally pure); FULL-PROJECT
stamp scope (resolves the filtered-output paradox); pure analyses BLOCK in
revise's pure tier (the owner's hard-constraints philosophy — analysis that can
block must block). Other `[%%]` choices: the squash op-list optimizer is out of
scope (concatenation-only); go-pgquery bumps are deliberate epoch events,
foreclosed by the CI pin guard; mixed-epoch chains are an unconditional
consistency-checker hard error; modelgen's validity oracle is validate itself;
seed import tiers (real-key pools / count-wrapped offset subqueries / hard errors
scoped to provably-broken cases); renames gated by `[renames]` declarations +
diff-time detection, ambiguous detections hard-error listing all candidates
(never auto-pair); serve binds 127.0.0.1 by default behind an explicit override
flag whose help states there is NO auth (auth is a decided non-goal, not an
omission); every migrate subcommand run against a pre-upgrade database
hard-errors naming `migrate upgrade`.

---

## Part IV — Ruled-out designs — do not resurrect

This is institutional memory: designs that were considered and rejected for a
law or axiom violation, or as a strictly dominated alternative. Do not revive
them without first refuting the refutation.

Compat-named DB objects or dual recognition of old names (owner axiom).
Staged/multi-pass header transitions (the one-release axiom makes them double
work). Checksums on the rollback path (post-journal, no such surface exists —
rollback reads no files). Replacing StrEnum with plain Enum, or
construction-closing machinery in Python (native Enum validation already rejects
invalid values). A nominal TS brand (regresses the union's compile-closure and
exhaustiveness narrowing). `@Enumerated(STRING)` in JPA (persists constant NAMES,
not DB values). A registry builtin-inclusion special case in identity (redundant
— builtin-derived domains materialize into the model collections L1 covers). A
single append-only manifest file, whole-model snapshots, dot-directories for
load-bearing data, or counter-allocated edge filenames (git-merge conflicts at
EOF; massive duplication; invisibility of committed artifacts; cross-branch
counter races — the per-edge content-derived chain, object store, and visible
names dominate). Manifest-diff downs for ranges containing data-bearing ops (L4
violation — a structural down would recreate a dropped column empty).
Consolidations that fold across DML/RawSQL ops (schema-commutation cannot see the
data divergence they cause for fresh databases). "Net manifest delta" as the
invertibility criterion (a trap: DROP populated column then ADD column has an
empty net delta and destroys data — per-op typing is correct). Rejecting (rather
than validating) Go unmarshal/scan boundaries (breaks every DB-scanned struct).
Row-count-conditional generation (L5 violation — generation must not read the
world). A closed boundary list AND a grow-only boundary list (each unfalsifiable
in one direction — the bidirectional rule with demotion is the sound form). An
iff form of L4's composite-inverse rule (false converse: composites can be
semantically invertible when components are not). "Squash is composition by
definition" for op-list-ALTERING consolidations (empty without a morphism
congruence — the adopted concatenation form earns it structurally because the
op-lists coincide; any future optimizer must re-earn it via the CHECKED
squash-commutation property). A single undifferentiated ≈ (unachievable:
`pg_get_*` cast materialization is catalog-dependent and unreachable by pure
normalization — hence ≈_syn with the foldings inside N, and live round-trip
normalization for the residue on live paths). One-sided expression rewriting
(false drift for users who write PG's own forms directly — foldings must apply to
both sides, inside N). Staging catalog-independent foldings in the live-side
residue mechanism instead of N (one-sided rewriting reborn: the live differ would
compute ≈_syn-plus-extras while enc, the executor, and the renderer compute plain
≈_syn — desynchronizing the conformance matrix; catalog-independent equivalences
go into N at epoch events or into the xfail backlog, never live-side;
investigated and refuted 2026-07). Interactive rename prompts, Prisma/Django
style (non-deterministic, CI-hostile, an escape hatch — the declarative
`[renames]` gate dominates).
