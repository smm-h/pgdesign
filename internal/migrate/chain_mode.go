package migrate

// Chain-mode detection + bridge guards (roadmap 5.2, item 6).
//
// A migrations/ directory is in CHAIN MODE once it holds a migrations/chain/
// directory (the on-disk chain edges; store_layout.md). Legacy (semver-TOML)
// projects never have it. squash (5.3) and rollback (5.6) are now reworked for
// the chain format — the CLI dispatches a chain-mode project to their chain
// implementations (SquashChain / RollbackChain). The remaining bridge guard is
// baseline (5.10): against a chain-mode project it HARD-ERRORS cleanly naming its
// subphase rather than misbehaving on files it does not understand. The guarded
// subcommands keep working unchanged for legacy-mode projects (a pre-upgrade
// database, which has no chain/ dir, is mediated here too since they take --db).

import (
	"fmt"
	"os"
	"path/filepath"
)

// IsChainMode reports whether migrationsDir is an on-disk chain project (it holds
// a chain/ subdirectory). This is the file-vs-chain mode discriminator (item 6):
// legacy semver-TOML projects have no chain/ dir.
func IsChainMode(migrationsDir string) bool {
	info, err := os.Stat(filepath.Join(migrationsDir, chainEdgesDir))
	return err == nil && info.IsDir()
}

// guardChainMode returns a hard error when migrationsDir is a chain-mode project,
// naming the subcommand and the subphase that reworks it. subcommands that are
// not yet chain-aware call it first so they never operate on chain files.
func guardChainMode(migrationsDir, subcommand, subphase string) error {
	if IsChainMode(migrationsDir) {
		return fmt.Errorf("migrate %s: this is a chain-format project (%s/%s exists) and %s is not yet reworked for the chain format — lands in %s",
			subcommand, migrationsDir, chainEdgesDir, subcommand, subphase)
	}
	return nil
}
