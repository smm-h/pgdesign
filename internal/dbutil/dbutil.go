package dbutil

import (
	"fmt"
	"net/url"
)

// SwapDatabase rewrites a PostgreSQL connection URL to target a different database.
// Example: SwapDatabase("postgres://localhost:5432/myapp", "otherdb") returns "postgres://localhost:5432/otherdb"
func SwapDatabase(dbURL, newDB string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", fmt.Errorf("parse connection URL: %w", err)
	}
	u.Path = "/" + newDB
	return u.String(), nil
}

// MaintenanceURL rewrites a PostgreSQL connection URL to target the 'postgres' maintenance database.
func MaintenanceURL(dbURL string) (string, error) {
	return SwapDatabase(dbURL, "postgres")
}

// ResolveURL returns the first non-empty string from the arguments.
// Intended usage: ResolveURL(flagValue, configValue, envVarValue)
func ResolveURL(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
