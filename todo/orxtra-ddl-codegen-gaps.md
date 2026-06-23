# DDL codegen gaps for consumer adoption

## Context

A consumer monorepo tested `pgdesign codegen --lang python --mode ddl` against its 16-table schema (v0.15.0). The DDL output is a viable replacement for hand-maintained schema files, but three gaps prevent direct adoption.

## Gap 1: UUID v7 support

The consumer uses `uuid_generate_v7()` (from pg-uuidv7 extension) for time-ordered UUIDs on all primary keys. pgdesign emits `gen_random_uuid()` (v4). The TOML schema has no way to specify UUID version or the extension that provides the function.

Possible fix: a `uuid_version` field on the schema or type definition, or an `extensions` config that maps `uuid` to a specific generation function.

## Gap 2: NUMERIC precision

The consumer uses `NUMERIC(12, 6)` for cost columns. pgdesign emits bare `numeric` because the `amount` type has no precision specifier. The TOML type system doesn't currently support precision/scale on numeric types.

Possible fix: add `precision` and `scale` fields to the `amount` type definition, or allow them on column definitions that use `amount`.

## Gap 3: LISTEN/NOTIFY triggers

The consumer has a PL/pgSQL function + trigger that fires `pg_notify('orxtra_events', ...)` on INSERT into the events table for real-time event streaming. This is application-specific and likely not derivable from the schema alone.

Possible fix: a `triggers` section in the TOML that allows arbitrary trigger definitions, or a `post_ddl` section for custom SQL that gets included in the generated output. Alternatively, document that application-specific triggers should be maintained in a supplement file alongside the generated DDL.

## Impact

With these three gaps resolved, the consumer could replace 333 lines of hand-maintained Python DDL with pgdesign-generated output (1,130 lines with executor, idempotent operations, and verification). The query-layer output (7,985 lines) could also replace hand-maintained storage backends (~1,500 lines), though the consumer's domain-specific protocols would remain hand-written.
