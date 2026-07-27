package migrate

// Post-apply reconcile (roadmap 5.8) — L5's CODOMAIN CHECK made unconditional.
//
// After ApplyChain lands the head, ReconcileAfterApply proves the world actually
// arrived at the target model: introspect the live database (managed objects are
// already excluded by introspect's 0.4 filters), reconstruct the TARGET model from
// the head revision manifest, and N-normalized DiffLive the two — the live
// round-trip normalizer resolves the ≈_pg cast residue on the introspected side.
// A residual mismatch is a HARD ERROR listing every divergent object. Drift is
// loud (L5); it is never absorbed. This runs while ApplyChain still holds the
// session advisory lock, so no concurrent migration can interleave.
//
// FOLD vs full-model path (design note, spec 5.8): DiffLive IS the shared
// comparison engine, and it already HAS the shape 5.8 prescribes — a fold of a
// per-object comparator plus an orphan check. diffTables/diffEnums/… match objects
// by key and compare each matched pair (the per-object comparator), and unmatched
// keys fall out as added/removed (the orphan check). Its expression comparisons
// route through the same N/livenorm ≈_syn/≈_pg engine the apply-loop predicate
// comparator (5.7) and the conformance matrix use, so there is ONE comparison unit,
// not a second whole-model path. This is the pragmatic reading the roadmap invites:
// the predicate comparator is the per-OP apply-time check; reconcile is the
// whole-model codomain check, and DiffLive is its already-a-fold engine. Reusing it
// verbatim (as upgrade's TOML<->DB reconcile already does) keeps one engine.
//
// SCHEMA-GLOBAL fields introspection cannot observe are aligned, never reported as
// drift: [groups] are pgdesign config (not database objects), and the live server's
// PG version is ground truth. Reconcile does NOT auto-add imported schemas: it
// introspects only the schemas the target model spans, so a foreign schema is out
// of scope.
//
// COMMENTS ARE NOT CERTIFIED (documented limitation, flagged 5.8): the migrate
// generator does NOT emit COMMENT ON — only internal/generate (the full-DDL path)
// does — so a chain-applied database never carries table/column/object comments.
// Comments are documentation metadata, not schema structure; reconcile certifies
// structure, so it strips comments from both sides before comparison rather than
// false-erroring on every commented model (which would be every real schema). If
// migrate gains comment emission, this alignment should be removed so reconcile
// certifies comments too. The same holds for a policy's PUBLIC role, which PG
// stores as the empty role set (a `to = "public"` model spelling would otherwise
// false-drift) — but that lives in the differ/introspect layer, not here.
//
// SM-vs-ENUM LOSSINESS (documented, L10 injective caveat): a state_machine type
// introspects as a plain enum plus a CHECK and a trigger — the SM *kind* is not
// reconstructable from pg_catalog. DiffLive compares the enum labels (the states)
// and the transition-enforcing CHECK, which converge, so a correctly-applied SM
// type reconciles clean on its enum/constraint projection. Reconcile therefore
// certifies that projection, NOT the SM kind. The L10 property test's manifest
// oracle (not the re-introspection oracle) is what covers the SM kind.

import (
	"context"
	"fmt"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/livenorm"
	"github.com/smm-h/pgdesign/internal/model"
)

// ReconcileAfterApply is the always-on codomain check. It is a no-op at a genesis
// head (nothing was ever applied). A nil return means the applied database matches
// the target model exactly on every introspectable object.
func ReconcileAfterApply(ctx context.Context, dbURL string, p *ChainProject) error {
	head, target, err := ChainHead(p)
	if err != nil {
		return fmt.Errorf("reconcile: resolve head: %w", err)
	}
	if target == nil {
		return nil // genesis head: no objects to reconcile
	}

	schemaNames := reconcileSchemaNames(target)
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		return fmt.Errorf("reconcile: introspect: %w", err)
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return fmt.Errorf("reconcile: introspect reported errors: %v", diags)
	}

	// Align the two introspection-blind schema-global fields so they are never
	// mistaken for drift: the live PG version is ground truth, and [groups] are
	// pgdesign config that no catalog carries.
	target.PGVersion = actual.PGVersion
	target.Groups = actual.Groups

	// Comments are not part of the apply codomain (migrate does not emit COMMENT
	// ON). Strip them from both sides so documentation metadata never false-drifts.
	stripComments(target)
	stripComments(actual)

	// The live round-trip normalizer resolves the ≈_pg cast residue on expressions
	// (CHECKs, partial-index predicates, policy clauses). It opens its own session
	// (temp objects), independent of the apply connection.
	ln, err := livenorm.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("reconcile: open live normalizer: %w", err)
	}
	defer ln.Close()

	d := diff.DiffLive(target, actual, ln)
	if !d.IsEmpty() {
		return fmt.Errorf("reconcile: the applied database does not match the target model at revision %s — divergent objects:\n%s", head, diff.FormatTerminal(d))
	}
	return nil
}

// stripComments zeros every comment field reconcile's DiffLive would compare, so
// documentation metadata (which migrate does not emit) is excluded from the
// structural codomain check. See the package-level note on the comments limitation.
func stripComments(m *model.Schema) {
	for i := range m.Tables {
		m.Tables[i].Comment = ""
		for j := range m.Tables[i].Columns {
			m.Tables[i].Columns[j].Comment = ""
		}
	}
	for i := range m.Views {
		m.Views[i].Comment = ""
	}
	for i := range m.MaterializedViews {
		m.MaterializedViews[i].Comment = ""
	}
	for i := range m.Sequences {
		m.Sequences[i].Comment = ""
	}
	for i := range m.Domains {
		m.Domains[i].Comment = ""
	}
	for i := range m.CompositeTypes {
		m.CompositeTypes[i].Comment = ""
	}
	for i := range m.Functions {
		m.Functions[i].Comment = ""
	}
	for i := range m.Enums {
		m.Enums[i].Comment = ""
	}
}

// reconcileSchemaNames collects the distinct, non-empty schema names the target
// model spans (defaulting the empty schema to "public"), so introspection scopes
// exactly to the schemas the model declares — foreign schemas stay out of scope
// (reconcile does not auto-add imported schemas).
func reconcileSchemaNames(m *model.Schema) []string {
	seen := make(map[string]bool)
	var names []string
	add := func(s string) {
		if s == "" {
			s = "public"
		}
		if !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	for i := range m.Tables {
		add(m.Tables[i].Schema)
	}
	for i := range m.Views {
		add(m.Views[i].Schema)
	}
	for i := range m.MaterializedViews {
		add(m.MaterializedViews[i].Schema)
	}
	for i := range m.Sequences {
		add(m.Sequences[i].Schema)
	}
	if len(names) == 0 {
		return []string{"public"}
	}
	return names
}
