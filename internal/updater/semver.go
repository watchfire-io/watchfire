package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Semver represents a semantic version.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// ParseSemver parses a version string like "1.2.3" or "v1.2.3".
func ParseSemver(s string) (Semver, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("invalid semver: %q", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid major version: %w", err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid minor version: %w", err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid patch version: %w", err)
	}

	return Semver{Major: major, Minor: minor, Patch: patch}, nil
}

// String returns the version as "major.minor.patch".
func (v Semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// LessThan returns true if v < other.
func (v Semver) LessThan(other Semver) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}
