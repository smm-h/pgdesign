package migrate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// NextSemverVersion derives the next legacy-mode migration version from the
// semver *.toml files already in dir: the maximum existing version with its patch
// bumped, or "0.1.0" when the directory has none. This is the TRANSITIONAL
// auto-derivation for legacy-mode `migrate generate` after the --version flag was
// removed (roadmap 5.9 makes chain-mode identity content-derived; legacy-mode
// generate before `migrate upgrade` still needs a semver filename).
func NextSemverVersion(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "0.1.0", nil
		}
		return "", fmt.Errorf("migrate: read migrations dir: %w", err)
	}
	max := ""
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		v := strings.TrimSuffix(e.Name(), ".toml")
		if _, _, _, err := semverParts(v); err != nil {
			continue
		}
		if max == "" || compareSemver(v, max) > 0 {
			max = v
		}
	}
	if max == "" {
		return "0.1.0", nil
	}
	maj, min, patch, err := semverParts(max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", maj, min, patch+1), nil
}

// semverParts splits a semver string into major, minor, patch ints.
// Returns an error if the format is not "X.Y.Z".
func semverParts(v string) (int, int, int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid semver: %q", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid semver major: %q", v)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid semver minor: %q", v)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid semver patch: %q", v)
	}
	return major, minor, patch, nil
}

// compareSemver returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareSemver(a, b string) int {
	aMaj, aMin, aPat, aErr := semverParts(a)
	bMaj, bMin, bPat, bErr := semverParts(b)

	// Invalid versions sort last.
	if aErr != nil && bErr != nil {
		return strings.Compare(a, b)
	}
	if aErr != nil {
		return 1
	}
	if bErr != nil {
		return -1
	}

	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	if aPat != bPat {
		if aPat < bPat {
			return -1
		}
		return 1
	}
	return 0
}

// InSemverRange returns true if version is in the [from, to] range (inclusive).
func InSemverRange(version, from, to string) bool {
	return compareSemver(version, from) >= 0 && compareSemver(version, to) <= 0
}
