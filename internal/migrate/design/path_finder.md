# The path-finder (5.0)

A DETERMINISTIC TOTAL rule replacing today's flat semver sort. The POS-RELATIVE
consumers — `apply`, `apply --dry-run`, and `migrate status` — resolve a
particular database's pending work through it: it searches the edge graph (chain
+ archive) for the path a database at its `pgdesign_chain_position` must apply to
reach the head.

`migrate plan` is NOT a pos-relative consumer: it is pure (no DB, no
`chain_position`) and enumerates the chain from GENESIS — or from an explicitly
supplied `--from` revision string — to the head, listing edges in path-finder
order. It uses the same forward-path ordering (steps 3–4 below) but with a
DECLARED start, never a database's `pos`. Per-database pending remains
`migrate status`'s job. See `command_surface.md`.

## Inputs

- `pos` — `pgdesign_chain_position.current_revision` (may be rebased-away).
- `remap` — the on-disk rebase revision-remap table.
- `liveEdges` — the LIVE (non-archived) edges only (`migrations/chain/`). This is
  the HEAD-FINDING domain (see step 2).
- `allEdges` — ALL edges, ARCHIVE-INCLUSIVE (`migrations/chain/` +
  `migrations/archive/`). This is the TRAVERSAL domain (steps 3+): a mid-range or
  rebased-away database walks archived originals to reach the live head.

### Two domains, one rule (why head-finding excludes the archive)

Head-finding and traversal use DIFFERENT edge sets on purpose:

- **Head-finding = LIVE edges only.** An archived edge's target is, by
  definition, superseded (squash) or rebased-away (rebase) — it is NOT a live
  head. If `FindHeads` ran over the archive-inclusive set, every squashed range's
  old endpoint and every rebased-away tail would surface as a SPURIOUS extra head
  and trip the fork error. Restricting head-finding to `liveEdges` makes the live
  head set exactly the set of live tips.
- **Traversal = archive-inclusive.** Once the single live head is fixed, the
  path from `start` to it may pass through archived originals (a mid-consolidation
  database) — so `allForwardPaths` enumerates over `allEdges`.

### Remap canonicalization (both phases)

BOTH `FindHeads` and `reachable` compare revisions AFTER canonicalizing each
revision through `remap` to its live re-parented form (follow to a fixpoint).
Revisions are compared by their canonical `String()` form. This means a
rebased-away revision and its live replacement are treated as the SAME node in
the graph, so an edge left dangling at a rebased-away target does not read as an
independent head, and reachability from a rebased-away `pos` lands on the live
frontier.

## Rule (informal)

1. **Resolve the start.** If `pos` is not the parent/target of any live edge but
   appears in `remap`, translate it: `start := remap[pos]` (follow the remap to a
   fixpoint). Otherwise `start := pos`.
2. **Find the head.** `heads := FindHeads(liveEdges)` (kernel) — LIVE edges only,
   with each revision canonicalized through `remap` before comparison. If exactly
   one head is reachable from `start`, that is the target. Zero reachable →
   NO-PATH error. More than one reachable → FORK error (unresolved; points at
   `migrate rebase`).
3. **Shortest edge-count path.** Among all `start → head` paths over `allEdges`
   (ARCHIVE-INCLUSIVE; forward edges only: an edge is usable iff its `parent` is
   the current frontier revision, comparing remap-canonicalized revisions),
   choose the one with the FEWEST edges. Consolidation edges make ranges shorter,
   so they win here without a tie-break in the common case.
4. **Tie-break, total order.** If two paths have equal edge-count:
   a. prefer the path using MORE consolidation edges (consolidation preference);
   b. if still tied, choose the path whose sorted sequence of edge-ids is
      lexicographically least. (b) makes the rule TOTAL — see TENSION 1.
5. **Position at head.** If `start` is already a head, the path is empty (the DB
   is up to date); not an error.

## The consolidation-range disjointness invariant (FORBIDDEN predicate)

Consolidation ranges are FORBIDDEN AT CREATION unless they are pairwise DISJOINT.
Precisely: identify each consolidation edge with the SET of superseded edge-ids it
covers (the archived originals its op-list concatenates). Two consolidation edges
are FORBIDDEN iff those sets INTERSECT at all:

> Two consolidation edges C₁, C₂ are forbidden ⇔
> `supersededEdgeIDs(C₁) ∩ supersededEdgeIDs(C₂) ≠ ∅`.

Equivalently: consolidation ranges must be pairwise disjoint — no shared
superseded edge whatsoever. This is STRICTER than merely banning partial overlap:
FULL CONTAINMENT is also forbidden (a nested consolidation shares every edge of
the inner range, so the sets intersect). The clean form is chosen deliberately
over the weaker "partial overlap only" rule:

- **Unambiguous consolidation preference.** With disjoint ranges, no two
  consolidation edges ever both cover the same original, so the path-finder's
  "prefer more consolidation edges" tie-break (4a) can never face two
  consolidations competing to cover one segment — the preference is total and
  unambiguous, and the tie-break never has to reason about a consolidation that
  partially (or fully) covers another.
- **Cheapest creation-time check.** Disjointness is decided by a single set
  operation: intersect the new consolidation's superseded-edge-id set against each
  existing consolidation's set; a non-empty intersection is a HARD ERROR at
  creation. No range-endpoint reasoning, no containment special-casing.

5.3 ENFORCES this at squash time: when `squash` emits a consolidation edge it
computes the superseded-edge-id set and rejects the squash if it intersects any
existing consolidation's set. The path-finder therefore never sees an overlapping
or nested consolidation and its tie-break stays total.

## Pseudocode

```
func FindPath(pos Revision, remap RemapTable, liveEdges, allEdges []Edge) ([]Edge, error):
    start := canon(resolveStart(pos, remap, allEdges), remap)  // follow remap to fixpoint if rebased away
    heads := FindHeads(liveEdges)                   // LIVE edges only: targets that are no LIVE edge's parent

    // reachable/head comparisons canonicalize every revision through remap first.
    reachableHeads := [h for h in heads if reachable(start, canon(h, remap), allEdges)]
    if len(reachableHeads) == 0:
        if start == any canon(head, remap): return [], nil  // already at a head: up to date
        return nil, NoPathError(start)              // corrupt / off-chain position
    if len(reachableHeads) > 1:
        return nil, ForkError(reachableHeads)       // -> migrate rebase

    target := reachableHeads[0]
    paths := allForwardPaths(start, target, allEdges)  // ARCHIVE-INCLUSIVE; each edge usable iff parent == frontier
    if len(paths) == 0:
        return nil, NoPathError(start)

    best := argmin(paths, key = len)                          // (3) fewest edges
    ties := [p for p in paths if len(p) == len(best)]
    if len(ties) > 1:
        best = argmax(ties, key = countConsolidation)         // (4a) prefer consolidation
        stillTied := [p for p in ties if countConsolidation(p) == countConsolidation(best)]
        if len(stillTied) > 1:
            best = min(stillTied, key = edgeIDSequence)        // (4b) lexicographic on edge-ids
    return best, nil
```

`allForwardPaths` only follows edges whose `parent` equals the current frontier
revision (an edge whose parent is upstream of `start` — e.g. a consolidation
covering a range the DB is already inside — is simply not applicable and is not
enumerated). Cycles are impossible in a revision DAG built from distinct content
ids except for endomorphisms (R→R DML edges); those are handled by treating an
endomorphism as a length-1 step that does not revisit (an edge is used at most
once per path).

## Edge cases (enumerated)

| Case | Behavior |
|---|---|
| **No path** (`pos` not on chain, no remap entry) | NO-PATH hard error naming the unreachable revision (corrupt/off-chain position). |
| **Multiple heads reachable** (fork) | FORK hard error listing the heads, pointing at `migrate rebase` — never an arbitrary pick. |
| **Position at head** | Empty path; database is up to date (success, no-op). |
| **Rebased-away position** | `remap` translates `pos` to the live re-parented revision, then path-finds from there; served forward, never orphaned (L2/5.10). |
| **Mid-consolidation-range position** | The consolidation edge's parent is upstream of `pos`, so it is not enumerated; the DB traverses the remaining ARCHIVED originals (archive-inclusive). |
| **Start == consolidation-range start** | Both the consolidation edge (1) and the originals (N) reach the range end; shortest-count picks the consolidation strictly (1 < N); tie-break not needed. |

### Worked case: post-rebase, no spurious head

Chain before rebase (all live): `R0 →e1→ R1 →e2→ R2` (fork tail `e2` diverged).
`migrate rebase` re-parents the tail onto the live head, producing a live edge
`e2' : R1 → R3` and RETIRING the old `e2 : R1 → R2` to `archive/`. It writes
`remap[R2] = R3`.

- **Head-finding (LIVE only).** `liveEdges = {e1, e2'}`. `FindHeads(liveEdges)`
  sees targets `{R1, R3}` and parents `{R0, R1}`; only `R3` is no live edge's
  parent, so `heads = {R3}` — ONE head. The archived `e2` (target `R2`) is NOT in
  `liveEdges`, so `R2` never surfaces as a second head. Had head-finding been
  archive-inclusive, `R2` (target of the archived `e2`, parent of nothing live)
  would have appeared as a spurious extra head and tripped the FORK error on a
  correctly-rebased chain.
- **A database stamped at the rebased-away `R2`.** `resolveStart` canonicalizes
  `R2` through `remap` to `R3`; `start = R3`. `R3` is a head, so the path is empty
  — the DB is already up to date and is SERVED, not orphaned.
- **A database stamped mid-range at `R1`.** `start = R1`; the single live head is
  `R3`; `allForwardPaths(R1, R3, allEdges)` uses the live `e2'` (`R1 → R3`) — one
  edge. (Nothing forces it through the archive here; the archive is consulted only
  when a live path does not exist, e.g. a mid-consolidation position.)

## TENSIONS surfaced (design gate)

1. **"Consolidation-edge preference" is not a total order.** Two equal-length
   paths that both use the SAME number of consolidation edges (e.g. two
   consolidations of equal length covering different non-overlapping ranges, both
   admissible on equal-length alternatives) are NOT disambiguated by rule (4a).
   The roadmap names only the consolidation preference as the tie-break, which
   leaves a residual ambiguity. **Resolution:** add a final deterministic
   tie-break (4b): lexicographically least sorted edge-id sequence. This makes
   the rule provably total. Flagged: the roadmap's stated tie-break is
   INCOMPLETE; (4b) is the design-gate addition that closes it. Since overlapping
   consolidation ranges are forbidden at creation, the residual-tie case is rare,
   but a path-finder that is "deterministic and total" must not leave ANY tie to
   chance.

2. **"To a head" is under-specified when the chain has forked.** A database with
   two reachable heads has no unique target. The roadmap says "to a head" but
   apply cannot silently pick one (that would apply divergent schema depending on
   enumeration order). **Resolution:** more-than-one reachable head is a HARD
   ERROR directing to `migrate rebase` (5.10), consistent with the boundary
   doctrine's two-head detection (boundary item 6). A single reachable head is the
   only non-error target. Flagged as a decision, not a silent behavior.
