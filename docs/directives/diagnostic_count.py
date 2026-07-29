"""Custom selfdoc directive: diagnostic-count.

Scans Go source for diagnostic code definitions and produces a summary
sentence like "pgdesign enforces N diagnostic rules across K categories."
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


# Matches diagnostic code string literals like "E001", "W102", "S003"
_CODE_RE = re.compile(r'"([EWISCA]\d{3})"')

# Category display names
_CATEGORY_NAMES = {
    "E": "errors",
    "W": "warnings",
    "I": "info",
    "S": "seed",
    "C": "codegen",
    "A": "audit",
}


def _scan_codes(root):
    """Scan non-test Go source files for unique diagnostic codes."""
    codes = set()
    for dirpath, _dirnames, filenames in os.walk(root):
        # Skip vendor, testdata, hidden dirs
        base = os.path.basename(dirpath)
        if base.startswith(".") or base in ("vendor", "testdata"):
            continue
        for fname in filenames:
            if not fname.endswith(".go"):
                continue
            if fname.endswith("_test.go"):
                continue
            fpath = os.path.join(dirpath, fname)
            with open(fpath, encoding="utf-8", errors="replace") as f:
                for line in f:
                    for m in _CODE_RE.finditer(line):
                        codes.add(m.group(1))
    return codes


def _filter_reserved(codes, root):
    """Remove reserved codes by checking codes.go for '(reserved)' markers."""
    codes_go = os.path.join(root, "internal", "diagnostic", "codes.go")
    reserved = set()
    if os.path.isfile(codes_go):
        reserved_re = re.compile(r"^//\s+([EWISCA]\d{3})\s+\(reserved")
        with open(codes_go, encoding="utf-8") as f:
            for line in f:
                m = reserved_re.match(line)
                if m:
                    reserved.add(m.group(1))
    return codes - reserved


def resolve(attrs, config, body):
    """Return a markdown summary of diagnostic code counts."""
    root = _find_project_root()
    codes = _scan_codes(root)
    codes = _filter_reserved(codes, root)

    # Group by category letter
    by_cat = {}
    for code in codes:
        cat = code[0]
        by_cat.setdefault(cat, set()).add(code)

    total = len(codes)

    # Build category breakdown in canonical order
    parts = []
    for letter in "EWISCA":
        if letter in by_cat:
            count = len(by_cat[letter])
            label = _CATEGORY_NAMES.get(letter, letter)
            parts.append(f"{letter}: {count} {label}")

    breakdown = ", ".join(parts)
    cat_count = len(by_cat)

    return (
        f"pgdesign enforces **{total}** diagnostic rules "
        f"across {cat_count} categories ({breakdown})."
    )
