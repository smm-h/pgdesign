package diagnostic

// Diagnostic code allocation
//
// Range       | Owner package        | Description
// ----------- | -------------------- | ----------------------------------------
// E001-E019   | internal/parse       | Parse errors
// E100-E119   | internal/semtype     | Semantic type system errors
// E120-E129   | internal/model       | Model build errors
// E200-E299   | internal/validate    | Schema validation errors
// E300-E399   | internal/migrate     | Migration safety errors
// W001-W099   | internal/validate    | Schema validation warnings
//             | internal/parse       | (W001 is shared; see note below)
// W100-W199   | internal/audit       | Normal form audit warnings
//
// Individual codes:
//
// === Parse errors (E001-E019) ===
//
// E001  Cannot read file / directory / no schema files found
// E002  TOML parse error
// E010  TOML value type mismatch
// E011  Missing required TOML field
// E012  json_schema file not found
// E013  json_schema file is not valid JSON
//
// === Semantic type system errors (E100-E119) ===
//
// E100  User-defined type has empty name
// E101  Enum type must have at least one value
// E102  Scalar type must have a base PG type
// E103  Composite types not yet supported
// E104  Unknown type kind
// E105  Duplicate type name with conflicting definition
// E106  Unknown base type (not in allowlist, not extension type)
// E107  Scalar base type references another user type (circular)
// E108  Check expression missing VALUE placeholder
// E109  Enum default not in declared values
// E110  Default value contains embedded SQL quotes
// E117  Enum extends with no new values or overrides
// E118  Composite extends: field name collision
// E119  State machine extends: state name collision
//
// === Model build errors (E120-E129) ===
//
// E120  Table missing primary key
// E121  Cannot resolve column type
// E122  Policy missing or invalid "for" field
// E123  Policy must have "using" or "with_check"
// E124  (reserved)
//
// === Schema validation errors (E200-E299) ===
//
// E200  Column missing type (no PGType)
// E201  FK missing ON DELETE clause
// E202  Table/view/matview missing comment
// E203  Table missing primary key
// E204  FK references non-existent table or column
// E205  Column default contains embedded SQL quotes
// E206  Duplicate index
// E207  VARCHAR usage (prefer text + CHECK)
// E208  Timestamp without time zone (use timestamptz)
// E209  Serial/bigserial usage (use identity or uuid)
// E210  Float type on money column (use numeric)
// E211  Naming convention violation (snake_case)
// E212  FK columns missing covering index
// E213  Generated column references another generated column
// E214  Index opclass requires undeclared extension
// E215  RLS policy expression mismatch
// E216  Invalid WITH parameter for index method
// E217  Unknown index method
// E218  VIRTUAL generated column requires PG 18+
// E219  Index method requires undeclared extension
// E220  depends_on references non-existent entity
// E221  FD references unknown column name
// E222  RESTRICTIVE RLS policy requires PG 10+
// E223  State machine transition requires a column missing from the table
// E224  Column default doesn't match state machine's initial state
// E225  FK on_delete value is not a valid PostgreSQL action
// E226  Trigger name uses reserved "_pgdesign_sm_" prefix
// E227  [groups] references unknown table
//       NOTE: E227 is emitted by internal/model (build.go resolveGroups),
//       not internal/validate, despite living in the E200-E299 validate
//       range. Left in place deliberately -- do not move.
// E228  Append-only table FK on_delete (CASCADE/SET NULL/SET DEFAULT) lets
//       a DELETE elsewhere write into it, blocked by the append-only trigger
// E229  [suppress] key or [validate] disable targets an E-code (errors can
//       be neither suppressed nor disabled)
//
// === Migration safety errors (E300-E399) ===
//
// E300  ADD CONSTRAINT without NOT VALID on large table
//
// === Warnings (W001-W099) ===
//
// W001 has dual use: in internal/parse it signals an unknown/unrecognized
// TOML key; in internal/validate it signals a god table (too many columns).
// Both meanings coexist because parse warnings and validation warnings are
// emitted in separate phases and distinguished by their context message.
//
// W001  Unknown/unrecognized TOML key (parse) / God table (validate)
// W002  Orphan table (no FK relationships)
// W003  Boolean state explosion (3+ boolean columns)
// W004  JSONB array column could be separate table
// W005  Missing created_at on non-junction table
// W006  char(n) usage (prefer text)
// W007  Redundant index (prefix of another)
// W008  Circular FK dependency
// W009  RLS policy error_code not snake_case
// W010  Append-only table has update-suggesting column
// W011  RLS enabled without policies
// W012  RLS operation gap (missing policy for some operations)
// W013  CASCADE depth exceeds threshold
// W014  Single DELETE cascades to too many tables
// W015  Mixed ON DELETE actions on incoming FKs
// W016  PK columns in UNIQUE constraint (redundant)
// W017  NOT NULL column with CHECK (col IS NOT NULL) (redundant)
// W018  Domain CHECK + identical column CHECK (redundant)
// W019  Range CHECK subsumed by wider range CHECK
// W020  (reserved for dead column escalation with pg_stats null_frac=1.0)
// W021  Estimated row size exceeds page size (8192 bytes)
// W022  JSONB column without GIN index (workload)
// W023  Array column without GIN index (workload)
// W024  tsvector column without GIN index (workload)
// W025  Potential N+1 query pattern (workload)
// W026  Sequential scan heavy table (workload)
//
// === Info diagnostics (I001-I099) ===
//
// I001  Natural key candidate detected (from FD-derived candidate keys)
// I002  Dead column (not referenced by any constraint, index, policy, or generated column)
// I003  Estimated row size exceeds TOAST threshold (2048 bytes)
// I004  Column reordering could save significant padding
// I005  Timestamp on append-only table without BRIN index (workload)
// I006  Boolean column with dedicated index (low selectivity) (workload)
// I007  Table with 10+ indexes (write overhead) (workload)
//
// === Normal form audit warnings (W100-W199) ===
//
// W100  1NF violation (repeating group)
// W101  2NF violation (partial dependency)
// W102  3NF violation (transitive dependency)
// W103  BCNF violation
