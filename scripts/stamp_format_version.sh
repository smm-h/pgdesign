#!/usr/bin/env bash
#
# stamp_format_version.sh — bootstrap remediation for the strictspec shape gate.
#
# pgdesign schema documents must now carry a top-level `format_version = 1` (the
# strictspec document version gate; see internal/parse/pgschema/). This script
# stamps that key onto schema TOML documents that lack it. It is idempotent: a
# document that already declares format_version is left untouched.
#
# Usage:
#   scripts/stamp_format_version.sh <file-or-dir>...
#
# For a directory argument, every *.toml that looks like a pgdesign schema
# (contains a [meta], [tables.*], [types.*], [views.*], [functions.*], or
# [sequences.*] header) is stamped; pgdesign.toml (project config) is skipped.
set -euo pipefail

VERSION=1

is_schema() {
  grep -qE '^\[(meta\]|tables\.|types\.|views\.|materialized_views\.|sequences\.|functions\.|groups\])' "$1"
}

stamp_file() {
  local f="$1"
  if grep -qE '^[[:space:]]*format_version[[:space:]]*=' "$f"; then
    return 0
  fi
  local tmp
  tmp="$(mktemp)"
  printf 'format_version = %s\n' "$VERSION" >"$tmp"
  cat "$f" >>"$tmp"
  mv "$tmp" "$f"
  echo "stamped $f"
}

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <file-or-dir>..." >&2
  exit 2
fi

for arg in "$@"; do
  if [ -d "$arg" ]; then
    while IFS= read -r -d '' f; do
      base="$(basename "$f")"
      [ "$base" = "pgdesign.toml" ] && continue
      is_schema "$f" && stamp_file "$f"
    done < <(find "$arg" -type f -name '*.toml' -print0)
  elif [ -f "$arg" ]; then
    stamp_file "$arg"
  else
    echo "skip (not found): $arg" >&2
  fi
done
