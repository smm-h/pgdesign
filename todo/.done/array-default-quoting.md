# Array defaults have the same triple-quoting bug that enum defaults had

The 0.7.0 enum default fix (breaking change to raw values + E109 validation) treated the symptom on enums without fixing the underlying DDL generator bug. The generator still double-quotes values that already contain SQL quotes, and this surfaces immediately on array column defaults.

## Reproduction

```toml
[tables.inbox_items.columns.tags]
type = "str"
array = true
default = "'{}'"
```

Generated: `DEFAULT '''{}'''`
Expected: `DEFAULT '{}'`

The TOML value `"'{}'"` contains the SQL quotes the user intends. The generator wraps them in another layer, producing a string literal containing `'{}'` rather than an empty array.

## Root cause

Same as the original enum default bug: the DDL generator adds SQL quoting unconditionally, without checking whether the value is already quoted. The enum fix avoided this by requiring raw values and auto-adding quotes during generation. The array path doesn't have equivalent logic.

## Workaround

Omit the default and handle it in application code. But this means the DDL doesn't express the full schema contract.
