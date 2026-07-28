---
title: "The Migration Chain"
description: "How pgdesign's content-addressed migration chain works: revisions, edges, the object store, the journal, the path-finder, and the integrity guarantees they provide."
---

# The Migration Chain

This page explains the *concepts* behind pgdesign's migration system — what a revision is, what an edge is, and why the design gives you integrity guarantees that file-numbered migration tools cannot. For the command-by-command how-to (generate, apply, rollback, squash, rebase, upgrade, baseline), see the [Migration Guide](migration-guide.html).

## The core idea: identity by content, not by filename

Most migration tools identify a migration by a filename or a sequence number: `0007_add_status.sql`, `V12__widen_email`. That number is assigned by whoever created the file. Two developers on two branches both grab `0007`; a migration edited after it was applied still claims the same number; and nothing ties the number to what the migration actually contains.

pgdesign takes a different approach, borrowed from git. Everything is addressed by the hash of its content:

- The fully-resolved schema (every table, type, view, function, comment, and version target) is encoded into canonical bytes and hashed into a **revision** — a single fingerprint of the entire schema at a point in time.
- A **migration** is an **edge**: a transition from one revision to another. Its identity is the hash of its contents (its from-revision, its to-revision, and its operations). Its on-disk filename is derived from that hash plus a human-readable slug.
- Every object an edge touches (a table definition, an enum, a column's type) is stored once in a content-addressed **object store** and referenced by its id.

Because identity is derived from content, several problems simply cannot occur:

- **No spurious churn.** Regenerating a migration from an unchanged schema produces a byte-identical edge with the same name. Git sees nothing to commit.
- **No branch collisions.** Two branches that make *different* changes produce *different* edges. There is no counter to race, so they never collide — they form a fork, which you resolve explicitly (see [forks](#forks-two-heads) below).
- **No lying migrations.** An edge references its objects by content id. It cannot claim to add a column while carrying the wrong definition, because the id *is* the definition.

## Revisions

A revision is the hash of a canonical **manifest** — a sorted map from kind-qualified keys (`table:public.users`, `function:public.f(int)`, ...) to the content id of each object. Two schemas produce the same revision if and only if they are equivalent under pgdesign's normalization:

- Declaration order in your TOML does not matter for order-insensitive collections (checks, indexes, policies). Reordering them does not change the revision.
- Order *does* matter where PostgreSQL cares — column order, enum value order, composite-type field order.
- Semantically-equal expressions normalize to the same form. Writing `x IN (1, 2)` and `x = ANY(ARRAY[1, 2])` does not produce two different revisions.

A revision is an opaque value tagged with its **model class**. A schema built from your TOML (with full semantic type information) and a schema read back from a live database (without it) are different classes; comparing their revisions is a type error, not a silent mismatch.

## Edges and the store

Everything a chain-format project needs lives under `migrations/`:

| Directory | Contents |
|-----------|----------|
| `migrations/chain/` | one file per **live** edge — the current history |
| `migrations/archive/` | retired originals (superseded by a squash, or rebased away) — kept intact, never rewritten |
| `migrations/objects/` | the content-addressed object store: every object and operation payload |
| `migrations/revisions/` | one manifest per revision |
| `migrations/remap.json` | the rebase revision-remap table (present only after a `migrate rebase`) |

An edge carries a list of **operations** — `create_table`, `add_column`, `alter_column_type`, `add_fk`, `backfill`, and so on — each of which references its target objects by content id and carries a recorded **inverse** for rollback. Each op is typed by how it inverts:

- **Mechanically invertible** — the inverse is derived (adding a column inverts to dropping it).
- **Declared inverse** — the inverse is recorded explicitly, and may be *vacuous* (a data backfill "inverts" to a no-op: the schema is restored, the data is not).
- **Non-invertible** — there is no inverse; rollback across it is refused.

A composite range is invertible only when *every* operation in it is. pgdesign never infers invertibility from a net effect: dropping a populated column and then adding a fresh one has an empty net delta but destroys data, so it is not treated as reversible.

## Applied history lives in the database

The on-disk chain is the *repository* of possible history. What a *particular database* has actually applied is recorded in the database itself, in three managed objects:

- `pgdesign_chain_position` — which revision this database is at, and whether an edge is mid-application.
- `pgdesign_migration_ops` — the **journal**: every applied operation, with its serialized inverse.
- `pgdesign_applied_migrations` — a view exposing version, applied-at time, description, and checksum.

This separation is the source of pgdesign's safety properties:

- **Generation is pure.** `migrate generate` diffs the on-disk head against your current schema and never touches a database. The same schema edit always produces the same edge, regardless of any database's state. (Because it cannot see row counts, it always emits large-table-safe forms — `NOT VALID` + `VALIDATE`, backfill-then-set-not-null, expand/contract phasing.)
- **Rollback reads the journal, not files.** `migrate rollback` replays the inverses the database *recorded* when it applied each op — it never re-reads or trusts the on-disk edge files. A file edited after it was applied cannot mislead a rollback.

## The path-finder

Because edges form a graph rather than a line, "apply the pending migrations" is a graph search, not a sort. The **path-finder** walks the edge graph (live edges *and* the archive) from the database's recorded position to the head, choosing the shortest edge-count path and preferring consolidation edges as a tie-break. Overlapping consolidation ranges are forbidden at creation time, so the search is always unambiguous.

This is why a database stranded mid-way through a squashed range still works: the archived originals are reachable, and the path-finder routes through them. And a database stamped at a revision that a later rebase moved away from is *served forward* through `migrations/remap.json` rather than being orphaned.

## Preconditions and reconcile: catching drift by construction

Apply is bracketed by two checks that make schema drift a loud error instead of silent corruption:

- **Preconditions** run *before* each operation. They assert the database is in the state the edge expects — the object to be altered exists and matches, the object to be created is absent. A mismatch is a hard error naming the object, what was **expected**, and what was **found**. This is where a database that someone changed out-of-band gets caught, precisely and immediately.
- **Reconcile** runs *after* apply. It re-introspects the database and verifies it arrived at the target revision, listing every residual mismatch. Apply does not merely run SQL and hope; it certifies the result.

Comparisons that must judge whether the live database "matches" a definition use **live round-trip normalization**: the expected definition is round-tripped through the target database itself (a throwaway temp object plus PostgreSQL's own deparse), so PostgreSQL computes its own canonical form. This absorbs the catalog-dependent rewrites (how a cast materializes, for instance) that no offline normalizer can predict.

## Squash is composition, never a rewrite

`migrate squash` consolidates a range of edges into a single **consolidation edge** whose operation list is the ordered concatenation of the range. It is an *additional* edge from the range's start revision to its end revision; the originals retire intact to `migrations/archive/`. Nothing is rewritten, so:

- A database mid-range still resumes, via the archived originals.
- Every data operation in the range is preserved verbatim — a squash can never drop or fold a backfill.
- Apply-equivalence holds by construction: the consolidated op-list *is* the concatenation of what it supersedes.

## Forks: two heads

If two branches each append an edge to the same parent, the chain has two live heads. `migrate apply` refuses to guess and points you at `migrate rebase --head <ref>`, which re-parents one tail onto the other, re-simulating each moved edge to recompute its revision and content-derived filename. The rebased-away originals retire to the archive, and the remap table keeps any database stamped at an old revision applying forward.

## Adopting an existing database

Two commands bring a database that predates the chain (or was created by other means) under management:

- `migrate upgrade` — one-time adoption of a legacy pgdesign database (the old single tracking table, no chain position). It refuses to proceed if the schema and database have drifted, folds the existing applied history into the chain journal, and stamps an upgrade boundary. Run once per database.
- `migrate baseline` — adoption of a database whose schema was created by other means, or one that has *intentionally* drifted. It synthesizes a revision manifest from introspection and stamps a baseline boundary without executing any migration SQL.

Both boundaries are **rollback-frozen**: rollback is guaranteed only from the adoption boundary forward, because pgdesign has no recorded inverses for history it did not itself apply. Crossing a frozen boundary in rollback is a hard error.

## Why this matters

Every guarantee above is structural, not a matter of discipline:

- You cannot ship a migration whose contents disagree with its name.
- You cannot silently roll back over an operation that would lose data.
- You cannot apply to a database that has drifted without hearing about it, named object by named object.
- You cannot orphan a database by squashing or rebasing.

The chain is a small algebra — content-addressed objects, revisions, and edges — and these properties fall out of it rather than being bolted on. See the [Migration Guide](migration-guide.html) for the commands, flags, and worked examples.
