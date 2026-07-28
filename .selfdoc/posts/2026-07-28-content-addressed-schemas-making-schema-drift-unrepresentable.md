---
title: "Content-Addressed Schemas: Making Schema Drift Unrepresentable"
date: 2026-07-28
slug: content-addressed-schemas-making-schema-drift-unrepresentable
tags: ["postgresql", "schema", "migrations", "content-addressing", "database"]
draft: false
project: pgdesign
---

Every team that has run a database for more than a year has met the same ghost. The schema in your repository says one thing. The database in production says something subtly different. A column was widened by hand during an incident. A migration was edited after it ran. An index was added directly in `psql` and never written down. Nobody is sure. The gap between what your schema *says* and what your database *is* — schema drift — is where a surprising amount of production pain lives.

pgdesign is a PostgreSQL schema compiler: you write your schema declaratively in TOML, and it produces SQL DDL, migrations, diagrams, docs, and type-safe client code. But the interesting part isn't the compilation. It's a design decision underneath it: **every schema has an identity derived from its content**, and every artifact and every migration is tied to that identity. This turns a whole family of drift bugs from "things you prevent with discipline" into "things that are not representable." This post is about that idea and the three problems it dissolves.

## The trust gap

Consider what a conventional migration tool actually gives you. A folder of files named `0001_init.sql`, `0002_add_users.sql`, and so on, plus a table in the database recording which numbers have run. The numbers are assigned by whoever writes the file. The tool trusts them completely: it applies files in numeric order, and it trusts that file `0007` today contains what file `0007` contained when it ran on production six months ago.

None of that trust is verified. And each unverified assumption is a real bug that ships:

- Two developers branch, both add `0008`, both merge. Now there are two `0008`s, or a renumbering scramble, and the ordering the tool applies is not the ordering anyone intended.
- Someone fixes a typo in an already-applied migration file. Staging re-runs from scratch and gets the fixed version; production ran the old one. The two databases now differ, and nothing notices.
- A migration's filename says `add_status_column` but a bad merge left it adding the wrong default. The name is a comment; the tool cannot check it against the contents.
- A `DROP COLUMN` migration has a hand-written "down" that recreates the column — empty. Roll back and the data is gone, silently, because the down script *looked* symmetric.

The common thread: the tool's notion of identity (a filename, a number) is disconnected from the thing's actual content. Everything downstream is trust without verification.

## Identity from content

pgdesign borrows the fix from git. In git, a commit isn't identified by a sequence number you assign; it's identified by the hash of its contents. You cannot have two different commits with the same id, and you cannot alter a commit's contents without changing its id. Identity *is* content.

pgdesign applies this at two levels.

First, the whole schema. pgdesign takes your fully-resolved schema — every table, column, type, view, function, comment, and version target — and encodes it into a canonical byte form, then hashes it. The result is a **revision**: a single fingerprint of your entire schema at a point in time. "Canonical" is doing real work here. Reordering your table definitions doesn't change the revision. Writing a check constraint as `x IN (1, 2)` in one place and `x = ANY(ARRAY[1, 2])` in another doesn't produce two different revisions, because pgdesign normalizes semantically-equal expressions to the same form before hashing. Two schemas have the same revision if and only if they mean the same thing to PostgreSQL.

Second, each migration. A migration in pgdesign is an **edge**: a transition from one revision to another, carrying a list of operations. Its identity is the hash of its contents, and its filename is derived from that hash plus a human-readable slug. The operations reference the objects they touch by *their* content ids, drawn from a content-addressed object store — the same Merkle-DAG trick git uses for trees and blobs.

That one change — identity from content, at both levels — is what dissolves the problems above.

## Problem one: migrations that lie

Because an edge references its objects by content id, it physically cannot claim to do one thing while carrying another. The id of the "new column definition" *is* the definition. There is no filename to disagree with the contents, because the filename is computed *from* the contents.

This also kills branch collisions without a coordination protocol. Two branches making different changes produce edges with different hashes; they never collide, because there's no counter to fight over. What they produce instead is a **fork** — two heads in the edge graph — which pgdesign detects and refuses to guess about, pointing you at an explicit `rebase` to resolve. Two branches making the *same* change produce byte-identical edges with the same name, so git sees nothing to merge. The tool tells the truth about divergence because divergence is visible in the content.

And regeneration is honest. Ask pgdesign to generate the migration for an unchanged schema and you get the exact same edge, byte for byte. No spurious churn, no "did something change or did the generator just reformat?" The output is a pure function of the input.

## Problem two: the drifted database, caught by name

Here's the scenario that keeps people up at night. Your migration runs `ALTER TABLE orders ALTER COLUMN total TYPE numeric(12,2)`. But three weeks ago, someone changed `total` by hand during an incident and it's already `numeric(14,2)`. What happens?

In most tools: the ALTER runs (or errors cryptically), and either way nobody learns that the database had drifted from the schema's assumptions. The drift is absorbed.

pgdesign brackets every apply with two checks derived from treating the database as a state the migration is a function on.

Before each operation, a **precondition** asserts the database is where the edge says it should be. Not just "does the column exist" — does it match what this edge assumes it's transforming? When it doesn't, you get a hard error that names the object, what was **expected**, and what was **found**:

```
precondition failed for alter_column_type on orders.total:
  expected: numeric(12,2)
  found:    numeric(14,2)
the database has drifted from the schema this migration was generated against
```

Drift stops being a silent absorption and becomes a named, located, loud failure — before any change is made.

After apply, a **reconcile** step re-introspects the database and verifies it actually arrived at the target revision, listing every residual mismatch. Applying isn't "run the SQL and hope"; it's "run the SQL and then certify the result matches the revision you asked for." If PostgreSQL did something the migration didn't anticipate, you hear about it immediately.

There's an honest subtlety worth naming: deciding whether a live column "matches" a definition is harder than string comparison, because PostgreSQL rewrites things internally (how a cast materializes, how a default is stored). pgdesign handles the part it can compute offline with its normalizer, and for the catalog-dependent residue it round-trips the expected definition *through the target database itself* — creating a throwaway object and asking PostgreSQL for its own canonical form. It lets the authority answer the question only the authority can. This is a real boundary, and pgdesign draws it explicitly rather than pretending it isn't there.

## Problem three: two artifacts that cannot silently diverge

Modern schemas fan out into generated artifacts: DDL, an ER diagram, GraphQL types, and client code in several languages. The classic failure is drift *among these*: you regenerate the TypeScript but forget the Python, and now two "generated from the schema" files describe different schemas. Nobody notices until a mismatch causes a runtime error in a service nobody was looking at.

Because pgdesign's whole schema has a single revision, it stamps that revision into every artifact it produces. The generated Go carries a revision. So does the generated Python, the DDL, the JSON snapshot. Freshness stops being a vibe and becomes an equality check: an artifact is stale exactly when its stamp doesn't equal the current schema's revision.

A single CI check, `pgdesign check --tag revision`, then enforces the invariant across the whole project: every regenerable artifact must carry the current revision. Two generated files *cannot* silently describe different schemas, because staleness in either one is a concrete, checkable inequality, not something a human has to spot in a diff. The one-command workflow, `pgdesign revise`, regenerates everything and stamps it consistently, so the normal path produces a coherent set by construction; the check catches the abnormal path.

The same provenance idea powers a precise taxonomy of who's allowed to write what. Full regenerators (`build`, `revise`) are always fine. The one partial writer that exists (`codegen --output` for a single target) refuses to run when its non-rewritten siblings would be left at a different revision. Source editors like the formatter announce that they changed the revision. The rule isn't a list of dos and don'ts; it's derived from a single principle — every artifact carries the revision that produced it — and everything else follows.

## The rename that isn't a rename

One more, because it's the sharpest example of "make the dangerous thing unrepresentable."

Rename a column in your schema from `email_addr` to `email`. What does a diff-based migration tool see? A column named `email_addr` that's gone, and a column named `email` that's new. The obvious migration: drop `email_addr`, add `email`. That is a data-loss bomb with a friendly face — it does exactly what the diff literally says and destroys every value in the column.

pgdesign detects this at generation time. When it sees a column being dropped and another added whose definition is identical except for the name, it recognizes a *plausible rename* and refuses to generate the migration unless you've declared the rename explicitly:

```toml
[renames]
columns = [ { table = "users", from = "email_addr", to = "email" } ]
```

Declare it, and you get a data-preserving `ALTER TABLE ... RENAME COLUMN` — which, as a bonus, is cleanly reversible. Don't declare it, and generation stops with an error naming the pair. If the drop-and-add really was intentional (two genuinely different columns that happen to look alike), you make them differ — even a comment suffices — and the gate steps aside. The default is safe; the dangerous operation requires you to say, on the record, that you mean it.

## By construction, not by discipline

None of this is a linter you can forget to run or a convention that erodes under deadline pressure. The guarantees come from the shape of the data model — content-addressed objects, revisions, and a chain of edges with recorded inverses, preconditions, a journal, and reconcile:

- You cannot ship a migration whose contents disagree with its name, because the name is computed from the contents.
- You cannot silently roll back over an operation that would lose data, because rollback replays the inverses the database *recorded*, and irreversible operations are typed as such.
- You cannot apply to a drifted database without a named, located error.
- You cannot let two generated artifacts silently describe different schemas, because both carry the revision and the check is an equality.

Schema drift is the accumulated cost of a hundred small unverified assumptions. The fix isn't more discipline; it's removing the assumptions. When identity comes from content, most of them simply have nowhere to hide.

pgdesign is open source. If any of these problems are familiar ghosts, the [documentation](https://pgdesign.smmh.dev) — start with the quickstart and *The Migration Chain* — is the place to begin.
