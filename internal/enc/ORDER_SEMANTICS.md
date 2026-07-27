# The order-semantics table (internal/enc)

This table is part of the canonical-encoder format spec (roadmap kernel 1.1,
law L1). It is **exhaustive over the resolved Model** and classifies, for every
collection, two independent order questions:

- **Collection order** — the order of the collection *as a whole* among its
  siblings (e.g. the order of tables in a schema, of columns in a table).
- **Intra-object order** — for the elements *inside* one object of that kind
  (e.g. the order of the columns a composite index spans).

Each is one of:

- **SEMANTIC** — the order changes what PostgreSQL does, so it is preserved
  verbatim by the encoder and is part of `≈_syn`. Reordering is a real change
  and flips identity.
- **CANONICAL-ONLY** — the order is a set/bag that PostgreSQL does not observe,
  so the encoder normalizes it (via `Canonicalize` for object collections, or by
  sorting set-valued leaf slices / relying on `encoding/json`'s sorted map keys)
  and two orderings converge to the same bytes.
- **N/A** — the collection is a map or scalar with no positional order.

Together these classifications **define `≈_syn` on the structural sublanguage**:
two models are structurally equivalent iff, after normalizing every
CANONICAL-ONLY order and holding every SEMANTIC order fixed, they encode to the
same bytes. (Expression *leaves* — CHECK/policy/index predicates, defaults, view
and function bodies — remain opaque at this phase; they enter normal form when
the normalizer N lands, roadmap 1.2.)

## Top-level (schema)

| Collection | Collection order | Intra-object order | Notes |
|---|---|---|---|
| `Schema.Extensions` | CANONICAL-ONLY (sorted) | — | extension set; sorted by `Canonicalize` |
| `Schema.Enums` | CANONICAL-ONLY (name-sorted) | — | see Enum below for values |
| `Schema.Domains` | CANONICAL-ONLY (name-sorted) | — | |
| `Schema.CompositeTypes` | CANONICAL-ONLY (name-sorted) | — | see composite fields below |
| `Schema.Tables` | CANONICAL-ONLY (topo + alpha tie-break) | — | dependency-ordered, deterministic |
| `Schema.Views` | CANONICAL-ONLY (topo + alpha) | — | |
| `Schema.MaterializedViews` | CANONICAL-ONLY (topo + alpha) | — | |
| `Schema.Sequences` | CANONICAL-ONLY (name-sorted) | — | |
| `Schema.Functions` | CANONICAL-ONLY (topo + alpha) | — | |
| `Schema.Groups` | N/A (map, keys sorted) | CANONICAL-ONLY (values sorted) | group membership is a set |

## Table

| Collection | Collection order | Intra-object order | Notes |
|---|---|---|---|
| `Table.Columns` | — | **SEMANTIC** | on-disk/attribute order is observable |
| `Table.PK` | — | **SEMANTIC** | composite-key column order defines the backing index |
| `Table.FKs` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Indexes` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Uniques` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Checks` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Exclusions` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Policies` | CANONICAL-ONLY (name-sorted) | — | |
| `Table.Triggers` | CANONICAL-ONLY (name-sorted) | — | PG fires same-event triggers in **name** order, so name-canonicalization coincides with firing order |
| `Table.Dependencies` | CANONICAL-ONLY | CANONICAL-ONLY (determinant/dependent sorted) | declared FDs; each FD is a set→set |

## Intra-object element orders

| Collection | Intra-object order | Notes |
|---|---|---|
| `FK.Columns` / `FK.RefColumns` | **SEMANTIC** | positional correspondence: the i-th local column references the i-th ref column |
| `Index.Columns` | **SEMANTIC** | key-column order defines the index |
| `Index.Desc` | **SEMANTIC** | parallel to `Index.Columns` |
| `Index.Include` | CANONICAL-ONLY (sorted) | INCLUDE payload is a set |
| `Index.Opclasses` / `Collations` / `With` | N/A (map, keys sorted) | keyed by column / param name |
| `Enum.Values` | **SEMANTIC** | PostgreSQL enum label order is significant (ordering comparisons) |
| `CompositeType.Fields` | **SEMANTIC** | composite attribute order |
| `Function.Args` | **SEMANTIC** | positional argument order; also the overload signature |
| `PartitionSpec.Columns` | **SEMANTIC** | partition-key column order |
| `PartitionSpec.Children` | CANONICAL-ONLY | child partitions (bound-distinguished) |
| `ExclusionConstraint.Elements` | **SEMANTIC** | ordering of the backing index's columns — treated like index key columns |
| `Trigger.Events` | CANONICAL-ONLY (sorted) | `{INSERT, UPDATE, DELETE}` is a set; DDL order is not observable |
| `UniqueConstraint.Columns` | **SEMANTIC** | defines the backing unique index |
| `CheckConstraint` / policy / view / function bodies | opaque | expression leaves; normalized by N (1.2), not here |

## State-machine transitions (registry snapshot)

State-machine transition identity flows through the **registry snapshot**
(`EncodeRegistrySnapshot`), not the schema-side `Schema.StateMachineTransitions`
(excluded per 1.5). See `snapshot.go`.

| Collection | Collection order | Intra-object order | Notes |
|---|---|---|---|
| snapshot `state_machines` | CANONICAL-ONLY (name-sorted) | — | `Registry.StateMachineTypes()` sorts by name |
| `TypeDef.States` | — | **SEMANTIC** | becomes the enum label order |
| `TypeDef.Transitions` | CANONICAL-ONLY | — | declaration order not observable |
| `SMTransitionDef.From` | — | CANONICAL-ONLY (sorted) | source-state set |
| `SMTransitionDef.Requires` | N/A (map, keys sorted) | — | param name → PG type |

## Excluded from identity (allowlist)

See `policy.go` for the machine-checked allowlist and per-field reasons. In
summary: derived caches (`TablesByName`, `FKGraph`, `CycleGroups`), the
schema-side `StateMachineTransitions` duplicate, `SourceFile` provenance,
`Index.IsAutoFK` (enrich-derived), `Table.PartmanManaged`/`PartmanParent`
(introspect-path facts), and all `Source` provenance fields
(`TypeDef.Source`, `fd.FuncDep.Source`) are excluded — the last so that
relabeling provenance never flips identity.
