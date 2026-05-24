# internal/audit

Normal form auditor. Detects 1NF, 2NF, 3NF violations using declared functional dependencies. Suggests decompositions.

## Function

`Audit(schema *model.Schema) []Diagnostic`

## Checks

### 1NF (heuristic)

Flag jsonb columns whose names suggest they hold repeating groups (e.g., "tags", "items", "values"). This is a warning -- jsonb is often legitimate. The heuristic looks for plural names and array-default columns.

### 2NF

For each table with a composite PK and declared functional dependencies:
1. Compute candidate keys via internal/fd.CandidateKeys
2. For each non-prime attribute A, check if any proper subset of any candidate key determines A
3. If yes: partial dependency detected. Report which attribute depends on which key subset.

### 3NF

For each table with declared functional dependencies:
1. Compute candidate keys
2. For each FD X->A in the declared dependencies:
   - If X is a superkey: OK (allowed by 3NF)
   - If A is a prime attribute (part of any candidate key): OK (allowed by 3NF)
   - Otherwise: transitive dependency violation. A is determined by non-superkey X.
3. Report the violation with: which FD causes it, which attributes should be extracted.

### Decomposition suggestions (Bernstein's synthesis)

When violations are found:
1. Compute minimal cover of all declared FDs (via internal/fd.MinimalCover)
2. Group FDs by left-hand side
3. For each group, suggest a new table containing all attributes in that group
4. If no suggested table contains a candidate key of the original, suggest adding one
5. Report as structured Diagnostic with Suggestion field containing the proposed decomposition

## Dependencies declaration

FDs are declared in TOML as:
```toml
[[tables.orders.dependencies]]
determinant = ["customer_id"]
dependent = ["customer_name", "customer_email"]
```

If no dependencies are declared on a table, NF audit is skipped for that table with an Info diagnostic: "No functional dependencies declared. NF audit skipped."

## --strict-nf

When enabled (global flag), NF violations are promoted to Error severity, causing `generate` to refuse DDL output. Default: NF findings are Warning severity.
