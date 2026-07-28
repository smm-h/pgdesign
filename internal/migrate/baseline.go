package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// BaselineReport summarizes a chain-mode baseline.
type BaselineReport struct {
	AlreadyAtBaseline bool   // true when the database was already stamped at this baseline (no-op)
	Target            string // the baseline target revision (registry-absent)
	EdgeFile          string // the written genesis baseline edge filename
}

// BaselineChain adopts a database whose schema was created by other means, or that
// has intentionally drifted, onto the on-disk chain (roadmap 5.10). Unlike
// `migrate upgrade`, it does NOT reconcile the TOML against the database and does
// NOT refuse drift — it ADOPTS the live state as the truth:
//
//  1. It synthesizes a revision manifest FROM INTROSPECTION (actual, a
//     registry-absent model) and attaches it as a GENESIS-PARENTED edge carrying
//     the introspected manifest. Per the 5.2 as-built precedent, the genesis edge
//     builds its ops FROM THE MODEL via the shim (the same path upgrade uses for
//     its genesis prefix edge), except the source is the INTROSPECTED model, not
//     the TOML. Degraded objects that introspection cannot fully model are the
//     documented lossiness (SM-vs-enum, etc.).
//  2. It stamps chain_position with boundary_kind='baseline' (rollback-frozen per
//     5.6; the boundary logic refuses to roll back across it).
//
// The two legacy semver guards are re-expressed against the chain graph:
//   - DIVERGENCE: the stamped position (if any) must be chain-reachable — an
//     off-chain current_revision is corruption, the chain analogue of a recorded
//     version with no migration file.
//   - OUT-OF-ORDER: the baseline target must be reachable from the stamped
//     position — you cannot baseline backward. A genesis-parented baseline target
//     is reachable only from genesis, so re-baselining is admitted only from the
//     genesis floor; a database already advanced on the chain is refused.
//
// TWO-HEADS GUARD (hard error): the baseline edge is REGISTRY-ABSENT class and is
// genesis-parented, so appending it to a chain that ALREADY has a live head would
// create a second, cross-class head — an unresolvable fork (cross-class rebase is
// not supported). Baseline is for adopting a FOREIGN database into an EMPTY chain,
// so a pre-existing live head is refused outright: the remediation is to regenerate
// the chain from the adopted state, not to rebase the two heads.
func BaselineChain(ctx context.Context, conn *pgx.Conn, p *ChainProject, actual *model.Schema, description string) (*BaselineReport, error) {
	target, err := rev.Compute(actual, rev.RegistryAbsent)
	if err != nil {
		return nil, fmt.Errorf("migrate baseline: compute baseline target revision: %w", err)
	}

	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("migrate baseline: another migration operation is in progress (could not acquire the advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	// Reachability guards against any EXISTING stamped position.
	structuresExist, err := ChainStructuresExist(ctx, conn)
	if err != nil {
		return nil, err
	}
	if structuresExist {
		cp, ok, err := readChainPosition(ctx, conn)
		if err != nil {
			return nil, err
		}
		if ok {
			if cp.CurrentRevision == target.String() {
				return &BaselineReport{AlreadyAtBaseline: true, Target: target.String()}, nil // idempotent no-op
			}
			if err := checkBaselineReachability(p, cp.CurrentRevision, target); err != nil {
				return nil, err
			}
		}
	}

	// Two-heads guard: refuse if the on-disk chain already carries a live head
	// (a pre-existing chain). Appending a genesis-parented, registry-absent
	// baseline edge would fork it into two cross-class heads.
	if err := checkBaselineEmptyChain(p, target); err != nil {
		return nil, err
	}

	// Build the genesis baseline edge FROM THE INTROSPECTED MODEL (registry-absent),
	// the same model->ops->edge path upgrade uses, and prove the store is consistent.
	edgeFile, err := writeBaselineEdge(p, actual)
	if err != nil {
		return nil, err
	}
	if err := VerifyChainConsistency(p); err != nil {
		return nil, fmt.Errorf("migrate baseline: chain consistency check failed after writing the baseline edge: %w", err)
	}

	// Stamp chain_position in one transaction (create structures for a foreign
	// database first). boundary_kind='baseline' freezes rollback across it (5.6).
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate baseline: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if !structuresExist {
		if err := CreateTrackingStructures(ctx, tx); err != nil {
			return nil, err
		}
	}
	if err := upsertBaselinePosition(ctx, tx, structuresExist, target.String()); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("migrate baseline: commit: %w", err)
	}

	return &BaselineReport{Target: target.String(), EdgeFile: edgeFile}, nil
}

// writeBaselineEdge folds the introspected model into a single genesis edge
// (empty -> baseline target, registry-absent class) plus its objects and manifest.
// It is idempotent (content-addressed). An empty model is a hard error.
func writeBaselineEdge(p *ChainProject, actual *model.Schema) (string, error) {
	base := &model.Schema{Name: actual.Name, PGVersion: actual.PGVersion}
	d := diff.Diff(actual, base)
	if d.IsEmpty() {
		return "", fmt.Errorf("migrate baseline: the introspected schema is empty; there is nothing to baseline")
	}
	m, _ := GenerateMigration(d, actual, "", extregistry.NewBuiltinRegistry())
	if len(m.DDLOps) == 0 && len(m.DMLOps) == 0 {
		return "", fmt.Errorf("migrate baseline: the introspected schema produced no operations; there is nothing to baseline")
	}
	name, err := GenerateEdge(p, m, actual, nil, rev.Revision{}, rev.RegistryAbsent, "baseline")
	if err != nil {
		return "", fmt.Errorf("migrate baseline: write baseline edge: %w", err)
	}
	return name, nil
}

// checkBaselineEmptyChain refuses baseline when the on-disk chain already has a
// live head other than the baseline target itself. Loading only LIVE edges (head
// finding is live-only), it errors on any pre-existing head — the sole tolerated
// head is the baseline target (an idempotent re-run whose edge was already
// written but whose stamp did not complete).
func checkBaselineEmptyChain(p *ChainProject, target rev.Revision) error {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return err
	}
	remap, err := p.LoadRemap()
	if err != nil {
		return err
	}
	heads, err := findLiveHeads(live, remap)
	if err != nil {
		return err
	}
	targetCanon := canon(target.String(), remap)
	for _, h := range heads {
		if h != targetCanon {
			return fmt.Errorf("migrate baseline: this project already has a chain (live head %s); baseline adopts FOREIGN databases into EMPTY chains — regenerate the chain from the adopted state instead", h)
		}
	}
	return nil
}

// checkBaselineReachability enforces the two re-expressed guards for a database
// that already carries a chain position.
func checkBaselineReachability(p *ChainProject, currentRev string, target rev.Revision) error {
	all, err := p.LoadAllEdges()
	if err != nil {
		return err
	}
	remap, err := p.LoadRemap()
	if err != nil {
		return err
	}
	start := canon(currentRev, remap)

	// DIVERGENCE: the stamped position must be a known chain node (an endpoint of
	// some edge, or genesis). An off-chain position is corruption.
	if start != "" && !revisionIsChainNode(start, all, remap) {
		return fmt.Errorf("migrate baseline: divergence: the database's stamped position %s is not reachable on the chain (off-chain or corrupt position); resolve it before baselining", currentRev)
	}

	// OUT-OF-ORDER: the baseline target must be reachable FROM the stamped position.
	// The genesis-parented baseline target is reachable only from genesis.
	canonEdges := toCanonEdges(all, remap)
	if !reachable(start, canon(target.String(), remap), canonEdges) && start != canon(target.String(), remap) {
		return fmt.Errorf("migrate baseline: out-of-order: the baseline target %s is not reachable from the database's stamped position %s — a database already advanced on the chain cannot be baselined backward", target, currentRev)
	}
	return nil
}

// revisionIsChainNode reports whether s (a canonical revision string) appears as
// an endpoint of any edge in the graph.
func revisionIsChainNode(s string, all []Edge, remap RemapTable) bool {
	for _, e := range all {
		if canon(e.Target.String(), remap) == s {
			return true
		}
		if !e.Parent.IsZero() && canon(e.Parent.String(), remap) == s {
			return true
		}
	}
	return false
}

// upsertBaselinePosition writes (or replaces) the singleton chain_position at the
// baseline target with boundary_kind='baseline'. Both writes route through the
// tracking_chain.go writer (the single-write-path invariant).
func upsertBaselinePosition(ctx context.Context, tx pgx.Tx, positionExists bool, target string) error {
	if positionExists {
		return rebaselineChainPosition(ctx, tx, target, int(enc.CodecVersion))
	}
	return insertChainPosition(ctx, tx, chainPosition{
		CurrentRevision:  target,
		BoundaryRevision: target,
		BoundaryKind:     "baseline",
		CodecEpoch:       int(enc.CodecVersion),
	})
}

// Baseline marks a database as being at a specific migration version without
// actually applying any migrations. This is used when adopting pgdesign
// migrations for an existing database whose schema was created by other means.
//
// All discovered migration files with version <= targetVersion are recorded as
// baseline-applied. This ensures that a subsequent Apply sees them as already
// applied and skips them.
//
// Additive idempotency: re-running baseline records any versions that are
// discovered but not yet recorded (versions <= target). A conflict is reported
// only when a previously recorded version is absent from the discovered set
// (true divergence -- the migration file was deleted).
//
// Out-of-order guard: if a discovered migration file has a version < the
// maximum already-applied version and is not yet recorded, this indicates a
// migration file was added after later versions were applied. This is a hard
// error requiring explicit adoption.
func Baseline(ctx context.Context, conn *pgx.Conn, migrationsDir string, targetVersion string, description string) error {
	// Chain-mode baseline is BaselineChain (roadmap 5.10); the CLI dispatches by
	// mode, so this legacy semver path only runs for legacy (semver-TOML) projects.
	// Acquire advisory lock (same pattern as Apply).
	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("another migration is in progress (could not acquire advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return err
	}

	// Discover migration files.
	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		return fmt.Errorf("discover migrations: %w", err)
	}

	// Filter to versions <= targetVersion.
	var toRecord []migrationFile
	for _, mf := range migrations {
		if compareSemver(mf.version, targetVersion) <= 0 {
			toRecord = append(toRecord, mf)
		}
	}

	if len(toRecord) == 0 {
		return fmt.Errorf("no migration files found with version <= %s in %s", targetVersion, migrationsDir)
	}

	// Validate that the target version actually exists in the discovered set.
	targetFound := false
	for _, mf := range toRecord {
		if mf.version == targetVersion {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return fmt.Errorf("target version %s not found in migrations directory %s", targetVersion, migrationsDir)
	}

	// Check for existing records.
	existing, err := AppliedVersions(ctx, conn)
	if err != nil {
		return err
	}
	existingSet := make(map[string]bool, len(existing))
	for _, v := range existing {
		existingSet[v] = true
	}

	// Build the set of versions we want to record.
	toRecordSet := make(map[string]bool, len(toRecord))
	for _, mf := range toRecord {
		toRecordSet[mf.version] = true
	}

	// Divergence check: any recorded version absent from discovered set is
	// true divergence (migration file was deleted).
	for _, v := range existing {
		if compareSemver(v, targetVersion) <= 0 && !toRecordSet[v] {
			return fmt.Errorf("baseline divergence: version %s is recorded in the database but no corresponding migration file exists in %s; this may indicate a deleted migration file", v, migrationsDir)
		}
	}

	// Find maximum already-applied version for out-of-order guard.
	var maxApplied string
	for _, v := range existing {
		if maxApplied == "" || compareSemver(v, maxApplied) > 0 {
			maxApplied = v
		}
	}

	// Out-of-order guard: a discovered file with version < max-applied that
	// is NOT already recorded means it was added after later versions were
	// applied. This is a hard error.
	if maxApplied != "" {
		for _, mf := range toRecord {
			if compareSemver(mf.version, maxApplied) < 0 && !existingSet[mf.version] {
				return fmt.Errorf("out-of-order migration detected: version %s was discovered but version %s is already applied; this migration file appears to have been added after later versions were applied -- resolve the ordering (renumber or apply the missing versions) before baselining", mf.version, maxApplied)
			}
		}
	}

	// Record all versions that are not yet recorded.
	for _, mf := range toRecord {
		if existingSet[mf.version] {
			continue // Already recorded: skip (additive idempotency).
		}
		if err := RecordMigration(ctx, conn, mf.version, "baseline", description); err != nil {
			return err
		}
	}

	return nil
}
