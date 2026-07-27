package migrate

// Unconditional checksums on the apply surface (roadmap 5.4, L2).
//
// Checksum verification is STRUCTURAL on the chain: LoadEdge re-derives the
// filename's edge-content hash and refuses any file whose bytes do not hash to
// its name (plus VerifyDown on every op's down cache). EVERY apply-surface edge
// read goes through LoadEdge — LoadLiveEdges and LoadAllEdges (which includes the
// archived originals loaded for mid-range resume) both call it — so a tampered
// active OR archived edge is refused before any DDL runs. There is no separate
// "verify checksum" step to forget: identity IS the checksum.
//
// 5.4 note: checksums exist ONLY on the apply surface. Post-5.6, journal-driven
// rollback reads the journal, never these files — so no rollback checksum surface
// exists (roadmap: "Checksums on the rollback path ... no such surface exists").

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tamperEdgeSlug rewrites the first edge file in dir whose body carries the given
// slug, changing its slug to a same-length replacement. Because the slug
// participates in edge identity but NOT in the filename on disk, the rewritten
// file's bytes no longer hash to its content-derived name — exactly the corruption
// LoadEdge must catch. Returns the tampered file's base name.
func tamperEdgeSlug(t *testing.T, dir, slug, replacement string) string {
	t.Helper()
	if len(slug) != len(replacement) {
		t.Fatalf("test bug: slug %q and replacement %q differ in length", slug, replacement)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	needle := `"slug":"` + slug + `"`
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), needle) {
			continue
		}
		tampered := strings.Replace(string(raw), needle, `"slug":"`+replacement+`"`, 1)
		if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
			t.Fatal(err)
		}
		return e.Name()
	}
	t.Fatalf("no edge file with slug %q in %s", slug, dir)
	return ""
}

// TestApplySurfaceRefusesTamperedActiveEdge: a tampered LIVE edge is refused by
// the apply surface's loader, naming the file (identity IS the checksum).
func TestApplySurfaceRefusesTamperedActiveEdge(t *testing.T) {
	p, _, _, _, _, _, _ := threeEdgeChain(t)
	name := tamperEdgeSlug(t, p.edgesPath(), "create-a", "create-X")

	_, err := p.LoadLiveEdges()
	if err == nil {
		t.Fatal("expected the apply surface to refuse a tampered active edge")
	}
	if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "content-derived name") {
		t.Errorf("refusal should name the tampered file and the content-hash mismatch: %v", err)
	}
}

// TestApplySurfaceRefusesTamperedArchivedEdge: a tampered ARCHIVED original (the
// edges a mid-range database resumes through) is refused when the traversal domain
// loads it, naming the file.
func TestApplySurfaceRefusesTamperedArchivedEdge(t *testing.T) {
	p, _, _, _, r1, _, r3 := threeEdgeChain(t)
	if _, err := SquashChain(p, r1.String(), r3.String(), ""); err != nil {
		t.Fatalf("SquashChain: %v", err)
	}
	// create-b is now an archived original.
	name := tamperEdgeSlug(t, p.archivePath(), "create-b", "create-Y")

	if _, err := p.LoadArchivedEdges(); err == nil {
		t.Fatal("expected the archive loader to refuse a tampered original")
	} else if !strings.Contains(err.Error(), name) {
		t.Errorf("refusal should name the tampered archived file: %v", err)
	}
	// The archive-inclusive traversal domain (LoadAllEdges) refuses too.
	if _, err := p.LoadAllEdges(); err == nil {
		t.Fatal("LoadAllEdges must refuse a tampered archived original (mid-range resume)")
	}
}

// TestApplyRefusesTamperedActiveEdgeDB: the full apply path refuses a tampered
// active edge against a live database, naming the file — no DDL runs.
func TestApplyRefusesTamperedActiveEdgeDB(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, _, _, _, _, _, _ := threeEdgeChain(t)
	name := tamperEdgeSlug(t, p.edgesPath(), "create-a", "create-Z")

	_, err = ApplyChain(ctx, conn, p, "5s", nil)
	if err == nil {
		t.Fatal("apply must refuse a tampered active edge")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("apply refusal should name the tampered file: %v", err)
	}
	// No table was created (apply refused before executing any op).
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.a') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("apply should not have created any table after refusing the tampered edge")
	}
}
