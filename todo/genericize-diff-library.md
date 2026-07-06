# Genericize the structural differ into a real pkg/diff

## Context

pkg/diff at 0.24.0 is an interface-only stub: its own doc header says "The genericization is deferred… Until step 3, consumers that need the differ import internal/diff directly" — which external consumers cannot do (internal/). `Model` exposes only `TableNames()`; `DiffResult` is a placeholder. The target abstractions are also Postgres-column-shaped (`TypeComparer` over SQL type strings, `ColumnChange` with nullability/collation).

A consumer building its own schema compiler (TOML IDL → multi-language codegen, with an evolution gate classifying schema changes as additive vs destructive) evaluated pkg/diff for its lock-file compat gate and concluded it is unusable in exported form today. The consumer is shipping a small native differ instead (its domain is field-number/union-tag tree diffing, which may never fit a PG-shaped abstraction) — so this todo is NOT blocking anyone. It is filed for the ecosystem: pkg/genkit proved immediately reusable (CompareFreshness, ScanOrphans, determinism contract — adopted wholesale); pkg/diff should reach the same bar or be explicitly descoped.

## Problem

1. pkg/diff advertises a public API that cannot be used: interfaces without an engine, `Model` stub, placeholder result types. An exported package that silently requires internal/ imports is worse than no export — consumers discover the gap after designing against it.
2. The deferred "step 3" genericization has no tracking; the doc header's promise points nowhere.

## Solutions

- (a) **Complete the genericization**: extract the differ engine from internal/diff behind the pkg/diff interfaces with a domain-neutral model (nodes, typed attributes, identity keys) and a classification callback (consumer decides additive/destructive per hunk); internal/diff becomes an adapter binding the PG model. Pros: real ecosystem reuse (the freshness/orphan/diff triad becomes a complete schema-tooling kit alongside genkit). Cons: the current engine's PG assumptions (SQL type comparison, column semantics) may resist extraction; genuine design work.
- (b) **Descope honestly**: delete the stub interfaces (or move them back to internal/), update the doc header to say the differ is PG-internal by design, and let consumers build domain differs natively. Pros: no misleading API surface; zero effort beyond cleanup. Cons: closes the reuse door.
- (c) Middle path: keep pkg/diff but re-document it as "reserved, unimplemented — do not consume; see this todo," and decide (a) vs (b) when a second concrete consumer appears.

## Affected

pkg/diff/*, internal/diff/* (if extracting), doc headers, CHANGELOG.

## Effort

(a) M–L depending on how PG-entangled the engine is; (b) S; (c) XS.
