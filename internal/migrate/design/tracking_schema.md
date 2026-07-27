# Tracking schema design (5.0)

Reviewed DDL: `tracking_schema.sql`. Three managed structures replace today's
single `pgdesign_migrations` table. All are created inside the single
`migrate upgrade` transaction (5.2); the old table is dropped after the view is
asserted to reproduce the old applied set.

## The three structures

| Structure | Kind | Role |
|---|---|---|
| `pgdesign_migration_ops` | table | per-op journal: op identity, serialized down-op, intent/confirm status |
| `pgdesign_applied_migrations` | view | one SQL definition of "applied + status" for four readers |
| `pgdesign_chain_position` | table (singleton) | this database's chain position and boundary |

## pgdesign_migration_ops

Op identity is `(edge_id, seq)`: `edge_id` is the content-derived edge identity
(`chain.Edge.ID()`), `seq` is the op's 0-based position in the edge's op-list
(the deterministic apply order). Columns:

- `edge_id`, `seq` — op identity (migration ref + sequence).
- `phase` — `''` (single-phase) or `expand`/`migrate`/`contract`.
- `op_kind` — op family (`create_table`, `add_column`, `dml`, `raw`, …).
- `target` — `enc.Key.String()` of the op's target object; a pseudo-key for
  DML/RawSQL ops (`dml:<edge-seq>` / `raw:<edge-seq>`, grammar pinned in
  `edge_format.md` TENSION 2). This column is one of the two homes of a
  pseudo-target key (the other is the edge's op projection); pseudo-keys never
  appear in a manifest or the consistency checker.
- `invertibility` — the UP op's L4 class.
- `down_op` — the serialized down-op reference `{kind,target,invertibility,payload_id}`
  resolvable via objstore; `NULL` iff the op is non-invertible (CHECK-enforced).
- `status`, `intended_at`, `confirmed_at` — the intent/confirm protocol (below).

### Intent/confirm state machine (L8)

Two states, one terminal:

| From | Event | To |
|---|---|---|
| (no row) | transactional op executes | `confirmed` (row written INSIDE the op's txn, atomic with effect) |
| (no row) | non-transactional op about to start | `intended` (row committed BEFORE the op runs) |
| `intended` | op verified complete in Postgres's own state model | `confirmed` |

- **Transactional ops** journal atomically: the `confirmed` row is written in the
  same transaction as the DDL, so there is no crash window (L8: "atomic with its
  effect").
- **Non-transactional ops** (CREATE INDEX CONCURRENTLY, DROP INDEX CONCURRENTLY,
  pre-PG12 `ALTER TYPE ... ADD VALUE`) write an `intended` row (committed), run
  the op outside a transaction, then write `confirmed`. **Resume** of an
  `intended`-but-not-`confirmed` row uses Postgres's state model, not ours:
  - create-index intent → check `pg_index.indisvalid`; an interrupted CIC leaves
    an INVALID index that `IF NOT EXISTS` would skip forever, so resume DROPs and
    rebuilds, then confirms.
  - drop-index intent → re-issue `DROP INDEX CONCURRENTLY IF EXISTS`, then confirm.
  - enum-add intent → already idempotent; re-issue and confirm.
- **Edge completion** is defined (5.5) as the transaction that confirms the
  edge's FINAL op; `pgdesign_chain_position.current_revision` advances to the
  edge's target in that same transaction.

Integrity CHECKs pin the machine: `status = 'confirmed'` iff `confirmed_at IS NOT
NULL`; `invertibility = 'non-invertible'` iff `down_op IS NULL`.

## pgdesign_applied_migrations (the view)

```sql
SELECT version_label AS version, max(confirmed_at) AS applied_at, description, checksum
FROM pgdesign_migration_ops
GROUP BY edge_id, version_label, description, checksum
HAVING bool_and(status = 'confirmed');
```

- An edge is **applied** iff every one of its op rows is `confirmed`
  (`bool_and`). In-progress edges (any `intended` op) are excluded.
- `applied_at` = `max(confirmed_at)` = the edge's final-op confirm time = edge
  completion.
- Four readers consume this one definition: `serve` (`handleMigrations`),
  `migrate status`, `AppliedVersions`, and the upgrade's ASSERT step.

### applied_at derivation across the two eras

- **Post-upgrade edges**: `applied_at` is the journal edge-completion time,
  derived by `max(confirmed_at)`.
- **Prefix rows** (migrations applied by the OLD system, pre-upgrade): there is
  no per-op journal to derive a time from. `migrate upgrade` therefore inserts,
  per old row, ONE synthetic `confirmed` op with `confirmed_at := old.applied_at`
  and `checksum := old.checksum`. The view then surfaces both verbatim with NO
  special-casing — `max(confirmed_at)` over a single synthetic op is exactly
  `old.applied_at`. Verbatim preservation is a FOLD-TIME action, not a view
  branch. This is precisely what lets the upgrade's
  ASSERT-view-reproduces-snapshot step pass on its own columns (5.0/5.2).
- **One view row per prefix migration (edge_id distinctness).** The view groups
  by `edge_id` (§ the view), so the fold MUST give each old tracking row a
  DISTINCT `edge_id` or two old migrations would collapse into one grouped row.
  The fold does exactly this: it mints a distinct synthetic revision per old row
  (the per-database synthetic-prefix revision, 5.2), and the synthetic op's
  `edge_id` is derived from that per-row synthetic revision — so the `GROUP BY
  edge_id` yields exactly ONE row per prefix migration, never a merge. The single
  synthetic op is that edge's only op, so `bool_and(status = 'confirmed')` is
  trivially true and `max(confirmed_at)` is `old.applied_at` verbatim.

## pgdesign_chain_position

A singleton row (enforced by `id boolean PRIMARY KEY DEFAULT true CHECK (id)`):

- `current_revision` — the revision this DB is at; advances with each edge's
  final-op confirm. May hold a rebased-away revision (served via the on-disk
  remap, not orphaned — L2/5.10).
- `in_progress_edge` — `edge_id` of an edge mid-apply; `NULL` when idle.
- `boundary_revision` + `boundary_kind` — the upgrade/baseline floor. Rollback
  refuses to cross below it (5.6: pre-upgrade prefix + baselines are
  rollback-frozen). `boundary_kind` is `upgrade` or `baseline`.
- `codec_epoch` — the codec epoch of this DB's chain; the consistency checker
  flags a mismatch against chain-edge epochs (mixed-epoch = corruption).

The rebase revision-remap table is NOT a DB structure — it is a REBASE-ONLY
on-disk chain artifact (see `store_layout.md`). apply consults it to translate a
rebased-away `current_revision` to a live revision before path-finding.

## TENSIONS surfaced against the kernel (design gate)

1. **The three-structure naming under-specifies where `version`, `description`,
   `checksum` live.** The roadmap enumerates `pgdesign_migration_ops` columns as
   op-identity only (migration ref, phase, sequence, op kind, target, down-op,
   status), yet the view requires `version`/`description`/`checksum` per applied
   edge, and no fourth per-edge table is named. **Resolution:** denormalize the
   three edge-level attributes onto every op row (they are functionally
   dependent on `edge_id`). This honors the three-structure [%%] naming; the cost
   is two small text columns repeated per op. The rejected alternative — a fourth
   `pgdesign_applied_edges` table — is cleaner relationally but adds a named
   structure the roadmap did not sanction and buys little for tables that are
   small by nature. Flagged for owner awareness; reversible.

2. **`version`'s post-content-identity meaning is a free choice.** Under
   content-derived identity there is no semver "version". The view's `version`
   column is `version_label`: the preserved semver for prefix rows, and the
   `edge_id` for post-upgrade edges (uniquely identifies the applied migration
   even for endomorphisms/parallel edges, which a to-revision would not). This is
   a [%%] decision — an alternative is the to-revision string; `edge_id` is
   chosen because it survives endomorphic (R→R) DML edges. Flagged; reversible.

3. **`checksum` is a distinct hash from `edge_id`.** The edge's IDENTITY
   (`edge_id`) is the edge-CONTENT hash (`chain.Edge.ID()` over the op projection);
   the apply-surface `checksum` (5.4) is over the edge FILE bytes, which are
   location-addressed and not identity. They differ, so `checksum` is a genuine
   stored column, not derivable from `edge_id`. Consistent, noted so 5.4 does not
   conflate them.
