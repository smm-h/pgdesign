# Zig Constraint Regex Support

## Context

The `constraints` codegen mode generates client-side validation from CHECK/NOT NULL/enum constraints. Go and TypeScript implementations use regex for LIKE/SIMILAR TO pattern matching. Zig has no stdlib regex, so the Zig constraint generator (not yet implemented) needs a strategy for pattern-based constraints.

## Problem

CHECK constraints can contain `LIKE` and `SIMILAR TO` patterns (e.g., `value LIKE '^[A-Z]{2,3}$'`). The constraint codegen extracts these patterns and emits regex-based validators. Zig's standard library (`std`) has no regex support, so emitting regex calls is not an option.

## Options

### 1. Hand-written char-class validators using std.mem

Zero external dependencies. For each CHECK pattern, emit inline Zig validation logic using `std.mem` functions (`startsWith`, `endsWith`, `indexOf`, `len`). Char-class checks like `[A-Z]` become explicit range comparisons.

**Pros:**
- Zero-dep, compiles everywhere Zig compiles
- Covers the most common CHECK patterns (length bounds, char-class ranges, prefix/suffix)
- Predictable performance, no allocation

**Cons:**
- Can only cover a subset of regex features (no alternation, no quantifiers beyond fixed repetition)
- Codegen logic becomes complex for non-trivial patterns
- Each new pattern class requires new codegen support

### 2. mvzr library (github.com/tiehuis/zig-regex successor)

[mvzr](https://github.com/mnemnion/mvzr) is a zero-allocation, comptime-capable regex engine built entirely on `std`. ~73 stars. Compiles patterns at comptime so there is no runtime overhead for fixed patterns.

**Pros:**
- Full regex support for emitted patterns
- Comptime compilation means zero runtime cost for constant patterns (which all CHECK patterns are)
- std-only, no libc or system dependencies

**Cons:**
- External dependency (~73 stars, single maintainer)
- Adds a `build.zig.zon` dependency that Zig consumers must fetch
- If the library breaks or is abandoned, constraint codegen breaks

### 3. Skip LIKE patterns entirely with a comment

Emit a `// TODO: pattern validation requires regex — not available in Zig` comment in the generated code. Generate all other constraint checks (NOT NULL, enum membership, length, range) and only skip pattern-based ones.

**Pros:**
- No dependency, no complex codegen
- Other constraint types still work
- Honest about the limitation

**Cons:**
- Pattern-based constraints are silently unenforced
- Users may not notice the TODO comment
- Zig output is less capable than Go/TS output

## Recommendation

Option 1 (hand-written char-class validators) is the most correct for a code generation tool. The patterns that appear in CHECK constraints are overwhelmingly simple (char ranges, length bounds, prefix/suffix) because they must be valid PostgreSQL expressions. A focused pattern-to-Zig compiler covering the 5-6 common forms would handle >90% of real schemas. Fall back to Option 3 (skip with comment) for any pattern the compiler cannot handle, so there is no silent degradation.

## Effort

Medium. Requires implementing the Zig constraint generator first (similar scope to Go/TS constraint generators), then adding the pattern-to-validator compiler on top.
