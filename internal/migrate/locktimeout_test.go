package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"
)

func TestValidateLockTimeout(t *testing.T) {
	testenv.Isolate(t)
	valid := []struct{ in, want string }{
		{"", "5s"},
		{"5s", "5s"},
		{"100ms", "100ms"},
		{"1min", "1min"},
		{"0", "0"},
		{"  2s  ", "2s"},
	}
	for _, c := range valid {
		got, err := validateLockTimeout(c.in)
		if err != nil {
			t.Errorf("validateLockTimeout(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("validateLockTimeout(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Injection / malformed values must be rejected.
	invalid := []string{
		"5s'; DROP TABLE users; --",
		"5 seconds; SELECT 1",
		"abc",
		"'; DELETE FROM pgdesign_migrations; --",
		"5s5s",
		"-1",
	}
	for _, in := range invalid {
		if _, err := validateLockTimeout(in); err == nil {
			t.Errorf("validateLockTimeout(%q) should have errored (injection/malformed)", in)
		}
	}
}
