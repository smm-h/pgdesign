# Additive-only registry enforcement (check tag + breaking-change diagnostics)

## Context

Consumers increasingly treat pgdesign schemas as the single source of truth for
name registries that downstream code depends on: enum values, semantic type
names, state-machine state/transition names, and (if the wire-truth direction
lands — see the separate wire-truth todo) transport-type registries. For such
consumers, *removal or rename* of a declared name between releases is a
breaking event that should be impossible to ship accidentally.

Today pgdesign computes the signal but does not act on it:

- `diff/diff.go` `diffEnum` (~lines 1486-1508) computes `ValuesAdded`,
  `ValuesRemoved`, `Reordered`, plus position-aware classification. Removal IS
  detected.
- `migrate/generate.go` (~lines 75-89) turns only `ValuesAdded` into ops
  (`alter_enum_add_value`). `ValuesRemoved` produces no migration op and no
  breaking-change diagnostic — it is computed and then silently ignored.
  (`migrate/selfcontained_inventory.go:40` already documents that enum values
  are not droppable in Postgres.)
- The diff command already reconstructs a prior schema from a git ref:
  `cmd/pgdesign/handlers_diff.go` shells out to `git rev-parse
  --show-toplevel` (~line 310) and `git show <ref>:<path>` (~line 320). The
  "compare current model against a historical model" plumbing exists.
- The check framework is cheap to extend: tags register in
  `cmd/pgdesign/cli.go` (~lines 52-58) via strictcli
  (`RegisterErrorCheck`/`RegisterWarnCheck`), implementations live in
  `cmd/pgdesign/checks.go`, and a check receives a `ProjectRoot()` and builds
  the model itself.
- The content-addressed revision kernel (`internal/enc`, `internal/objstore`,
  `internal/rev`, `internal/chain`) gives schema states cryptographic identity
  and parent-linked history — the natural long-term home for "properties over
  pairs of released states."

## Problem

There is no way to declare "this schema's name sets may only ever grow across
releases" and have the toolchain enforce it. A merchant of generated code can
delete an enum value or rename a type, every check passes, the release ships,
and every downstream consumer of the generated identifiers breaks at compile
time (best case) or at runtime (worst case, for stringly consumers). The
guardrail must be a hard error at check/release time, not review discipline.

## Proposed solutions

### A. New check tag `additivity` comparing against the last version tag (recommended)

`pgdesign check --tag additivity`:

1. Resolve the last release tag (`git describe --tags --abbrev=0 --match
   <glob>`; glob configurable for monorepo tag schemes, default `v*`).
2. Reconstruct the schema at that tag via the existing `git show` path
   (extract the git helpers out of `cmd/pgdesign/handlers_diff.go` into a
   reusable internal package rather than duplicating them).
3. Build both models; compare configured name sets; hard-error on any name
   present at the tag and absent now. A rename is a removal plus an addition
   and therefore errors — that is correct and should be documented as such.
4. No tag found (first release) = pass.

Scope of name sets, v1: enum names + enum values, semantic type names,
state-machine state names and named transitions. Optional (config): table
names, column names per table (this is a stricter posture many consumers will
not want — must be opt-in).

Pros: rides existing diff output and git plumbing; small; immediately
wireable into release gates (the ecosystem release tooling runs project
checks as subprocess gates, non-zero exit aborts the release). Cons: adds a
second "reconstruct from git" call site if the helpers are not extracted
first; policy/config surface needs design (see open questions).

### B. Revision-kernel-native enforcement

Record released schema states as `rev` Revisions (anchored at release time),
and enforce additivity as a law over `chain` edges: an edge whose child
removes a registry name from its parent is invalid.

Pros: architecture-coherent; content-addressed instead of tag-string-based;
generalizes to future model classes (e.g. a wire/contract registry) for free;
the enforcement becomes a property of history rather than a check that must
remember to run. Cons: depends on the kernel's completion state (the packages
reference multi-phase roadmap comments — verify how finished `rev`/`chain`
integration actually is before betting on this); heavier to land; release
anchoring needs a home in the release flow.

### C. Consumer-side scripts

Each consumer writes its own compare-against-last-tag script. Rejected as the
product answer: duplicated, drift-prone, and the semantics (which sets are
registries) belong to the schema tool.

### Recommendation

A now, with its comparison logic written against model-level sets (not diff
render output) so that B can absorb it later as a chain law without rewriting
the semantics. Independently of A vs B: promote `EnumDiff.ValuesRemoved` (and
analogous removals) to explicit breaking-change diagnostics in normal diff
output — silence there is wrong even for consumers who never enable the
additivity check.

## Open questions

- Kernel readiness: how complete are `rev`/`chain` in practice; is a released
  Revision currently recorded anywhere at all?
- Config surface: opt-in per project (a key in the project config) vs
  default-on for enums/types with opt-out? Given the ecosystem preference for
  hard guardrails, default-on for the v1 sets is defensible; decide
  deliberately.
- Monorepo tag globs: the tag scheme must be configurable and documented; a
  wrong glob silently compares against the wrong baseline.
- Should the check also run inside `pgdesign diff` as annotations (breaking
  badges) even when the check tag is not enabled? (Probably yes — see
  recommendation.)
- Interaction with epochs/major versions: a deliberate breaking release needs
  an explicit, audited escape (e.g. an allowlist file naming the removed
  identifiers for one release), NOT a bypass flag. Design the escape as
  data-reviewed-in-repo, consistent with the no-escape-hatches philosophy.

## Affected files

- `cmd/pgdesign/cli.go` (register tag), `cmd/pgdesign/checks.go` (check impl)
- `cmd/pgdesign/handlers_diff.go` → extract git-ref schema reconstruction into
  an internal package both call sites use
- `diff/diff.go` + diagnostics: breaking-change classification for removals
- docs: new check tag, the allowlist escape format, monorepo tag-glob config
- tests: red-green — a fixture repo with a tag, a removal, and an assertion
  that the check fails; plus first-release pass; plus allowlist pass

## Effort

Small-to-medium. Option A core is roughly: helper extraction + one check
function + set comparison + config keys + tests/docs. The breaking-diagnostic
promotion is small and independent. Option B is a separate, larger follow-up
gated on kernel verification.
