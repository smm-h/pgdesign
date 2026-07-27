# The path-finder (5.0)

A DETERMINISTIC TOTAL rule replacing today's flat semver sort. apply, `migrate
plan`, `apply --dry-run`, and `migrate status` all resolve pending work through
it. It searches the edge graph (chain + archive) for the path a database at its
`pgdesign_chain_position` must apply to reach the head.

## Inputs

- `pos` — `pgdesign_chain_position.current_revision` (may be rebased-away).
- `remap` — the on-disk rebase revision-remap table.
- `edges` — ALL edges, ARCHIVE-INCLUSIVE (`migrations/chain/` + `migrations/archive/`).

## Rule (informal)

1. **Resolve the start.** If `pos` is not the parent/target of any live edge but
   appears in `remap`, translate it: `start := remap[pos]` (follow the remap to a
   fixpoint). Otherwise `start := pos`.
2. **Find the head.** `heads := FindHeads(edges)` (kernel). If exactly one head is
   reachable from `start`, that is the target. Zero reachable → NO-PATH error.
   More than one reachable → FORK error (unresolved; points at `migrate rebase`).
3. **Shortest edge-count path.** Among all `start → head` paths (forward edges
   only: an edge is usable iff its `parent` is the current frontier revision),
   choose the one with the FEWEST edges. Consolidation edges make ranges shorter,
   so they win here without a tie-break in the common case.
4. **Tie-break, total order.** If two paths have equal edge-count:
   a. prefer the path using MORE consolidation edges (consolidation preference);
   b. if still tied, choose the path whose sorted sequence of edge-ids is
      lexicographically least. (b) makes the rule TOTAL — see TENSION 1.
5. **Position at head.** If `start` is already a head, the path is empty (the DB
   is up to date); not an error.

Overlapping consolidation ranges are FORBIDDEN AT CREATION (a cheap structural
invariant checked when a consolidation edge is written), which eliminates the
ambiguous nested/overlapping cases outright — the tie-break never has to reason
about a consolidation that partially covers another.

## Pseudocode

```
func FindPath(pos Revision, remap RemapTable, edges []Edge) ([]Edge, error):
    start := resolveStart(pos, remap, edges)      // follow remap to fixpoint if rebased away
    heads := FindHeads(edges)                       // kernel: targets that are no edge's parent

    reachableHeads := [h for h in heads if reachable(start, h, edges)]
    if len(reachableHeads) == 0:
        if start == any head: return [], nil        // already at a head: up to date
        return nil, NoPathError(start)              // corrupt / off-chain position
    if len(reachableHeads) > 1:
        return nil, ForkError(reachableHeads)       // -> migrate rebase

    target := reachableHeads[0]
    paths := allForwardPaths(start, target, edges)  // each edge usable iff parent == frontier
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
