package migrate

import (
	"os"
	"testing"

	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestMain boots one ephemeral PostgreSQL cluster for this test binary and
// exports its base URL under PGDESIGN_DB, so the database-backed tests in this
// package have a server of their own. There is no fallback: on a machine
// without the PostgreSQL binaries the variable stays unset and those tests skip
// rather than reaching for whatever server happens to be listening locally.
func TestMain(m *testing.M) {
	os.Exit(testdb.RunWithCluster(func() int { return m.Run() }))
}
