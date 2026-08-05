package main

import (
	"errors"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// reviseProjectConfig is the pgdesign.toml every revise test writes: one SQL
// output plus a chain-format migrations dir and a pinned pg_version.
const reviseProjectConfig = `[project]
schemas = ["schema.toml"]
migrations_dir = "migrations"

[database]
pg_version = 16

[output.sql]
format = "sql"
path = "out.sql"
`

// setupReviseRepo creates a temp git repo with a committed schema.toml (copied
// from the named testdata fixture) and pgdesign.toml. It returns the repo dir.
// initGit controls whether the dir is a git repo at all: passing false is how the
// commit-failure test forces safegit to fail.
func setupReviseRepo(t *testing.T, fixture string, initGit bool) string {
	t.Helper()
	config.CodegenModes = SupportedModes()

	dir := t.TempDir()

	src, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %q: %v", fixture, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), src, 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pgdesign.toml"), []byte(reviseProjectConfig), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}

	if initGit {
		runGit(t, dir, "init", "-q")
		runGit(t, dir, "config", "user.email", "revise-test@example.com")
		runGit(t, dir, "config", "user.name", "revise test")
		runGit(t, dir, "add", "schema.toml", "pgdesign.toml")
		runGit(t, dir, "commit", "-q", "-m", "init")
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitCommitCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list: %v\n%s", err, out)
	}
	n := 0
	for _, r := range strings.TrimSpace(string(out)) {
		n = n*10 + int(r-'0')
	}
	return n
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written. revise reports everything (including the DB-tier skip notice) on
// stderr, so the tier-naming assertions read it here.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()
	fn()
	w.Close()
	os.Stderr = orig
	return <-done
}

// revisionFromSQL extracts the "pgdesign-revision: <rev>" stamp line from a
// generated SQL output, or "" if absent.
func revisionFromSQL(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "pgdesign-revision: "); i >= 0 {
			return strings.TrimSpace(line[i+len("pgdesign-revision: "):])
		}
	}
	return ""
}

// TestRunRevise_Genesis_EndToEnd covers the roadmap 6.1 happy path on a fresh
// (genesis) project with no database configured: the pure tier regenerates
// outputs, creates the chain and its genesis migration edge, and lands TWO
// commits (outputs, then migration+chain+store); the build output stamp and the
// migration's to-revision are the SAME revision everywhere. With no DB, revise
// exits reviseExitDBSkipped (non-zero) after committing the pure tier.
func TestRunRevise_Genesis_EndToEnd(t *testing.T) {
	testenv.Isolate(t)
	dir := setupReviseRepo(t, "freshness_schema.toml", true)
	t.Chdir(dir)

	before := gitCommitCount(t, dir)

	code := runRevise(nil, true, nil, "")
	if code != reviseExitDBSkipped {
		t.Fatalf("expected exit %d (DB tier skipped, pure committed), got %d", reviseExitDBSkipped, code)
	}

	// Two new commits: pure outputs, then migration+chain+store.
	if got := gitCommitCount(t, dir); got != before+2 {
		t.Fatalf("expected 2 new commits (before=%d), got %d", before, got)
	}

	// Build output written and stamped.
	sqlPath := filepath.Join(dir, "out.sql")
	if _, err := os.Stat(sqlPath); err != nil {
		t.Fatalf("expected build output at %q: %v", sqlPath, err)
	}
	stampRev := revisionFromSQL(t, sqlPath)
	if stampRev == "" {
		t.Fatal("build output carries no pgdesign-revision stamp")
	}

	// Genesis project became a chain project.
	migDir := filepath.Join(dir, "migrations")
	if _, err := os.Stat(filepath.Join(migDir, "chain")); err != nil {
		t.Fatalf("revise did not create the chain dir: %v", err)
	}
	if !migrate.IsChainMode(migDir) {
		t.Fatal("migrations dir is not chain mode after genesis revise")
	}

	// One revision everywhere: the chain head's revision equals the build stamp.
	p, err := migrate.OpenChainProject(migDir)
	if err != nil {
		t.Fatalf("open chain project: %v", err)
	}
	head, _, err := migrate.ChainHead(p)
	if err != nil {
		t.Fatalf("chain head: %v", err)
	}
	if head.String() != stampRev {
		t.Fatalf("revision mismatch: build stamp %q != chain head %q", stampRev, head.String())
	}

	// Working tree is clean (both commits captured everything revise wrote).
	if out := gitStatusPorcelain(t, dir); out != "" {
		t.Fatalf("working tree not clean after revise:\n%s", out)
	}
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRunRevise_BCNFBlocksPureTier: a BCNF violation is promoted to error in the
// pure NF core and blocks the whole pure tier — nothing is written or committed.
func TestRunRevise_BCNFBlocksPureTier(t *testing.T) {
	testenv.Isolate(t)
	dir := setupReviseRepo(t, "revise_bcnf.toml", true)
	t.Chdir(dir)

	before := gitCommitCount(t, dir)

	stderr := captureStderr(t, func() {
		if code := runRevise(nil, true, nil, ""); code != reviseExitPureFailure {
			t.Fatalf("expected pure-tier failure %d, got %d", reviseExitPureFailure, code)
		}
	})

	if !strings.Contains(stderr, "W103") && !strings.Contains(stderr, "normal form") {
		t.Errorf("expected NF/BCNF diagnostic in stderr, got:\n%s", stderr)
	}
	if got := gitCommitCount(t, dir); got != before {
		t.Fatalf("BCNF block must commit nothing: before=%d after=%d", before, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.sql")); err == nil {
		t.Fatal("BCNF block must not write build outputs")
	}
	if _, err := os.Stat(filepath.Join(dir, "migrations", "chain")); err == nil {
		t.Fatal("BCNF block must not create the chain")
	}
}

// TestRunRevise_DBUnreachable_NamesSkippedTier: with an unreachable database the
// pure tier still commits, revise exits non-zero, and the message names the
// skipped DB tier.
func TestRunRevise_DBUnreachable_NamesSkippedTier(t *testing.T) {
	testenv.Isolate(t)
	dir := setupReviseRepo(t, "freshness_schema.toml", true)
	t.Chdir(dir)

	before := gitCommitCount(t, dir)

	var code int
	stderr := captureStderr(t, func() {
		// Port 1 refuses immediately: a fast, deterministic "unreachable".
		code = runRevise(nil, true, nil, "postgres://127.0.0.1:1/nope?connect_timeout=1")
	})
	if code != reviseExitDBSkipped {
		t.Fatalf("expected DB-skipped exit %d, got %d\n%s", reviseExitDBSkipped, code, stderr)
	}
	if !strings.Contains(stderr, "DB tier SKIPPED") {
		t.Errorf("expected the message to name the skipped DB tier, got:\n%s", stderr)
	}
	// Pure tier committed regardless of the DB tier outcome.
	if got := gitCommitCount(t, dir); got != before+2 {
		t.Fatalf("pure tier must commit even when DB is unreachable: before=%d after=%d", before, got)
	}
}

// TestRunRevise_CommitFailureHardErrors: when the commit cannot run (not a git
// repo, so safegit fails) revise is a hard error — no warn-and-continue.
func TestRunRevise_CommitFailureHardErrors(t *testing.T) {
	testenv.Isolate(t)
	dir := setupReviseRepo(t, "freshness_schema.toml", false) // NOT a git repo
	t.Chdir(dir)

	stderr := captureStderr(t, func() {
		if code := runRevise(nil, true, nil, ""); code != reviseExitPureFailure {
			t.Fatalf("expected commit failure to be a hard error (%d), got %d", reviseExitPureFailure, code)
		}
	})
	if !strings.Contains(stderr, "safegit commit failed") {
		t.Errorf("expected a commit-failure error, got:\n%s", stderr)
	}
}

// TestRunRevise_LegacyProjectRejected: a legacy semver-migration project is a
// hard error naming `migrate upgrade`.
func TestRunRevise_LegacyProjectRejected(t *testing.T) {
	testenv.Isolate(t)
	dir := setupReviseRepo(t, "freshness_schema.toml", true)
	migDir := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations: %v", err)
	}
	// A legacy semver-TOML migration file (no chain/ dir) marks the project legacy.
	if err := os.WriteFile(filepath.Join(migDir, "0.1.0.toml"), []byte("description = \"legacy\"\n"), 0o644); err != nil {
		t.Fatalf("write legacy migration: %v", err)
	}
	t.Chdir(dir)

	before := gitCommitCount(t, dir)
	stderr := captureStderr(t, func() {
		if code := runRevise(nil, true, nil, ""); code != reviseExitPureFailure {
			t.Fatalf("expected legacy project to be rejected (%d), got %d", reviseExitPureFailure, code)
		}
	})
	if !strings.Contains(stderr, "migrate upgrade") {
		t.Errorf("expected the rejection to point at `migrate upgrade`, got:\n%s", stderr)
	}
	if got := gitCommitCount(t, dir); got != before {
		t.Fatalf("legacy rejection must commit nothing: before=%d after=%d", before, got)
	}
}

// TestRunRevise_TwoHeadsPointAtRebase: a chain with two live heads is an
// unresolved fork; revise (via the shared chain-generate core) hard-errors,
// naming both heads and pointing at `migrate rebase`.
func TestRunRevise_TwoHeadsPointAtRebase(t *testing.T) {
	testenv.Isolate(t)
	config.CodegenModes = SupportedModes()

	// Build two distinct genesis-eligible schemas via the real parse/build path.
	base := t.TempDir()
	writeForkSchema(t, base, "a.toml", "table_a")
	writeForkSchema(t, base, "b.toml", "table_b")
	if err := os.WriteFile(filepath.Join(base, "pgdesign.toml"), []byte("[database]\npg_version = 16\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	schemaA, _, ec := parseAndBuild(nil, []string{filepath.Join(base, "a.toml")})
	if ec != 0 {
		t.Fatal("build schema A failed")
	}
	schemaB, _, ec := parseAndBuild(nil, []string{filepath.Join(base, "b.toml")})
	if ec != 0 {
		t.Fatal("build schema B failed")
	}

	migDir := filepath.Join(base, "migrations")
	// Seed TWO genesis roots (both parent=zero, prev=nil) — an unresolved fork.
	seedGenesisRoot(t, migDir, schemaA, "root-a")
	seedGenesisRoot(t, migDir, schemaB, "root-b")

	_, err := generateChainEdge(schemaA, migDir, diff.RenameSpec{})
	if err == nil {
		t.Fatal("expected a fork error from a two-head chain")
	}
	msg := err.Error()
	if !strings.Contains(msg, "rebase") {
		t.Errorf("fork error must point at `migrate rebase`, got: %s", msg)
	}
	var fork *migrate.ForkError
	if !errors.As(err, &fork) {
		t.Errorf("expected a *migrate.ForkError, got %T: %v", err, err)
	} else if len(fork.Heads) != 2 {
		t.Errorf("expected 2 heads named, got %d: %v", len(fork.Heads), fork.Heads)
	}
}

func writeForkSchema(t *testing.T, dir, file, table string) {
	t.Helper()
	content := "format_version = 1\n[meta]\nschema = \"fork\"\n\n[tables." + table + "]\ncomment = \"fork fixture\"\n\n[tables." + table + ".columns.id]\ntype = \"id\"\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", file, err)
	}
}

// seedGenesisRoot writes a genesis edge (parent=zero, prev=nil) for schema s,
// bypassing the head — two such roots make an unresolved fork.
func seedGenesisRoot(t *testing.T, migDir string, s *model.Schema, slug string) {
	t.Helper()
	p, err := migrate.OpenChainProject(migDir)
	if err != nil {
		t.Fatalf("open chain project: %v", err)
	}
	d := diff.Diff(s, &model.Schema{Name: s.Name, PGVersion: s.PGVersion})
	m, _ := migrate.GenerateMigration(d, s, "", extregistry.NewBuiltinRegistry())
	if _, err := migrate.GenerateEdge(p, m, s, nil, rev.Revision{}, rev.RegistryPresent, slug); err != nil {
		t.Fatalf("seed genesis root %q: %v", slug, err)
	}
}
