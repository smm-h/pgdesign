---
title: "State Machines"
description: "How pgdesign defines state machine types with enforced transitions via database triggers, D2 state diagrams, diff and migration support, and codegen transition maps."
---

# State Machines

pgdesign supports state machine types that enforce valid state transitions at the database level via triggers, generate D2 state diagrams, and produce transition methods in codegen output.

## TOML Syntax

State machines are defined in the `[types.*]` section with `kind = "state_machine"`:

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

Named transitions provide human-readable names for state changes and support additional parameters:

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

A CHECK constraint ensures the column value is always a valid state:

```sql
ALTER TABLE orders
  ADD CONSTRAINT chk_orders_status_valid
  CHECK (status IN ('pending', 'confirmed', 'shipped', 'cancelled', 'suspended'));
```

### Trigger Enforcement

A BEFORE UPDATE trigger validates state transitions:

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

State machines generate D2 diagrams showing states as nodes and transitions as directed edges. The initial state is visually distinguished. Unreachable states (if any) are marked.

## Codegen

### Transition Methods

For each named transition, codegen produces methods on the per-table Writer protocol and both backends:

- `cancel_orders(id: UUID) -> None`
- `confirm_orders(id: UUID) -> None`
- `suspend_orders(id: UUID, *, suspended_reason: str) -> None`

PgBackend: uses SELECT ... FOR UPDATE to read the current state, validates the transition in Python, then UPDATE with parameterized SQL.

InMemoryBackend: reads the row from the in-memory store, validates the transition via ConstraintEngine (STATE_MACHINE_TRANSITION constraint kind), then updates the row.

### Constraint Registry

The `_constraints.py` file includes STATE_MACHINE_TRANSITION constraints with the full transition map in the params dict. The ConstraintEngine uses this for InMemory validation of state changes.

## Diff and Migration

State machine type changes are detected during diff:

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
