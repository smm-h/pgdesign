package main

import (
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
	"github.com/smm-h/strictcli/go/strictcli"
)

// classificationEntry is one row of the pinned classification table.
type classificationEntry struct {
	effect        string
	consequential bool
}

// classification pins every command's strictcli effect classification and its
// `consequential` declaration. The table is TOTAL over the live command set --
// the same pre-commitment writersRegistry makes for the writer taxonomy -- so a
// new command that does not declare its effect turns this test red, and a
// reclassification is a deliberate edit here with the reasoning beside it.
//
// The two questions are separate, per the strictcli effects contract:
//
//   - effect (§1) answers "should a dry run RECORD this rather than execute
//     it?". read_only means no user-visible mutation anywhere -- not on disk,
//     not in the target database.
//   - consequential (§8.1) answers "are these effects worth INTERRUPTING
//     someone for?". The framework prompts for exactly these commands and no
//     others. The bar: destructive on a remote, creates a named external
//     resource a rerun cannot un-create, or makes something public or live
//     that was not before.
//
// EFFECT -- the local-filesystem writers. These line up with writersRegistry's
// non-ClassNonWriter entries, which is not a coincidence: that taxonomy already
// answers "does this write to disk".
//
//   - build, revise -- full regenerators; they write every configured output
//     and git-commit them.
//   - codegen -- writes the generated artifact at --output.
//   - fmt -- rewrites a schema source file in place.
//   - introspect -- writes the candidate schema TOML at --output.
//   - seed -- writes the generated seed SQL, and with --apply executes it
//     against the target database.
//   - migrate generate/squash/rebase/upgrade/baseline -- write the
//     append-only migration chain, object store and revision manifests.
//   - import lock/update -- vendor the imported surface and write the lockfile.
//   - testdb init -- writes the consumer test-wrapper scaffolding.
//
// EFFECT -- the database writers. These write nothing locally (writersRegistry
// calls them non-writers) but they change a live database, which is exactly the
// user-visible mutation §1 is about:
//
//   - migrate apply -- executes the pending DDL.
//   - migrate rollback -- executes journaled inverse ops.
//   - migrate upgrade, migrate baseline -- stamp adoption boundaries into the
//     database (they are on both lists: chain files AND database rows).
//   - migrate test -- creates a shadow database, replays into it, drops it.
//   - testdb setup/teardown/gc -- create and drop databases on the server.
//
// EFFECT -- read_only. Everything left prints and nothing more:
//
//   - generate -- renders DDL to stdout. It has no output flag; the
//     file-writing generator is `build`.
//   - diff, stats, migrate plan, migrate status -- read (files, or the live
//     catalog) and report.
//   - serve -- reads the project or the live database and serves it over
//     HTTP. It writes no file and issues no write query; the whole surface is
//     GET handlers.
//   - check -- strictcli's own auto-registered command, classified read_only
//     by the framework (contract §7.5). Its row is here because the table is
//     total over the live command set.
//
// CONSEQUENTIAL -- six commands, and the reason each clears the bar:
//
//   - migrate apply -- runs generated DDL against whatever --db/PGDESIGN_DB
//     points at, which in normal use is a real, often remote, often production
//     database. DROP COLUMN and DROP TABLE are ordinary migration ops. This is
//     the canonical destructive-on-a-remote act.
//   - migrate rollback -- the same, in reverse. Journaled inverses do not
//     restore data (the contract's vacuous DML inverse); rolling back past a
//     drop destroys rows for good.
//   - migrate upgrade -- one-time adoption of a legacy database onto the
//     chain. It folds the tracking rows into the journal and stamps the
//     upgrade boundary in one transaction. Run once per database; a rerun
//     cannot un-stamp it.
//   - migrate baseline -- stamps a rollback-frozen baseline boundary onto a
//     live database, declaring its current state to be the origin. One-way by
//     construction: after it, everything before the baseline is unreachable.
//   - testdb teardown -- DROP DATABASE on the target server. Destructive, and
//     it takes its target from a URL, so a mistyped one drops the wrong
//     database.
//   - testdb gc -- drops EVERY database on the server matching the pgdesign
//     test pattern older than a cutoff. Same destruction, with a blast radius
//     the caller does not enumerate.
//
// NOT consequential, where the call was close enough to record:
//
//   - seed -- `--apply --clean` emits TRUNCATE CASCADE against the target
//     database, which is genuinely destructive. It is still not declared,
//     because that is a doubly-opted-into sub-mode of a command whose dominant
//     use is generating SQL to stdout, and strictcli's declaration is
//     per-command with no per-flag granularity. Declaring it would prompt on
//     every offline seed -- exactly the 1:10 signal-to-noise ratio that the
//     consequence round exists to avoid. The sharp edge is recorded here so a
//     future per-flag mechanism has somewhere to land.
//   - testdb setup -- creates a uniquely-named ephemeral database that gc and
//     teardown exist to remove. It is designed to be run by test harnesses in
//     a loop; prompting would break exactly the workflow it serves.
//   - migrate test -- creates and drops its own shadow database and leaves the
//     target schema untouched.
//   - build, revise -- write files and git-commit locally. Local commits are
//     recoverable and are not the bar (the fleet does not declare
//     `safegit commit` consequential either).
//   - migrate squash, migrate rebase -- retire superseded edges INTACT to
//     migrations/archive/ rather than rewriting them, by design.
//   - serve -- binds a socket, and --bind can expose it beyond localhost, but
//     it is a foreground process the user started and stops; nothing survives
//     it.
var classification = map[string]classificationEntry{
	// Local-filesystem writers.
	"build":            {strictcli.EffectMutating, false},
	"revise":           {strictcli.EffectMutating, false},
	"codegen":          {strictcli.EffectMutating, false},
	"fmt":              {strictcli.EffectMutating, false},
	"introspect":       {strictcli.EffectMutating, false},
	"seed":             {strictcli.EffectMutating, false},
	"migrate.generate": {strictcli.EffectMutating, false},
	"migrate.squash":   {strictcli.EffectMutating, false},
	"migrate.rebase":   {strictcli.EffectMutating, false},
	"import.lock":      {strictcli.EffectMutating, false},
	"import.update":    {strictcli.EffectMutating, false},
	"testdb.init":      {strictcli.EffectMutating, false},

	// Database writers.
	"migrate.apply":    {strictcli.EffectMutating, true},
	"migrate.rollback": {strictcli.EffectMutating, true},
	"migrate.upgrade":  {strictcli.EffectMutating, true},
	"migrate.baseline": {strictcli.EffectMutating, true},
	"migrate.test":     {strictcli.EffectMutating, false},
	"testdb.setup":     {strictcli.EffectMutating, false},
	"testdb.teardown":  {strictcli.EffectMutating, true},
	"testdb.gc":        {strictcli.EffectMutating, true},

	// Read-only.
	"generate":       {strictcli.EffectReadOnly, false},
	"diff":           {strictcli.EffectReadOnly, false},
	"serve":          {strictcli.EffectReadOnly, false},
	"stats":          {strictcli.EffectReadOnly, false},
	"migrate.plan":   {strictcli.EffectReadOnly, false},
	"migrate.status": {strictcli.EffectReadOnly, false},
	"check":          {strictcli.EffectReadOnly, false},
}

// TestClassification_Totality is the mechanical pre-commit for the effect
// taxonomy, mirroring TestWritersRegistry_Totality: the live command set and
// the classification table must be identical, and every row must match what the
// command actually declares.
func TestClassification_Totality(t *testing.T) {
	testenv.Isolate(t)

	app := buildApp()
	live := collectCommandPaths(app)

	var missing, stale []string
	for p := range live {
		if _, ok := classification[p]; !ok {
			missing = append(missing, p)
		}
	}
	for p := range classification {
		if !live[p] {
			stale = append(stale, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, p := range missing {
		t.Errorf("command %q has no classification row; declare its effect (and whether it is consequential) with the reasoning", p)
	}
	for _, p := range stale {
		t.Errorf("classification pins %q but no such command is registered", p)
	}
}

// TestClassification_Declared checks each row against the live registration.
func TestClassification_Declared(t *testing.T) {
	testenv.Isolate(t)

	cmds := collectCommands(buildApp())
	var names []string
	for name := range classification {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cmd, ok := cmds[name]
		if !ok {
			continue // totality test reports this
		}
		want := classification[name]
		if cmd.Effect != want.effect {
			t.Errorf("command %q: effect = %q, pinned %q", name, cmd.Effect, want.effect)
		}
		if cmd.Consequential != want.consequential {
			t.Errorf("command %q: consequential = %v, pinned %v", name, cmd.Consequential, want.consequential)
		}
	}
}

// collectCommands is collectCommandPaths' value-carrying twin: same traversal,
// but it keeps the *Command so the declarations can be read off it.
func collectCommands(app *strictcli.App) map[string]*strictcli.Command {
	out := map[string]*strictcli.Command{}
	for name, cmd := range app.Commands() {
		out[name] = cmd
	}
	var walk func(prefix string, groups map[string]*strictcli.Group)
	walk = func(prefix string, groups map[string]*strictcli.Group) {
		for gname, g := range groups {
			gp := gname
			if prefix != "" {
				gp = prefix + "." + gname
			}
			for cname, cmd := range g.Commands {
				out[gp+"."+cname] = cmd
			}
			walk(gp, g.Groups)
		}
	}
	walk("", app.Groups())
	return out
}

// TestNoReservedGlobalFlagNames guards strictcli's reserved quartet at the app
// level. pgdesign used to declare a --quiet global and a --dry-run flag on both
// `build` and `migrate apply`; all three are framework-owned now and are read
// off the Context. Command-level flags are covered implicitly: strictcli panics
// at registration for a reserved name anywhere, and buildApp registers
// everything.
func TestNoReservedGlobalFlagNames(t *testing.T) {
	testenv.Isolate(t)

	reserved := map[string]bool{"dry-run": true, "yes": true, "quiet": true, "verbose": true}
	for _, f := range buildApp().GlobalFlags() {
		if reserved[f.Name] {
			t.Errorf("global flag %q is reserved by the framework", f.Name)
		}
	}
}
