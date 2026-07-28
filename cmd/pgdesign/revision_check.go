package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

// This file holds the roadmap-6.2 revision enforcement: the STAMP-EXTRACTOR
// signal and the `revision` check that consumes it.
//
// Two complementary staleness signals exist across the two sibling checks:
//
//   - BYTE-COMPARE (the existing `build`/freshness check, checks.go): "this file
//     is not what the model produces" — it re-plans and byte-compares content.
//   - STAMP-EXTRACTOR (this file, the `revision` check): "a sibling is at a
//     different revision" — it reads the embedded provenance revision (genkit
//     comment stamp, or the JSON envelope field) and compares it to the revision
//     the CURRENT model produces, WITHOUT regenerating content.
//
// A tampered header trips BOTH: byte-compare sees changed bytes ([stale]); the
// stamp-extractor sees a wrong revision ([revision-mismatch]). Content drift with
// an intact header trips only byte-compare. An old but internally-consistent
// build (every file at the same PRIOR revision) trips only the stamp-extractor.
// The two are deliberately distinct so the diagnosis is unambiguous.
//
// The stamp-extractor honors the roadmap-4.2 artifact classes:
//   - comment-stamped (sql, d2, graphql, doc, codegen): carry the FULL-PROJECT
//     revision, even when group/source-filtered (provenance, not content).
//   - json (in-band envelope): carries the RECOMPUTED revision of its own
//     (possibly filtered) model — the documented filtered-JSON exception, since
//     the envelope's integrity law forbids a full-project stamp on filtered bytes.
//   - svg: structurally exempt (non-deterministic render).
//   - .sqlsplit companion, seed output, migrations, stdout: never in [output] as a
//     stamp-bearing artifact here, so never flagged by this check.

// revisionMismatchPrefix labels stamp-extractor findings so they read distinctly
// from the byte-compare check's [stale]/[missing]/[orphan] lines.
const revisionMismatchPrefix = "[revision-mismatch]"

// artifactRevisionFindings inspects the on-disk artifact(s) for one [output]
// entry and returns STAMP-EXTRACTOR findings: each names a file whose embedded
// provenance revision does not match what the current model produces. It honors
// the roadmap-4.2 per-format stamp discipline (see the file header). A missing
// file yields NO finding — "missing" belongs to the byte-compare/build check.
// The filtering reuses applyOutputFilters so the refusal and the check agree
// byte-for-byte on which model a filtered output stamps (roadmap 0.5's one write
// path).
func artifactRevisionFindings(out config.OutputConfig[config.AbsolutePath], schema *model.Schema, fullRev string) ([]string, error) {
	path := string(out.Path)
	switch out.Format {
	case "svg":
		// Structurally exempt: d2 rendering is non-deterministic.
		return nil, nil
	case "json":
		return jsonEnvelopeFindings(out, schema)
	case "sql", "d2", "graphql", "doc":
		return commentStampFindings(path, fullRev)
	case "codegen":
		return codegenStampFindings(path, fullRev)
	default:
		return nil, fmt.Errorf("revision check: output %q has unsupported format %q", path, out.Format)
	}
}

// commentStampFindings checks a single comment-stamped file: it must carry the
// full-project revision. A missing/unparseable stamp on a stamp-bearing format is
// itself staleness (roadmap 6.2: "missing/old-format stamps = stale").
func commentStampFindings(path, fullRev string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil // missing is the byte-compare check's signal
	}
	if err != nil {
		return nil, err
	}
	return stampFinding(path, data, fullRev), nil
}

// stampFinding returns a (possibly empty) finding for one comment-stamped file's
// bytes against the expected full-project revision.
func stampFinding(path string, data []byte, fullRev string) []string {
	ps, ok := genkit.ParseStamp(data)
	if !ok {
		return []string{fmt.Sprintf("%s %s: missing or unrecognized provenance stamp (stale — run `pgdesign build`)", revisionMismatchPrefix, path)}
	}
	if ps.Revision != fullRev {
		found := ps.Revision
		if found == "" {
			found = "(none)"
		}
		return []string{fmt.Sprintf("%s %s: stamped revision %s, project revision %s (a sibling is at a different revision; run `pgdesign build`)", revisionMismatchPrefix, path, found, fullRev)}
	}
	return nil
}

// codegenStampFindings checks a codegen output. Single-file outputs are one
// stamped file; multi-file outputs are a directory whose EVERY STAMPED file must
// carry the full-project revision. Files that carry no stamp at all (data files,
// __init__ shims) are skipped — the check only asserts agreement where pgdesign
// actually stamps.
func codegenStampFindings(path, fullRev string) ([]string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return commentStampFindings(path, fullRev)
	}
	var findings []string
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(path, p)
		if relErr != nil {
			return relErr
		}
		if orphanIgnored(filepath.ToSlash(rel)) {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if !genkit.HasStamp(data) {
			return nil // unstamped data file — not enforced
		}
		findings = append(findings, stampFinding(p, data, fullRev)...)
		return nil
	})
	return findings, walkErr
}

// jsonEnvelopeFindings checks a standalone JSON artifact: its envelope revision
// must equal the revision RECOMPUTED over its own (possibly filtered) model. This
// is both the roadmap-4.2 filtered-JSON exception (json carries the filtered
// revision, not the full-project one) and the envelope's own integrity law
// (revision == hash(embedded bytes), verified by rev.Parse).
func jsonEnvelopeFindings(out config.OutputConfig[config.AbsolutePath], schema *model.Schema) ([]string, error) {
	path := string(out.Path)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	env, parseErr := rev.Parse(data)
	if parseErr != nil {
		return []string{fmt.Sprintf("%s %s: JSON envelope integrity failure: %v (stale — run `pgdesign build`)", revisionMismatchPrefix, path, parseErr)}, nil
	}
	filtered := applyOutputFilters(schema, out.Groups, out.Source)
	want, err := rev.Compute(filtered, rev.RegistryPresent)
	if err != nil {
		return nil, err
	}
	eq, eqErr := env.Revision.Equal(want)
	if eqErr != nil {
		// Cross-class — both are registry_present here, so this is defensive.
		return []string{fmt.Sprintf("%s %s: envelope revision class mismatch: %v", revisionMismatchPrefix, path, eqErr)}, nil
	}
	if !eq {
		return []string{fmt.Sprintf("%s %s: envelope revision %s, recomputed %s (run `pgdesign build`)", revisionMismatchPrefix, path, env.Revision, want)}, nil
	}
	return nil, nil
}

// chainConsistencyFindings runs the store<->chain consistency checker over a
// chain-mode migrations project (roadmap 5.2, invoked here per 6.2). A non-chain
// project yields no findings. The returned strings carry a [chain] prefix so they
// read distinctly from the stamp-extractor's [revision-mismatch] lines.
func chainConsistencyFindings(root string, cfg *config.ResolvedConfig) []string {
	dir := resolveMigrationsDir(nil, string(cfg.Project.MigrationsDir))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if !migrate.IsChainMode(dir) {
		return nil // no chain to check
	}
	p, err := migrate.OpenChainProject(dir)
	if err != nil {
		return []string{fmt.Sprintf("[chain] cannot open chain project %q: %v", dir, err)}
	}
	if err := migrate.VerifyChainConsistency(p); err != nil {
		return []string{fmt.Sprintf("[chain] %v", err)}
	}
	return nil
}

// checkRevision is the roadmap-6.2 revision check (error severity), a sibling of
// the build-freshness check. It combines the three signals: (a) chain/store
// integrity via migrate.VerifyChainConsistency, (b) cross-artifact stamp
// agreement (every comment-stamped [output] carries the full-project revision),
// and (c) standalone JSON envelopes (envelope revision == recomputed). Findings
// are reported with distinct prefixes so the stamp-extractor and byte-compare
// signals never blur.
func checkRevision(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	configPath, found := config.FindConfig(root)
	if !found {
		return r.Skipped("no pgdesign.toml found")
	}
	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		r.Error(fmt.Sprintf("cannot load config: %v", err))
		return r.Found("config loading failed")
	}
	if len(cfg.Output) == 0 {
		return r.Skipped("no [output] section in pgdesign.toml")
	}

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		r.Error(fmt.Sprintf("cannot resolve schema paths: %v", err))
		return r.Found("schema resolution failed")
	}
	schema, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		r.Error("schema parse/build failed")
		return r.Found("schema parse/build failed")
	}
	if _, pgErr := requireSchemaPGVersion(schema); pgErr != nil {
		return r.Skipped(pgErr.Error())
	}

	fullRev, err := rev.Compute(schema, rev.RegistryPresent)
	if err != nil {
		r.Error(fmt.Sprintf("compute project revision: %v", err))
		return r.Found("revision computation failed")
	}

	problems := 0

	// (b) + (c): cross-artifact stamp agreement and standalone JSON envelopes.
	names := make([]string, 0, len(cfg.Output))
	for name := range cfg.Output {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		findings, ferr := artifactRevisionFindings(cfg.Output[name], schema, fullRev.String())
		if ferr != nil {
			r.Error(fmt.Sprintf("output %q: %v", name, ferr))
			problems++
			continue
		}
		for _, f := range findings {
			r.Error(f)
			problems++
		}
	}

	// (a) chain/store integrity.
	for _, f := range chainConsistencyFindings(root, cfg) {
		r.Error(f)
		problems++
	}

	if problems == 0 {
		return r.Passed("all artifacts carry the current revision " + fullRev.String())
	}
	return r.Found(fmt.Sprintf("%d artifact(s) at a stale or inconsistent revision", problems))
}

// partialWriteStaleSiblings implements the roadmap-6.2 PARTIAL-WRITER refusal for
// `codegen --output`. Before writing one artifact, it verifies every OTHER
// [output] sibling already carries the current full-project revision; it returns
// the stale-sibling findings (empty when writing is safe). Without a project
// config there are no siblings and it returns nil. It reuses the SAME per-output
// machinery (artifactRevisionFindings, applyOutputFilters) the revision check
// uses, so refusal and check never disagree.
func partialWriteStaleSiblings(configOverride *string, schemaPaths []string, outputPath string, schema *model.Schema) ([]string, error) {
	if len(schemaPaths) == 0 {
		return nil, nil
	}
	startDir := schemaPaths[0]
	if info, statErr := os.Stat(startDir); statErr == nil && !info.IsDir() {
		startDir = filepath.Dir(startDir)
	}
	configPath, found, err := resolveConfigPath(configOverride, startDir)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // no project config -> no siblings
	}
	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		// A malformed config must not block codegen; the sibling check simply
		// cannot run. build/check surface the config error loudly elsewhere.
		return nil, nil
	}
	fullRev, err := rev.Compute(schema, rev.RegistryPresent)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.Output))
	for name := range cfg.Output {
		names = append(names, name)
	}
	sort.Strings(names)

	var stale []string
	for _, name := range names {
		out := cfg.Output[name]
		if samePath(string(out.Path), outputPath) {
			continue // the artifact being (re)written is not its own sibling
		}
		findings, ferr := artifactRevisionFindings(out, schema, fullRev.String())
		if ferr != nil {
			return nil, ferr
		}
		stale = append(stale, findings...)
	}
	return stale, nil
}
