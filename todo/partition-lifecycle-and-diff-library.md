# Partition lifecycle completeness + expose diff/codegen scaffolding as importable library

## Context

A consumer project needs season-scale, high-volume time-partitioned event tables (rolling weekly/monthly partitions with a longer retention window) managed end-to-end by pgdesign, and is also building a schema-compiler-style tool of its own that would benefit from pgdesign's internal machinery as a library. Findings verified against source before filing.

## Part 1: partition lifecycle

### 1.1 Bug: interval/retention conflation (red-green fix required)

internal/generate/generate.go (~line 218) passes `t.Maintenance.Retention` as the `p_interval` argument of `partman.create_parent` (internal/sql/sql.go:683). Consequence: "monthly partitions, keep 6 months" is inexpressible — the partition interval and the retention window are forced to be the same value. Fix: add a distinct `interval` key to `[tables.X.maintenance]` (parse.go:1934, model/build.go:695), pass it as `p_interval`, keep `retention` for `partman.part_config.retention`. Write the failing test first (generated DDL for interval=1month/retention=6months), then fix.

### 1.2 No migrate/diff support for maintenance config

`grep Maintenance internal/diff internal/migrate internal/introspect` → zero hits. Changing `premake`/`retention`/(new)`interval` in TOML produces no migration ops. Want: maintenance-config diffing that emits `UPDATE partman.part_config` (and re-`create_parent` guidance where interval changes, which partman cannot do in place — surface as a documented Dangerous op).

### 1.3 partman-created children surface as drift

Introspection has no pg_partman awareness, so partitions created at runtime by `run_maintenance` appear as drift against the TOML. Want: introspection/diff recognize children of a partman-managed parent (via partman.part_config) and exclude them from drift, the same way generated-but-owned outputs are treated elsewhere.

### 1.4 Maintenance scheduling + extension setup

Generated DDL emits schema-qualified `partman.create_parent` but `CREATE EXTENSION pg_partman` without `SCHEMA partman` (internal/sql/sql.go:152), and nothing schedules `run_maintenance` (pg_cron or documented alternative). Want: correct extension/schema setup and an opt-in emitted pg_cron job (or a documented hard requirement that the consumer schedules it — no silent gap).

**Effort (1.1–1.4):** M total. 1.1 is S and highest priority (it blocks any rolling-with-retention setup).

## Part 2: expose internal machinery as importable library packages

A consumer building its own multi-language schema compiler (IDL → Go/TS codegen with structural evolution gating) wants to reuse rather than reimplement:

- **internal/diff** — the structural schema differ. Use case: diff two compiled schema models, classify hunks additive vs destructive, gate CI on the classification. This is generic model-diffing machinery, not Postgres-specific at its core.
- **The MultiFileGenerator / diagnostics pattern** (internal/codegen) — generator interface returning artifacts + diagnostics, deterministic output contract, per-generator golden tests.

**Solutions:**
- (a) **Recommended:** promote the reusable core to public packages (e.g., `pkg/diff`, `pkg/genkit`) with a documented stability posture; internal packages become thin adapters. Pros: ecosystem DRY, forces the interfaces to be clean. Cons: public API surface commitment on a pre-1.0 project (acceptable — breaking changes are minor bumps in 0.x per house rules).
- (b) Consumer copies the patterns. Pros: zero coupling. Cons: reimplementation of solved problems and permanent divergence — the exact outcome library exposure exists to prevent.

**Effort:** S (mechanical move + minimal docs) to M (API cleanup + stability documentation).

## Affected files

internal/parse/parse.go, internal/model/build.go, internal/generate/generate.go, internal/sql/sql.go, internal/diff/*, internal/migrate/*, internal/introspect/*, internal/codegen/*, docs/format-reference.md, docs/migration-guide.md.
