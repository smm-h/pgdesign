# Cross-framework schema composition

## Context

When a product (app) extends a framework (library) and both use pgdesign, their tables coexist in the same PostgreSQL database. The app needs to reference the framework's tables via foreign keys, and both schemas need to be applied to the same database in dependency order.

Today pgdesign has no awareness of this relationship. Each project generates DDL independently. Cross-schema FKs must be written as raw SQL outside pgdesign, which means pgdesign can't validate them, track them in migrations, or include them in its dependency graph.

Real example: a product with an `app` schema has a `users` table. A framework with a `public` schema has a `principals` table (unified identity model), an `events` table, a `subscriptions` table, and a `sources` table. The product's `users` table needs `principal_id UUID REFERENCES public.principals(id)`. The product fires events into the framework's `events` table attributed to principals. The product creates subscriptions owned by principals.

## Core feature: schema imports

A pgdesign project should be able to declare that another pgdesign project's tables exist in the same database and reference them.

### What it enables

- `app.users.principal_id REFERENCES public.principals(id)` — FK from app table to framework table, validated by pgdesign
- Dependency-ordered DDL generation — framework tables created before app tables
- Migration awareness — pgdesign knows the FK exists and includes it in migration diffs
- `pgdesign check` validates that referenced tables/columns actually exist in the imported schema
- `pgdesign build` for the app project produces DDL that includes or depends on the framework's DDL

### Minimal design

In the app project's `pgdesign.toml`:

```toml
[imports]
orxtra = { path = "../orxtra/schema", schema = "public" }
```

This tells pgdesign: "the tables defined in `../orxtra/schema/*.toml` exist in the `public` schema in the same database." The app can then reference them in its own TOML:

```toml
[tables.users.columns.principal_id]
type = "ref"
references = "orxtra:principals(id)"  # or "public.principals(id)"
on_delete = "RESTRICT"
```

pgdesign resolves `orxtra:principals` by reading the imported project's TOML, validating that `principals` has an `id` column of the right type, and emitting the FK in the app's DDL.

### Open questions

- **Path vs registry.** Should imports use relative paths, or should there be a registry (like a lockfile) that maps names to resolved schemas? Paths are simpler but break on different machines. A registry adds indirection but is reproducible.
- **Schema prefix.** Should the import declaration specify the target schema (`public`), or should pgdesign read it from the imported project's config? The imported project may not know what schema the consumer will place it in.
- **Version pinning.** Should imports pin to a specific version of the framework's schema? If the framework adds a column or changes a type, the app's FK might break. A hash or version tag on the import would catch this.
- **Generated DDL scope.** Should `pgdesign build` for the app project emit only the app's DDL (assuming the framework's DDL was already applied), or should it emit both? "Only app" is simpler but requires the consumer to know the apply order. "Both" is self-contained but duplicates the framework's DDL.
- **Circular references.** Two projects importing each other is almost certainly wrong, but should pgdesign detect and reject it, or just document the constraint?

## Nice to have: shared enum types

If the framework defines an enum (e.g., `trust_tier` with values `anonymous`, `identified`, `verified`, `system`), the app should be able to use that enum in its own tables without redefining it. Today enums are per-project — there's no way to say "use the `trust_tier` enum from the imported schema."

```toml
[tables.audit_log.columns.actor_trust]
type = "enum"
enum = "orxtra:trust_tier"  # defined in the imported project
```

pgdesign validates the enum exists in the import and emits the correct type reference in DDL.

## Nice to have: cross-project migration coordination

When the framework ships a migration (e.g., adds a column to `principals`), the app's migration needs to be aware of it — especially if the app has FKs pointing at the changed table. Today each project generates migrations independently with no cross-project awareness.

Minimal version: `pgdesign migrate generate` in the app project detects that the imported schema has changed since the last app migration and warns. It doesn't generate the framework's migration — that's the framework's responsibility — but it flags that the app's FK targets may have shifted.

Full version: `pgdesign migrate generate` can produce a coordinated migration plan that includes both the framework's schema changes and the app's dependent changes, in dependency order. This is item 22 from the existing deferred todos but scoped to the two-project case rather than arbitrary multi-repo.

## Nice to have: test fixture composition

When testing the app, the test database needs both the framework's tables and the app's tables. Today each project generates its own test fixtures. If the app imports the framework's schema, `pgdesign codegen --test` (item 21 from the existing deferred todos) should produce a combined test fixture that sets up both schemas in the right order.

## Nice to have: codegen for imported types

If the framework generates Python types from its schema (StrEnum files, dataclasses), the app should be able to import those types rather than regenerating them. pgdesign's codegen for the app project should reference the framework's generated types, not duplicate them.

This is less about pgdesign generating code and more about pgdesign's codegen being aware that certain types already exist as Python imports from the framework package, so it doesn't re-emit them.

## Effort estimate

- Core feature (schema imports with FK validation): medium. Requires TOML import resolution, cross-project table/column lookup, FK validation, and DDL emission with schema-qualified references.
- Shared enum types: small on top of imports (enum lookup instead of table lookup).
- Migration coordination: large (cross-project diff awareness).
- Test fixture composition: medium (ordered DDL concatenation with extension dedup).
- Codegen import awareness: small (skip re-emission of imported types).

## Relationship to existing todos

- Item 22 (dependency-aware multi-repo codegen) covers the most ambitious version of this. This todo is the pragmatic subset scoped to the two-project case.
- Item 21 (test schema mode) benefits from composition — test fixtures need both schemas.
- Item 18 (atomic migration codegen) benefits from cross-project awareness — a framework enum change should trigger app-side type regeneration.
