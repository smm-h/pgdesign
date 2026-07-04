# Function introspection crashes on "char" columns (provolatile, proparallel)

## Problem

`pgdesign diff --live` against PostgreSQL 18 crashes with:

```
error: functions for schema "game": can't scan into dest[5] (col: provolatile): cannot scan char (OID 18) in binary format into *string
```

## Root cause

`internal/introspect/introspect.go` lines 1737-1738 select `p.provolatile` and `p.proparallel` from `pg_proc`. Both are PostgreSQL `"char"` type (OID 18, a single-byte internal type). The scan destinations (lines 1767-1768) are Go `string`. pgx v5 in binary protocol mode has no decoder for `"char"` → `*string`.

Two columns affected:
- `p.provolatile` → `volatile string` (dest[5]) — crashes here
- `p.proparallel` → `parallel string` (dest[6]) — same bug, never reached

## Fix

Cast in the SQL query. Change:
```sql
p.provolatile,
p.proparallel,
```
to:
```sql
p.provolatile::text,
p.proparallel::text,
```

No Go struct changes needed. This is the standard pattern for `"char"` columns with pgx — cast at the query level so pgx receives `text` (OID 25).

## Impact

Blocks any `diff --live` or `introspect` operation against a database that has functions in any schema. Currently blocking a downstream project's deploy pipeline (the audit binary runs `pgdesign diff --live` post-deploy and the failure causes rollback).
