package migrate

import (
	"fmt"
	"strconv"
	"strings"
)

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
