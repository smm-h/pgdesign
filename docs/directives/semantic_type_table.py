"""Custom selfdoc directive: table-semantic-types.

Reads internal/semtype/builtins.go and produces a markdown table of all
built-in semantic types with their PostgreSQL type, nullability, default,
and check constraint.
"""

import os
import re


def _find_project_root():
    """Walk up from this file to find the project root (contains go.mod)."""
    d = os.path.dirname(os.path.abspath(__file__))
    for _ in range(10):
        if os.path.isfile(os.path.join(d, "go.mod")):
            return d
        d = os.path.dirname(d)
    raise RuntimeError("cannot find project root (no go.mod found)")


def _parse_builtins(path):
    """Parse builtins.go and extract type definitions.

    Returns a list of dicts with keys: name, pg_type, not_null, default, check.
    """
    with open(path, encoding="utf-8") as f:
        src = f.read()

    types = []

    # Match each TypeDef block delimited by tab-indented braces.
    # Entries are: \t\t{\n...\t\t},
    block_re = re.compile(
        r"\t\t\{\n(.*?)\t\t\}",
        re.DOTALL,
    )

    for block_m in block_re.finditer(src):
        block = block_m.group(1)

        # Only process blocks that look like TypeDef (have Name field)
        name_m = re.search(r'Name:\s*"([^"]+)"', block)
        if not name_m:
            continue

        entry = {
            "name": name_m.group(1),
            "pg_type": "",
            "not_null": False,
            "default": "",
            "check": "",
        }

        # BaseType: typeinfo.Type{Base: "uuid"}
        base_m = re.search(r'Base:\s*"([^"]+)"', block)
        if base_m:
            entry["pg_type"] = base_m.group(1)

        # NotNull: true/false
        nn_m = re.search(r"NotNull:\s*(true|false)", block)
        if nn_m:
            entry["not_null"] = nn_m.group(1) == "true"

        # DefaultExpr: "gen_random_uuid()"
        dexpr_m = re.search(r'DefaultExpr:\s*"([^"]+)"', block)
        if dexpr_m:
            entry["default"] = dexpr_m.group(1)

        # Default: strPtr("0")
        if not entry["default"]:
            default_m = re.search(r'Default:\s*strPtr\("([^"]+)"\)', block)
            if default_m:
                entry["default"] = default_m.group(1)

        # Check: "VALUE ~ '^[a-z0-9-]+$'"
        check_m = re.search(r'Check:\s*"((?:[^"\\]|\\.)*)"', block)
        if check_m:
            # Unescape Go string escapes
            entry["check"] = check_m.group(1).replace("\\\\", "\\")

        # Identity: "ALWAYS"
        id_m = re.search(r'Identity:\s*"([^"]+)"', block)
        if id_m:
            entry["identity"] = id_m.group(1)

        types.append(entry)

    return types


# Map short PG type names to their canonical display forms
_PG_TYPE_DISPLAY = {
    "int8": "bigint",
    "int4": "integer",
    "int2": "smallint",
    "float4": "real",
    "float8": "double precision",
    "bool": "boolean",
    "timestamptz": "timestamptz",
    "timestamp": "timestamp",
}


def _escape_pipe(s):
    """Escape pipe characters for markdown table cells."""
    return s.replace("|", "\\|")


def resolve(attrs, config, body):
    """Return a markdown table of built-in semantic types."""
    root = _find_project_root()
    builtins_path = os.path.join(root, "internal", "semtype", "builtins.go")

    if not os.path.isfile(builtins_path):
        return "> *[selfdoc: builtins.go not found]*"

    types = _parse_builtins(builtins_path)

    if not types:
        return "> *[selfdoc: no built-in types found in builtins.go]*"

    # Build markdown table
    lines = [
        "| Name | PostgreSQL Type | Not Null | Default | Check |",
        "|------|-----------------|----------|---------|-------|",
    ]

    for t in types:
        name = f"`{t['name']}`"
        raw_pg = t["pg_type"]
        display_pg = _PG_TYPE_DISPLAY.get(raw_pg, raw_pg)
        pg = f"`{display_pg}`" if display_pg else "--"
        nn = "yes" if t["not_null"] else "no"

        default = t.get("default", "")
        identity = t.get("identity", "")
        if identity:
            default = f"GENERATED {identity} AS IDENTITY"
        if default:
            default = f"`{_escape_pipe(default)}`"
        else:
            default = "--"

        check = t.get("check", "")
        if check:
            check = f"`{_escape_pipe(check)}`"
        else:
            check = "--"

        lines.append(f"| {name} | {pg} | {nn} | {default} | {check} |")

    return "\n".join(lines)
