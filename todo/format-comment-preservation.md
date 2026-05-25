# Format comment preservation

The formatter (internal/format) explicitly acknowledges in its package doc that comments are lost (v1 limitation). The code parses TOML via internal/parse, then reconstructs the document from scratch using go-toml-edit for writing.

## Current behavior

Comments in the original TOML file are discarded during formatting. The formatter reads via parse.Bytes() (which doesn't preserve comments), then rebuilds the document.

## Desired behavior

Comments should move with their associated key/section when reordered. This requires either:
1. Direct AST node reordering in go-toml-edit (move nodes without re-parsing)
2. A comment attachment pass that maps comments to their associated keys before reordering

## Effort

Medium. Depends on go-toml-edit's AST manipulation capabilities.
