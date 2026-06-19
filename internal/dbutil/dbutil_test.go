package dbutil

import (
	"testing"
)

func TestSwapDatabase(t *testing.T) {
	tests := []struct {
		name    string
		dbURL   string
		newDB   string
		want    string
		wantErr bool
	}{
		{
			name:  "standard URL",
			dbURL: "postgres://user:pass@host:5432/mydb",
			newDB: "newdb",
			want:  "postgres://user:pass@host:5432/newdb",
		},
		{
			name:  "Unix socket URL",
			dbURL: "postgres:///mydb",
			newDB: "newdb",
			want:  "postgres:///newdb",
		},
		{
			name:  "query parameters preserved",
			dbURL: "postgres://user:pass@host:5432/mydb?sslmode=disable&connect_timeout=10",
			newDB: "newdb",
			want:  "postgres://user:pass@host:5432/newdb?sslmode=disable&connect_timeout=10",
		},
		{
			name:    "empty URL",
			dbURL:   "",
			newDB:   "newdb",
			want:    "/newdb",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SwapDatabase(tt.dbURL, tt.newDB)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SwapDatabase() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SwapDatabase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaintenanceURL(t *testing.T) {
	got, err := MaintenanceURL("postgres://user:pass@host:5432/mydb")
	if err != nil {
		t.Fatalf("MaintenanceURL() error = %v", err)
	}
	want := "postgres://user:pass@host:5432/postgres"
	if got != want {
		t.Errorf("MaintenanceURL() = %q, want %q", got, want)
	}
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{
			name:   "returns first non-empty from end",
			values: []string{"", "", "third"},
			want:   "third",
		},
		{
			name:   "returns first non-empty from start",
			values: []string{"first", "second", "third"},
			want:   "first",
		},
		{
			name:   "all empty returns empty",
			values: []string{"", "", ""},
			want:   "",
		},
		{
			name:   "no arguments returns empty",
			values: nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveURL(tt.values...)
			if got != tt.want {
				t.Errorf("ResolveURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
