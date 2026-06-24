---
title: "State Machines"
description: "How pgdesign defines state machine types with enforced transitions via triggers, D2 state diagrams, diff support, and codegen transition methods."
---

# State Machines

pgdesign supports state machine types that enforce valid state transitions at the database level via triggers, generate D2 state diagrams, and produce transition methods in codegen output.

## TOML Syntax

State machines are defined in the `[types.*]` section with `kind = "state_machine"`, declaring the valid states, allowed transitions between them, and the initial state. Each state machine produces a CHECK constraint for value validation, a BEFORE UPDATE trigger function for transition enforcement, and can optionally generate D2 state diagrams and type-safe transition methods in codegen output across all supported languages.

```toml
[types.order_status]
kind = "state_machine"
base_type = "text"
initial = "pending"
states = ["pending", "confirmed", "shipped", "cancelled", "suspended"]

[types.order_status.transitions]
pending = ["confirmed", "cancelled"]
confirmed = ["shipped", "suspended"]
suspended = ["confirmed"]
```

A column using the state machine type:

```toml
[tables.orders.columns.status]
type = "order_status"
not_null = true
```

### Named Transitions

Named transitions provide human-readable names for state changes and support additional required parameters that must be supplied when performing the transition. Each named transition specifies the set of valid source states, a single target state, and an optional `requires` map of parameter names to their PostgreSQL types. These named transitions drive the codegen output, producing type-safe methods with the required parameters as function arguments.

```toml
[[types.order_status.named_transitions]]
name = "confirm"
from = ["pending", "suspended"]
to = "confirmed"

[[types.order_status.named_transitions]]
name = "suspend"
from = ["confirmed"]
to = "suspended"
requires = { suspended_reason = "text" }
```

The `requires` field declares additional parameters needed when performing the transition. These map to extra columns on the table that get updated alongside the state change.

## DDL Generation

### CHECK Constraint

A CHECK constraint ensures the column value is always one of the declared valid states. This constraint is enforced at the database level on both INSERT and UPDATE operations, preventing any row from containing an undeclared state value. The constraint uses an IN list of all declared state values and is named following the `chk_<table>_<column>_valid` convention for consistent identification across the schema.

```sql
ALTER TABLE orders
  ADD CONSTRAINT chk_orders_status_valid
  CHECK (status IN ('pending', 'confirmed', 'shipped', 'cancelled', 'suspended'));
```

### Trigger Enforcement

A BEFORE UPDATE trigger validates that every state change follows the declared transition rules. The trigger function compares the OLD and NEW values of the state column and raises an exception with a descriptive error message if the transition is not allowed. This enforcement happens at the database level, guaranteeing that no application code can bypass the transition rules regardless of how the UPDATE statement is constructed.

```sql
CREATE OR REPLACE FUNCTION check_orders_status_transition()
RETURNS TRIGGER AS $pgdesign$
BEGIN
  IF OLD.status IS DISTINCT FROM NEW.status THEN
    IF NOT (
      (OLD.status = 'pending' AND NEW.status IN ('confirmed', 'cancelled')) OR
      (OLD.status = 'confirmed' AND NEW.status IN ('shipped', 'suspended')) OR
      (OLD.status = 'suspended' AND NEW.status IN ('confirmed'))
    ) THEN
      RAISE EXCEPTION 'invalid state transition for status: % -> %',
        OLD.status, NEW.status;
    END IF;
  END IF;
  RETURN NEW;
END;
$pgdesign$ LANGUAGE plpgsql;

CREATE TRIGGER trg_orders_status_transition
  BEFORE UPDATE ON orders
  FOR EACH ROW
  EXECUTE FUNCTION check_orders_status_transition();
```

## Validation

### Reachability

pgdesign validates that all declared states are reachable from the initial state. Unreachable states are flagged as warnings -- they would never occur in practice and may indicate a design error.

### Transition Completeness

Every `from` state in named transitions must be a valid state. Every `to` state must be a valid state. The initial state does not need to appear as a `to` target (it is the entry point).

## D2 State Diagrams

State machines generate D2 diagrams showing states as nodes and transitions as directed edges between them. The initial state is visually distinguished with a special style, and unreachable states are marked with a warning indicator if present. These diagrams are included in the D2 output format alongside the entity-relationship diagram and provide a clear visualization of the allowed state flow for each state machine type defined in the schema.

## Codegen

### Transition Methods

For each named transition, codegen produces type-safe methods on the per-table Writer protocol and both PgBackend and InMemoryBackend implementations. Each method is named after the transition, takes the row identifier and any required parameters as arguments, validates the current state, and performs the state change atomically. This means application code calls descriptive methods like `confirm_orders` or `suspend_orders` instead of raw UPDATE statements.

- `cancel_orders(id: UUID) -> None`
- `confirm_orders(id: UUID) -> None`
- `suspend_orders(id: UUID, *, suspended_reason: str) -> None`

PgBackend: uses SELECT ... FOR UPDATE to read the current state, validates the transition in Python, then UPDATE with parameterized SQL.

InMemoryBackend: reads the row from the in-memory store, validates the transition via ConstraintEngine (STATE_MACHINE_TRANSITION constraint kind), then updates the row.

### Constraint Registry

The `_constraints.py` file includes STATE_MACHINE_TRANSITION constraints with the full transition map encoded in the params dictionary, listing every valid from-to pair. The ConstraintEngine uses this transition map for InMemory validation of state changes, ensuring the InMemoryBackend enforces exactly the same transition rules as the database trigger without requiring a live PostgreSQL connection. This enables comprehensive testing of state machine logic in unit tests.

## Diff and Migration

State machine type changes are detected during diff and classified by risk level based on whether they expand or contract the set of valid states and transitions. Adding new states or transitions is a safe expand operation, while removing states or transitions is dangerous because existing rows may contain the removed values or depend on the removed transitions. Migration generates the appropriate DDL for CHECK constraint updates and CREATE OR REPLACE FUNCTION for trigger changes.

- New states added: safe (expand)
- States removed: dangerous (may break existing rows)
- Transitions added: safe (expand)
- Transitions removed: dangerous (may break existing workflows)

Migration generates appropriate ALTER TABLE (for CHECK changes) and CREATE OR REPLACE FUNCTION (for trigger changes).

## Introspect Limitations

State machines introspected from a live database are recovered as enum types with CHECK constraints. The trigger-based transition enforcement is not reverse-engineered into the state machine model -- this would produce phantom diffs. Introspected schemas use the state machine type only when the TOML source declares it.

## Naming Convention

- CHECK constraint: `chk_<table>_<column>_valid`
- Trigger function: `check_<table>_<column>_transition()`
- Trigger: `trg_<table>_<column>_transition`
