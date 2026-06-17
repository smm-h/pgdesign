# loadProjectConfig should walk up directories

## Bug

`loadProjectConfig()` only looks for `pgdesign.toml` in the directory containing the schema files. If the config file lives in a parent directory (e.g., the project root), it is not found.

## Current workaround

Users create a symlink from the schema directory to the actual config file in a parent directory (e.g., `schema/pgdesign.toml -> ../pgdesign.toml`). This is fragile and surprising.

## Proposed fix

Walk up from the schema directory toward the filesystem root, checking each ancestor for `pgdesign.toml`, and stop at the first match. This is the standard pattern used by many tools to locate their config files (similar to how git finds `.git/`, npm finds `package.json`, etc.).

## Affected files

- Wherever `loadProjectConfig` is defined (likely the config/project loading module)
