---
title: "Cross-Repository Imports"
description: "Reference another pgdesign project's tables via git pins with locking, vendoring, foreign key references, and column-level semantic drift detection."
---

# Cross-Repository Imports

Imports let one pgdesign project reference tables owned by *another* pgdesign project — a shared "framework" schema, a platform team's user table, a billing service's accounts — across a git dependency, without copying the definitions by hand or coupling the two repositories at build time.

The imported surface is treated as a pinned, vendored sub-model: you pin it to a git ref, `import lock` vendors exactly the referenced tables plus the types they depend on into your repository, and a CI check tells you when the upstream has *semantically* changed in a way that affects you.

## Declaring an import

Imports are declared in `pgdesign.toml` under `[imports]`. Each entry maps a local **alias** to a git URL, a git ref to pin, and the PostgreSQL **schema** (namespace) that the imported tables live in:

```toml
[imports]
platform = { git = "https://github.com/acme/platform-schema.git", ref = "v2.4.0", schema = "platform" }
```

Both the inline-table form above and the expanded form are accepted:

```toml
[imports.platform]
git    = "https://github.com/acme/platform-schema.git"
ref    = "v2.4.0"
schema = "platform"
```

| Key | Description |
|-----|-------------|
| `git` | Fetch URL of the upstream pgdesign project |
| `ref` | Git ref to pin (tag, branch, or commit); resolved to an exact commit at lock time |
| `schema` | The PostgreSQL namespace an `alias:table` reference resolves into |

## Referencing an imported table

An imported table is referenced by prefixing its name with the alias and a colon, and this is allowed in exactly **one** place: a foreign key's `ref_table`. Alias resolution happens before the dot-split for schema qualification, so `platform:users` resolves the alias `platform` and names the table `users` in that import.

```toml
[tables.orders]
comment = "Customer orders"

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.customer_id]
type = "ref"

[tables.orders.fks.fk_orders_customer]
columns = ["customer_id"]
ref_table = "platform:users"
ref_columns = ["id"]
on_delete = "RESTRICT"
```

Using an alias anywhere other than a foreign key `ref_table` — in `depends_on`, in a view query, anywhere else — is a hard error that names the supported site. A typo'd alias is a resolution error naming the unknown alias, never a silently-invented phantom schema.

## Locking and vendoring: `import lock`

Declaring an import is not enough to build -- the upstream schema must be resolved, fetched, and vendored locally. Run `import lock` to resolve each pin and vendor its surface into the `imports/` directory:

```
pgdesign import lock
```

This resolves each alias's git ref to an exact commit, fetches and parses the upstream project's TOML, and vendors into `imports/<alias>/`:

- every table you reference, plus
- the transitive closure of the type definitions those tables depend on (enums, domains, composite types), each stored by its per-object content id,

along with a **lockfile** entry recording 4 fields: the URL, the pinned ref, the resolved commit, and a hash of the vendored surface. The vendored surface is committed to your repository, so builds are reproducible and work offline -- pgdesign never reaches out to the network during a normal build.

`import lock` refuses to overwrite an existing lockfile. To re-pin an import (for example, to adopt a new upstream release), use `import update`:

```
pgdesign import update platform
```

`import update` re-resolves the git ref, re-vendors the surface, and updates the lockfile.

## Detecting drift: `check --tag imports`

Because the vendored surface is committed, it can go stale relative to what your schema actually needs, or relative to what the lockfile records. `check --tag imports` re-derives the surface and reports problems, hard-failing CI:

```
pgdesign check --tag imports
```

The check reports, among others:

- A vendored surface whose hash no longer matches the lockfile (the vendored objects or lockfile were altered out of band).
- A vendored surface that has **semantically** drifted from the lockfile — compared at the *column level* through pgdesign's normalizer, so an equivalently-spelled default (`now()` vs `CURRENT_TIMESTAMP`) does **not** register as drift, but a real type change does. When the upstream framework legitimately changed, `import update` re-pins.
- A foreign key referencing an imported table or column that is not in the vendored surface (run `import update`).
- A junction-type mismatch: your local FK column's type disagreeing with the imported column it references.
- An import whose required PostgreSQL version floor exceeds your project's target version.

The import diagnostics are `E230`–`E244`; see the [Validation Rules](validation-rules.html#imports) reference for the full list.

## How imported tables behave

Imported tables are facts owned elsewhere -- your project references them but never generates DDL, audits, migrates, or fabricates data for them. This ownership boundary is enforced across all 6 major subsystems:

- **DDL, audit, and codegen emit zero imported artifacts.** Your generated schema does not `CREATE` an imported table; it only references it in foreign keys, schema-qualified.
- **Diff and migrate exclude imported tables.** Your migration chain never tries to alter something another project owns. Reconcile does not auto-add imported schemas.
- **Validation is fail-closed.** FK validation, orphan detection, dead-column analysis, and the FK graph all resolve imported targets correctly, so an FK to an imported table is neither a spurious error nor a phantom node.
- **Seed data resolves imported foreign keys through tiers.** With a database (`--db`), seed draws real keys from the live imported table. Offline, it emits count-wrapped ordered-offset subqueries in `INSERT` mode. Cases that genuinely cannot be satisfied offline — a `UNIQUE` distinguished solely by an imported FK column, or `--format copy` with a `NOT NULL` imported FK and no database — are hard errors (`S002`–`S004`) that name every constraint involved, never a silently-wrong fabrication.
- **Diagrams render imported tables as minimal reference shapes** — a distinct shape class so an imported table reads as "owned elsewhere" at a glance.

## Live verification

When a database is reachable (for example during `revise`'s database tier), pgdesign additionally verifies that each imported table actually exists in the live database with the referenced columns, catching a deployment where the upstream schema was never applied.
