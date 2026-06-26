package main

import (
	"strings"
	"testing"
)

func TestResolvePGVersion(t *testing.T) {
	tests := []struct {
		name   string
		live   int
		config int
		toml   int
		want   int
	}{
		{"live_wins", 17, 15, 14, 17},
		{"config_wins_when_no_live", 0, 15, 14, 15},
		{"toml_wins_when_no_live_or_config", 0, 0, 14, 14},
		{"all_zero", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePGVersion(tt.live, tt.config, tt.toml)
			if got != tt.want {
				t.Errorf("resolvePGVersion(%d, %d, %d) = %d, want %d",
					tt.live, tt.config, tt.toml, got, tt.want)
			}
		})
	}
}

func TestRequirePGVersion(t *testing.T) {
	t.Run("returns_resolved_version_live", func(t *testing.T) {
		got, err := requirePGVersion(17, 15, 14)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 17 {
			t.Errorf("requirePGVersion(17, 15, 14) = %d, want 17", got)
		}
	})

	t.Run("returns_resolved_version_config", func(t *testing.T) {
		got, err := requirePGVersion(0, 15, 14)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 15 {
			t.Errorf("requirePGVersion(0, 15, 14) = %d, want 15", got)
		}
	})

	t.Run("returns_resolved_version_toml", func(t *testing.T) {
		got, err := requirePGVersion(0, 0, 14)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 14 {
			t.Errorf("requirePGVersion(0, 0, 14) = %d, want 14", got)
		}
	})

	t.Run("error_when_all_zero", func(t *testing.T) {
		got, err := requirePGVersion(0, 0, 0)
		if err == nil {
			t.Fatalf("expected error, got version %d", got)
		}
		if got != 0 {
			t.Errorf("expected 0 on error, got %d", got)
		}
		if !strings.Contains(err.Error(), "pg_version") {
			t.Errorf("error should mention pg_version, got: %s", err.Error())
		}
		if !strings.Contains(err.Error(), "pgdesign.toml") {
			t.Errorf("error should mention pgdesign.toml, got: %s", err.Error())
		}
	})
}
