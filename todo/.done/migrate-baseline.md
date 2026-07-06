# pgdesign migrate baseline command

## Problem

There is no way to adopt pgdesign's migration system in a project that already has an existing production database with all current schema applied via a different mechanism. A project with 15+ hand-written migrations (applied via a bash loop and a `schema_migrations` table) cannot transition to pgdesign-native migrations without replaying history or manually inserting tracking records.

## Proposed solution

Add a `pgdesign migrate baseline` command that marks an existing production database as "all current schema applied" by inserting a record into pgdesign's migration tracking table (`pgdesign_migrations`) without actually running any SQL.

The command would:

1. Verify the target database is reachable.
2. Create the `pgdesign_migrations` table if it does not exist.
3. Record all currently-defined migrations as "applied" (with a timestamp and a flag indicating baseline, not actual execution).
4. Not execute any migration SQL against the database.

This allows projects to transition to pgdesign-native migrations going forward, with new migrations running normally through `pgdesign migrate run`, while acknowledging that the existing schema was applied through a different mechanism.

## Use case

Adopting pgdesign in a project that has 15+ existing tables defined in pgdesign TOML schemas, with corresponding hand-written SQL already applied to production via a different tool. The baseline command bridges the gap between "schema already exists in production" and "pgdesign now manages migrations."
