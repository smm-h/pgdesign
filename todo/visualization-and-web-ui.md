# Visualization and Web UI

## Context

pgdesign generates D2 diagrams and SVG renderings of database schemas, but the current implementation is minimal -- 163 lines in `internal/generate/d2.go` that produce `sql_table` shapes with PK/FK constraint annotations, FK edges labeled with ON DELETE actions, and view shapes. No index annotations, no enum shapes, no nullable markers, no table comments, no cardinality notation, no filtering, no customization. The doc output format (`internal/generate/doc.go`) is more comprehensive than D2, covering indexes, checks, policies, RLS, partitioning, enums, and append-only flags -- but it generates Markdown, not visual output.

The serve package (`internal/serve/`) exposes 11 JSON API endpoints (schema introspection, D2/SVG generation, migrations, stats, extensions, validation, diff, audit) but has zero frontend. No HTML, no static files, no web UI of any kind.

This plan covers two related areas: enriching the visualization pipeline (D2 diagrams, filtering, cardinality, heat maps) and building a web UI to present them interactively. The web UI choice is still open and affects how visualization features are delivered.

## Problem Statement

1. **D2 diagrams omit critical schema information.** Indexes, enums, nullable columns, table comments, check constraints, and RLS policies are invisible in diagrams. Users must cross-reference D2 output with doc output or raw TOML to get the full picture.

2. **No filtering or subsetting.** Large schemas produce unreadable diagrams. There is no way to generate a diagram for a subset of tables or to produce a summary-level view.

3. **No relationship cardinality.** FK edges show ON DELETE actions but not cardinality (1:1, 1:N, M:N). This is the most common information people look for in ERDs.

4. **No dependency analysis visualization.** The FK graph is implicit in `Table.FKs` arrays -- there are no graph metrics (fan-in, fan-out), no reverse lookups, and no visual representation of dependency density.

5. **No web UI.** The 11 API endpoints have no consumer. Schema browsing, interactive diagrams, migration history, and stats dashboards all require a frontend.

## Dependencies

- **FK graph (Plan 10):** The dependency heat map feature requires an explicit FK graph data structure with fan-in/fan-out metrics. Plan 10 builds persistent FK graph infrastructure. Heat maps depend on that being available.
- **Doc format in build:** `handleBuild()` in `cmd/pgdesign/handlers_build.go` currently skips doc format with `"format \"doc\" not yet implemented, skipping"`. This should be fixed as part of the build pipeline regardless of this plan, but the web UI will need doc output working.

## Proposed Solution

### Phase 1: D2 Diagram Enrichment

Enrich `GenerateD2()` in `internal/generate/d2.go` to include schema information that the doc format already covers but D2 currently omits.

**Additions to D2 output:**

| Feature | D2 representation | Source in model |
|---------|-------------------|-----------------|
| Index annotations | Columns marked `{constraint: index}` or a note listing index names | `Table.Indexes` |
| Enum shapes | Separate D2 shapes (class or sql_table) listing enum values | `Schema.Enums` (from `model.go`) |
| Nullable markers | `?` suffix or `NULL` annotation on nullable columns | `Column.Nullable` |
| Table comments | D2 `tooltip` or `near` label on table shapes | `Table.Comment` |
| Check constraints | Listed in a note attached to the table shape | `Table.Checks` |
| Unique constraints | Columns marked `{constraint: unique}` | `Table.Uniques` |
| RLS indicator | Visual marker (icon or border style) on RLS-enabled tables | `Table.EnableRLS` |
| Append-only indicator | Visual marker on append-only tables | `Table.AppendOnly` |

**Configurable layers and themes in pgdesign.toml:**

Add a `[output.<name>.d2]` config subsection for D2-specific options:

- `layout`: layout engine (dagre, elk, tala) -- currently hardcoded to dagre in `RenderSVG()`
- `theme`: D2 theme ID (integer)
- `layers`: list of optional annotation layers to include (indexes, enums, comments, checks, rls, append_only) -- all enabled by default, users can disable noisy layers for cleaner diagrams
- `direction`: graph direction (down, right, left, up)

**Affected files:**
- `internal/generate/d2.go` -- major expansion, probably 3-4x current size
- `internal/generate/generate.go` -- pass D2-specific options through `Options` struct
- `internal/config/config.go` -- add D2 config subsection to output config
- `internal/generate/d2_test.go` -- new test cases for each annotation type
- `internal/generate/testdata/` -- new golden files for enriched D2 output

**Effort estimate:** Medium (1-2 weeks)

### Phase 2: D2 Filtering

Add table filtering to diagram generation so users can produce subset diagrams of large schemas.

**Features:**
- **Table filter patterns:** Glob patterns (e.g., `auth_*`, `*_audit`) specified in `[output.<name>.d2]` config or via CLI flag. Only matching tables and their direct FK relationships appear.
- **Include-dependencies mode:** When filtering, optionally include tables that filtered tables reference (transitive FK targets up to a configurable depth). This prevents dangling FK edges.
- **Summary mode:** A stripped-down diagram showing only table names and FK edges -- no columns, no constraints. Useful for architecture overviews of large schemas (50+ tables).
- **Exclude patterns:** Inverse of filter -- exclude specific tables from the diagram (e.g., exclude audit tables from the main ERD).

**Affected files:**
- `internal/generate/d2.go` -- filtering logic before shape generation
- `internal/generate/generate.go` -- add filter/summary options to `Options`
- `internal/config/config.go` -- filter config in D2 subsection
- `cmd/pgdesign/handlers_build.go` -- pass filter options through build pipeline
- `internal/serve/handlers.go` -- add query params to `/api/schema/d2` and `/api/schema/svg` for filtering

**Effort estimate:** Medium (1 week)

### Phase 3: ERD Cardinality Annotations

Add structural cardinality inference and crow's foot notation to D2 FK edges.

**Cardinality inference rules (purely structural, no live data needed):**

| Pattern | Cardinality | Detection |
|---------|-------------|-----------|
| FK column(s) have a UNIQUE constraint or are the PK | 1:1 | Check if FK columns appear in `Table.Uniques` or match `Table.PK` |
| Standard FK with no unique constraint on FK columns | 1:N | Default case |
| Table with exactly 2 FKs, both part of the PK, no other non-FK columns (or only metadata columns) | M:N junction | Heuristic: PK is composite of both FK column sets |

**D2 representation:**

D2 supports edge labels and source/target arrowheads. Crow's foot notation requires custom arrowhead shapes or label conventions. Options:
- Use D2's `source-arrowhead` and `target-arrowhead` with label text (`1`, `*`, `0..1`, `1..*`)
- Use edge labels with standard notation (`1--*`, `1--1`, `*--*`)

The exact D2 syntax for crow's foot will need prototyping -- D2's native arrowhead customization may be sufficient, or we may need to use label-based fallback.

**Affected files:**
- `internal/generate/d2.go` -- cardinality inference + edge annotation
- `internal/model/model.go` -- possibly add helper methods for cardinality detection (e.g., `FK.IsOneToOne(table Table) bool`)
- `internal/generate/d2_test.go` -- test cases for each cardinality pattern
- `internal/generate/testdata/` -- golden files with cardinality annotations

**Effort estimate:** Medium (1 week)

### Phase 4: Dependency Heat Maps

Color-code D2 table nodes by dependency density (fan-in/fan-out) using the persistent FK graph from Plan 10.

**Requirements:**
- Compute fan-in (number of tables that reference this table) and fan-out (number of tables this table references) from the FK graph
- Map metrics to a color gradient (e.g., green for low-dependency tables, yellow for moderate, red for high-dependency hubs)
- Configurable metric: fan-in, fan-out, or combined (fan-in + fan-out)
- Configurable thresholds in `[output.<name>.d2]`

**Dependency on Plan 10:** This phase requires the FK graph infrastructure. The graph must provide:
- Reverse FK lookup (given a table, which tables reference it)
- Fan-in/fan-out counts
- These should be methods on the graph type, not recomputed in the generate package

If Plan 10 is not yet complete, the fan-in/fan-out can be computed locally from `Table.FKs` arrays as a stopgap, but the proper solution is to use the shared graph.

**Affected files:**
- `internal/generate/d2.go` -- apply D2 `style.fill` colors based on metrics
- `internal/config/config.go` -- heat map config (metric, thresholds, color palette)
- Whatever package Plan 10 creates for the FK graph -- consume its API

**Effort estimate:** Small-medium (3-5 days), assuming FK graph from Plan 10 is available

### Phase 5: Live Cardinality Annotations

Add a `--live` flag to diagram generation that queries `pg_stat_user_tables` for actual row counts and `pg_stats` for `n_distinct` to annotate edges with real cardinality ratios.

**Data sources:**
- `pg_stat_user_tables.n_live_tup` -- approximate row count per table
- `pg_stats.n_distinct` -- for FK columns, indicates how many distinct values exist (negative values are fractions of total rows)
- Combine to produce annotations like `users 1--47,832 orders` (one user has ~47,832 orders on average)

**Integration with serve package:** The `/api/stats` and `/api/stats/{table}` endpoints already query similar stats. Reuse the stats-gathering code rather than duplicating it.

**Affected files:**
- `internal/generate/d2.go` -- accept live stats and annotate edges
- `internal/generate/generate.go` -- add live-stats option, accept stats data
- `cmd/pgdesign/handlers_build.go` -- pass DB connection for live stats when `--live` is specified
- `internal/serve/handlers.go` -- enhance D2/SVG endpoints with optional live stats

**Effort estimate:** Medium (1 week)

### Phase 6: Web UI

The web UI decision is deferred. Three options remain open, each with different trade-offs:

**Option 1: Embedded minimal HTML (Go templates + //go:embed)**

- Pros:
  - Zero JS build pipeline
  - Ships as part of the single Go binary -- no separate deploy
  - Uses existing 11 API endpoints directly
  - Consistent with pgdesign's zero-external-dependency philosophy (D2 is native Go, no Makefile, no build scripts)
  - Fastest to implement
- Cons:
  - Limited interactivity -- page reloads for navigation, no client-side filtering
  - SVG diagrams are static images, not interactive (no pan/zoom/click-to-filter)
  - Heat map and filtering require server round-trips
- Effort: Small-medium (1-2 weeks)

**Option 2: Static site generation (pgdesign build outputs HTML)**

- Pros:
  - No runtime server needed -- deployable to GitHub Pages, Netlify, etc.
  - Can include pre-rendered SVGs, data dictionary, migration history
  - Fits the `build` command pattern (TOML in, artifacts out)
  - Could use lightweight JS for client-side interactivity (pan/zoom on SVGs)
- Cons:
  - No live data (stats, live cardinality, audit) -- those require a database connection
  - Stale the moment the schema changes -- must rebuild
  - Duplicates some doc format functionality
- Effort: Medium (2-3 weeks)

**Option 3: Full SPA (React/Svelte/Vue)**

- Pros:
  - Richest interactivity -- client-side filtering, interactive SVG with pan/zoom/click, live updates
  - Heat maps with hover tooltips, filterable ERDs, real-time stats dashboards
  - Best UX for large schemas
- Cons:
  - Adds npm dependency and JS build pipeline -- contradicts the "direct Go commands only" philosophy
  - Distribution: either bundle built assets via //go:embed (build step needed) or ship separately
  - Largest implementation effort
  - Maintenance burden of two ecosystems (Go + JS)
- Effort: Large (4-6 weeks)

**The web UI choice affects visualization delivery.** Interactive diagrams (pan/zoom/click-to-filter), filterable ERDs with live updates, and dependency heat maps with hover details are substantially richer in an SPA but achievable in all three approaches:
- Embedded HTML: static SVGs with basic pan/zoom via a small inline script, server-side filtering via query params
- Static site: pre-rendered SVGs with optional JS enhancement, filtering via generated subset pages
- SPA: fully interactive D2/SVG rendering with client-side filtering, live stats streaming, and dynamic heat maps

### Phase 7: Documentation

This plan is responsible for documenting its own features. Documentation deliverables:

- **Visualization features:** D2 enrichment options, filtering syntax, cardinality notation explanation, heat map configuration, live cardinality usage
- **Web UI:** User guide for whatever option is chosen -- how to start/deploy, navigate, interact
- **Serve API endpoints:** The 11 existing endpoints are undocumented. Document request/response schemas, query parameters, error codes. This documentation is needed regardless of web UI choice since the API is a public interface.
- **pgdesign.toml reference:** New D2 config subsections, heat map thresholds, filter patterns

The doc format in `internal/generate/doc.go` generates schema documentation (data dictionaries). This phase is about user-facing documentation of pgdesign's own features, which is separate -- likely selfdoc templates or manual docs depending on project convention.

**Affected files:**
- `docs/` templates (if selfdoc is used)
- Inline help text in strictcli command/flag descriptions
- API endpoint documentation (format TBD -- could be OpenAPI spec, could be doc page)

**Effort estimate:** Medium (1-2 weeks)

## Open Design Decisions

1. **Web UI approach:** Option 1 (embedded HTML), Option 2 (static site), or Option 3 (SPA). This is the biggest decision and affects the architecture of everything downstream. To be decided when this work starts.

2. **D2 layer mechanism:** D2 supports layers natively (overlay/underlay). Should configurable layers use D2's layer feature (produces multi-layer SVGs) or should they be implemented as conditional generation (include/exclude sections of D2 source)? D2 layers add complexity but enable toggling in viewers that support it.

3. **Crow's foot notation in D2:** D2's arrowhead customization may not support true crow's foot shapes. Need to prototype whether `source-arrowhead`/`target-arrowhead` with labels is sufficient or whether a different approach is needed (e.g., custom SVG markers post-processing).

4. **Heat map color palette:** Should use a colorblind-safe palette. Need to decide on the specific gradient and whether to support user-defined palettes in config.

5. **Summary mode scope:** Should summary mode show only tables + FK edges, or should it also show enums and views as simplified shapes? For very large schemas, even table-name-only diagrams may be too large -- consider clustering by schema namespace.

6. **Live cardinality formatting:** How to display ratios on edges -- exact counts (`1--47,832`), approximations (`1--~48K`), or percentages (`1--many`)? Configurable or opinionated default?

## Effort Summary

| Phase | Effort | Dependencies |
|-------|--------|--------------|
| Phase 1: D2 enrichment | Medium (1-2 weeks) | None |
| Phase 2: D2 filtering | Medium (1 week) | Phase 1 (uses enriched D2) |
| Phase 3: ERD cardinality | Medium (1 week) | Phase 1 (annotation infrastructure) |
| Phase 4: Dependency heat maps | Small-medium (3-5 days) | Plan 10 (FK graph) |
| Phase 5: Live cardinality | Medium (1 week) | Phase 3 (structural cardinality first) |
| Phase 6: Web UI | Small to large (1-6 weeks) | Phases 1-5 (visualization features to display) |
| Phase 7: Documentation | Medium (1-2 weeks) | All other phases |
| **Total** | **Large (8-16 weeks depending on web UI choice)** | |

Phases 1-3 can proceed independently of the web UI decision. Phase 4 depends on Plan 10. Phase 5 depends on Phase 3. Phase 6 is the variable -- the web UI choice determines whether this is a 2-month or 4-month plan. Phase 7 runs incrementally alongside other phases but has a final pass at the end.
