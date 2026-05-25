# End-to-end partition support

Partition support is partially implemented across the codebase but has gaps in introspection, diffing, and generation.

## Gaps by package

### internal/introspect
- No query for pg_partitioned_table (partition strategy and key)
- No query for pg_inherits (parent-child relationships)
- relkind='p' is selected but PartitionSpec is never populated

### internal/diff
- PartitioningChanged field is missing from TableDiff struct
- No comparison of partition strategies or partition lists

### internal/generate
- CREATE TABLE ... PARTITION OF (step 5 in emission order) is not implemented
- pg_partman integration (create_parent, part_config) is not implemented

### internal/extregistry
- pg_partman functions registered without schema prefix (plain create_parent vs partman.create_parent)

## Effort

Medium. The model already has PartitionSpec, and parse already handles partitioning in TOML. The gaps are in reading partitions from live DBs, diffing them, and generating the DDL.
