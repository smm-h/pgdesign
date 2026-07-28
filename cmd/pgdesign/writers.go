package main

// Writer taxonomy (roadmap 6.2, L6 — total provenance).
//
// The revision invariant is DERIVED, not legislated: after any write, all
// regenerable planner-set artifacts carry the ONE full-project revision. Every
// pgdesign command that writes to disk is classified into exactly one of SIX
// writer classes below. The classification is TOTAL — the writersRegistry table
// names every CLI command (writers and non-writers alike), and a test
// (writers_test.go) asserts the table and the live strictcli command set are
// identical. A newly-added command that is absent from the table turns the test
// red, forcing its author to declare its writer class. This is the taxonomy's
// pre-commitment made mechanical: the "future file-writing generate mode is
// full-or-banned" rule only holds if the initial enumeration is actually total.
//
// The six classes:
//
//   - FULL REGENERATOR (build, revise): regenerates EVERY configured [output] in
//     one pass, so the whole planner set lands at one revision. Always allowed —
//     it is the operation that RESTORES the invariant.
//
//   - PARTIAL WRITER (codegen --output): writes ONE artifact. Exactly one exists.
//     It REFUSES when any non-rewritten [output] sibling is at a different
//     revision (partialWriteStaleSiblings), because writing one output while
//     siblings lag would create a mixed-revision tree. The taxonomy PRE-COMMITS
//     that any future file-writing generate mode registers here as full-or-banned.
//
//   - SOURCE-EDITING WRITER (fmt): rewrites a SCHEMA SOURCE file, not a derived
//     artifact. It is OUTSIDE the invariant (its output is not planner-set) but
//     it CHANGES the revision (reformatting can reorder columns, which is
//     identity-bearing). It prints a follow-up notice (fmtRevisionNotice) and the
//     revision check catches the resulting staleness.
//
//   - SCAFFOLDING WRITER (testdb init — language wrappers + CI workflow;
//     introspect --output — a NEW candidate source file at an arbitrary path):
//     writes files that are NOT planner-set derived artifacts and NOT schema
//     source under the invariant. Never flagged. introspect --output prints the
//     adopting note (introspectAdoptionNote): adopting its output as project
//     source is a source edit that changes the revision.
//
//   - STAMPED-UNENFORCED (seed): stamps honest provenance onto its output, but
//     the content depends on --seed/--counts/--mode and is NEVER freshness- or
//     revision-checked. Stamped, unenforced.
//
//   - APPEND-ONLY STORE (migrate generate/squash/rebase/upgrade/baseline): writes
//     the content-addressed migration chain + object store + revision manifests.
//     Append-only and immutable; covered by the store<->chain consistency checker
//     (migrate.VerifyChainConsistency, invoked from the revision check), NOT by
//     the full-project-stamp agreement rule.
//
// Everything else (generate/diff/serve/stats/check, migrate plan/apply/rollback/
// status/test, testdb setup/teardown/gc) writes nothing to the local filesystem
// (stdout, or the target database) and is classed NON-WRITER.

// WriterClass is the provenance-enforcement class of a CLI command. See the
// package-level doc above for the semantics of each class.
type WriterClass string

const (
	// ClassFullRegenerator regenerates every configured output in one pass.
	ClassFullRegenerator WriterClass = "full-regenerator"
	// ClassPartialWriter writes one artifact and refuses on stale siblings.
	ClassPartialWriter WriterClass = "partial-writer"
	// ClassSourceEditing rewrites a schema source file (changes the revision).
	ClassSourceEditing WriterClass = "source-editing"
	// ClassScaffolding writes non-invariant files (wrappers, candidate sources).
	ClassScaffolding WriterClass = "scaffolding"
	// ClassStampedUnenforced stamps provenance but is never revision-checked.
	ClassStampedUnenforced WriterClass = "stamped-unenforced"
	// ClassAppendOnlyStore writes the immutable migration chain/store.
	ClassAppendOnlyStore WriterClass = "append-only-store"
	// ClassNonWriter writes nothing to the local filesystem.
	ClassNonWriter WriterClass = "non-writer"
)

// writersRegistry classifies EVERY CLI command path (dot-joined for grouped
// subcommands, e.g. "migrate.generate") into its writer class. The set of keys
// must equal the live strictcli command set exactly — writers_test.go enforces
// this. When adding a command, add its entry here.
var writersRegistry = map[string]WriterClass{
	// FULL regenerators.
	"build":  ClassFullRegenerator,
	"revise": ClassFullRegenerator,

	// PARTIAL writer (exactly one).
	"codegen": ClassPartialWriter,

	// SOURCE-EDITING writer.
	"fmt": ClassSourceEditing,

	// SCAFFOLDING writers.
	"introspect":  ClassScaffolding,
	"testdb.init": ClassScaffolding,

	// STAMPED-UNENFORCED.
	"seed": ClassStampedUnenforced,

	// APPEND-ONLY store writers.
	"migrate.generate": ClassAppendOnlyStore,
	"migrate.squash":   ClassAppendOnlyStore,
	"migrate.rebase":   ClassAppendOnlyStore,
	"migrate.upgrade":  ClassAppendOnlyStore,
	"migrate.baseline": ClassAppendOnlyStore,

	// NON-WRITERS (stdout / database only).
	"generate":         ClassNonWriter,
	"diff":             ClassNonWriter,
	"serve":            ClassNonWriter,
	"stats":            ClassNonWriter,
	"check":            ClassNonWriter,
	"migrate.plan":     ClassNonWriter,
	"migrate.apply":    ClassNonWriter,
	"migrate.rollback": ClassNonWriter,
	"migrate.status":   ClassNonWriter,
	"migrate.test":     ClassNonWriter,
	"testdb.setup":     ClassNonWriter,
	"testdb.teardown":  ClassNonWriter,
	"testdb.gc":        ClassNonWriter,
}

// fmtRevisionNotice is the follow-up printed after fmt rewrites a source file.
// A source edit changes the model, hence the full-project revision, hence every
// derived output is now stale — the revision check will flag it.
const fmtRevisionNotice = "note: reformatting edited the schema source, so the project revision changed; run `pgdesign build` to regenerate outputs (otherwise `pgdesign check --tag revision` will report them stale)"

// introspectAdoptionNote is printed after `introspect --output` writes a
// candidate source file. Introspect output is scaffolding, outside the invariant;
// but adopting it AS project source is a source edit that changes the revision.
const introspectAdoptionNote = "note: this is a candidate schema source, not a derived artifact; adopting it as a project source changes the project revision — run `pgdesign build` afterward to regenerate outputs"
