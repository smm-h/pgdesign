package main

import "testing"

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
