package main

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"
)

func strptr(s string) *string { return &s }

// TestResolveMigrationsDir pins the Default(nil)+was-set resolution used at all
// nine migrations-dir sites (8 migrate subcommands + serve). The load-bearing
// case is the ambiguity fix: an explicit "--dir migrations" must be honored
// verbatim and NOT be overridden by the project config's migrations_dir. The
// old sentinel logic (dir == "migrations" && cfg != "") could not tell an
// explicit "migrations" from the default, so config silently won.
func TestResolveMigrationsDir(t *testing.T) {
	testenv.Isolate(t)
	cases := []struct {
		name    string
		dirFlag *string
		cfgDir  string
		want    string
	}{
		{"unset flag, config set -> config", nil, "custom_migrations", "custom_migrations"},
		{"unset flag, no config -> default", nil, "", "migrations"},
		{"explicit migrations, config set -> explicit wins", strptr("migrations"), "custom_migrations", "migrations"},
		{"explicit custom, config set -> explicit wins", strptr("db/mig"), "custom_migrations", "db/mig"},
		{"explicit custom, no config -> explicit", strptr("db/mig"), "", "db/mig"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMigrationsDir(tc.dirFlag, tc.cfgDir)
			if got != tc.want {
				t.Errorf("resolveMigrationsDir(%v, %q) = %q, want %q", tc.dirFlag, tc.cfgDir, got, tc.want)
			}
		})
	}
}
