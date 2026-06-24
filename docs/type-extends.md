---
title: "Type Extends"
description: "How pgdesign type derivation works for scalars, enums, composites, and state machines using extends with sealed, overridable, and additive field semantics."
---

# Type Extends

Type derivation allows defining new types that inherit from and extend existing types. This works for all four type kinds: scalar, enum, composite, and state machine.

## Syntax

Add `extends = "parent_type_name"` to any `[types.*]` section in your schema TOML to derive a new type from an existing one. The parent type must be either a builtin type or another user-defined type declared in the same schema. Types with extends references are topologically sorted before processing, so parent types are always resolved before their children regardless of declaration order in the TOML file.

```toml
[types.strict_email]
extends = "email"
check = "LENGTH(VALUE) <= 100"
```

The parent type must be either a builtin type or another user-defined type. Types with extends references are topologically sorted before processing, so parent types are always loaded before their children.

## Per-Kind Semantics

### Scalar Extends

Scalar extends performs field overlay where the child inherits all fields from the parent and can selectively override non-sealed fields like not_null, default, check, and comment. The base PostgreSQL type and kind are sealed and cannot be changed by the child, ensuring type safety across the derivation chain. This pattern is useful for creating specialized variants of domain types with different constraints or defaults.

```toml
[types.email]
base_type = "text"
check = "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'"

[types.strict_email]
extends = "email"
check = "LENGTH(VALUE) <= 100"
```

Result: `strict_email` has base type `text` (inherited), overridden check constraint, and inherits not_null from parent.

Overridable fields: `not_null`, `default`, `default_expr`, `check`, `unique`, `array`, `comment`.

### Enum Extends

Enum extends is additive, meaning the child inherits all parent values and can declare additional ones that are appended to the parent's value list. Duplicate values that already exist in the parent are silently deduplicated. If the child provides no new values and no field overrides, E117 is emitted as a warning since the derivation has no effect. The child can also override default, not_null, comment, and array fields.

```toml
[types.color]
kind = "enum"
values = ["red", "green", "blue"]

[types.extended_color]
kind = "enum"
extends = "color"
values = ["yellow", "cyan", "magenta"]
```

Result: `extended_color` has values `["red", "green", "blue", "yellow", "cyan", "magenta"]`. Duplicate values (already in parent) are silently deduplicated. If the child provides no new values and no field overrides, E117 is emitted as a warning.

Overridable fields: `default`, `not_null`, `comment`, `array`.

### Composite Extends

Composite extends is additive, meaning the child inherits all parent fields and declares additional ones that are merged into the parent's field set. Field name collisions between parent and child are a hard error (E118) because implicit field overriding in composite types would create ambiguity about which definition takes precedence. The child can only override the comment field from the parent; all structural fields like kind and existing field definitions are sealed.

```toml
[types.address]
kind = "composite"

[types.address.fields]
street = "text"
city = "text"

[types.full_address]
kind = "composite"
extends = "address"

[types.full_address.fields]
state = "text"
zip = "varchar"
```

Result: `full_address` has fields `street`, `city` (from parent), plus `state`, `zip` (from child).

Overridable fields: `comment`.

### State Machine Extends

State machine extends is additive for both states and transitions, allowing the child to define new states and new transition rules that are merged with the parent's definitions. State name collisions between parent and child are a hard error (E119) to prevent ambiguous state definitions. The child can override `initial`, `enforce_trigger`, and `comment` fields. After the merge, reachability validation runs from the final initial state to ensure all states in the combined machine are reachable.

```toml
[types.order_status]
kind = "state_machine"
initial = "pending"

[[types.order_status.states]]
name = "pending"

[[types.order_status.states]]
name = "confirmed"

[[types.order_status.transitions]]
name = "confirm"
from = ["pending"]
to = "confirmed"

[types.extended_order_status]
kind = "state_machine"
extends = "order_status"

[[types.extended_order_status.states]]
name = "shipped"
terminal = true

[[types.extended_order_status.transitions]]
name = "ship"
from = ["confirmed"]
to = "shipped"
```

Result: `extended_order_status` has states `pending`, `confirmed`, `shipped` and transitions `confirm`, `ship`. Initial state defaults to parent's (`pending`) unless overridden. After merge, reachability is validated from the (possibly overridden) initial state.

Overridable fields: `initial`, `enforce_trigger`, `comment`.

## Sealed vs Overridable vs Additive Fields

| Field | Scalar | Enum | Composite | State Machine |
|-------|--------|------|-----------|---------------|
| Kind | Sealed | Sealed | Sealed | Sealed |
| BaseType.Base | Sealed | N/A | N/A | N/A |
| not_null | Overridable | Overridable | N/A | N/A |
| default | Overridable | Overridable | N/A | N/A |
| default_expr | Overridable | N/A | N/A | N/A |
| check | Overridable | N/A | N/A | N/A |
| unique | Overridable | N/A | N/A | N/A |
| array | Overridable | Overridable | N/A | N/A |
| comment | Overridable | Overridable | Overridable | Overridable |
| values | N/A | Additive | N/A | N/A |
| fields | N/A | N/A | Additive | N/A |
| states | N/A | N/A | N/A | Additive |
| transitions | N/A | N/A | N/A | Additive |
| initial | N/A | N/A | N/A | Overridable |
| enforce_trigger | N/A | N/A | N/A | Overridable |

Sealed fields cannot be changed. Attempting to change a sealed field produces E114.

## Self-Shadowing (Overriding Builtins)

User-defined types can shadow builtin types by declaring a type with the same name, such as redefining `id` to use a different UUID generation function. This is not `extends` but rather a direct re-registration with sealed field enforcement. The sealed fields (kind and base type) must match the builtin being shadowed, or E114 is emitted. Successful shadowing produces an I101 informational diagnostic so the override is visible during validation.

```toml
[types.id]
base_type = "uuid"
default_expr = "uuid_generate_v4()"
comment = "Custom UUID using uuid-ossp"
```

This shadows the builtin `id` type (which uses `gen_random_uuid()`). Because the sealed fields match (Kind=scalar, BaseType.Base=uuid), the shadowing succeeds and emits I101. Attempting to shadow `id` with a different base type (e.g., `bigint`) produces E114.

Types can also shadow builtins via extends:

```toml
[types.id]
extends = "id"
default_expr = "uuid_generate_v4()"
```

This is equivalent: extends resolves the builtin `id`, merges the child's overrides, and registers the result under the same name.

## Multi-File Schema Behavior

When a schema is spread across multiple TOML files, all `[types.*]` sections are collected before processing. The topological sort ensures correct ordering regardless of which file defines the parent vs child type. Identical type definitions across files are accepted silently (idempotent for multi-file schemas).

## Error Cases

| Code | Severity | Condition |
|------|----------|-----------|
| E114 | Error | Sealed field violation: Kind or BaseType.Base differs from parent/builtin |
| E115 | Error | Circular extends reference (detected by topological sort) |
| E116 | Error | Extends target not found in registry or current batch |
| E117 | Warning | Enum extends with no new values and no field overrides |
| E118 | Error | Composite extends: field name exists in both parent and child |
| E119 | Error | State machine extends: state name exists in both parent and child |

Additional codes related to builtin shadowing:

| Code | Severity | Condition |
|------|----------|-----------|
| I101 | Info | Type successfully shadows a builtin type |
| E105 | Error | Duplicate type name with conflicting definition (non-builtin) |

## Source Tracking

The `Source` field on `TypeDef` tracks the origin of each type definition, distinguishing between types that were registered as builtins during initialization, defined directly by the user in TOML, or created through extends derivation. This tracking enables diagnostic messages to accurately report where a type came from and helps the validation engine distinguish between intentional shadowing of builtins and accidental name collisions.

- `"builtin"` -- registered by `NewBuiltinRegistry()` (e.g., id, ref, timestamp, money)
- `"user"` -- registered by `LoadUserTypes()` without extends
- `"extended"` -- registered by `LoadUserTypes()` via extends derivation
