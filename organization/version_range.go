package organization

import (
	"errors"
	"strings"
)

var ErrInvalidVersionRange = errors.New("invalid role version range")

// VersionRange supports exact numeric triples, trailing wildcards (2.*,
// 2.1.*), and a single Maven-style interval. It deliberately does not adopt
// Maven's qualifier ordering or soft-version requirements. Signed bundles
// currently use numeric major.minor.patch versions only.
type VersionRange struct {
	lower, upper                   string
	lowerInclusive, upperInclusive bool
}

func ParseVersionRange(value string) (VersionRange, error) {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return VersionRange{}, ErrInvalidVersionRange
	}
	if versionPattern.MatchString(value) {
		return VersionRange{value, value, true, true}, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) >= 2 && len(parts) <= 3 && parts[len(parts)-1] == "*" {
		lowerParts := append([]string(nil), parts[:len(parts)-1]...)
		for len(lowerParts) < 3 {
			lowerParts = append(lowerParts, "0")
		}
		lower := strings.Join(lowerParts, ".")
		if !versionPattern.MatchString(lower) {
			return VersionRange{}, ErrInvalidVersionRange
		}
		upperParts := append([]string(nil), lowerParts...)
		upperParts[len(parts)-2] = incrementVersionComponent(upperParts[len(parts)-2])
		return VersionRange{lower, strings.Join(upperParts, "."), true, false}, nil
	}
	if len(value) < 3 {
		return VersionRange{}, ErrInvalidVersionRange
	}
	first, last := value[0], value[len(value)-1]
	if (first != '[' && first != '(') || (last != ']' && last != ')') {
		return VersionRange{}, ErrInvalidVersionRange
	}
	body := value[1 : len(value)-1]
	if !strings.Contains(body, ",") && first == '[' && last == ']' && versionPattern.MatchString(body) {
		return VersionRange{body, body, true, true}, nil
	}
	bounds := strings.Split(body, ",")
	if len(bounds) != 2 || bounds[0] == "" && bounds[1] == "" || bounds[0] != "" && !versionPattern.MatchString(bounds[0]) || bounds[1] != "" && !versionPattern.MatchString(bounds[1]) || bounds[0] == "" && first != '(' || bounds[1] == "" && last != ')' {
		return VersionRange{}, ErrInvalidVersionRange
	}
	r := VersionRange{bounds[0], bounds[1], first == '[', last == ']'}
	if r.lower != "" && r.upper != "" {
		c := compareSemanticVersions(r.lower, r.upper)
		if c > 0 || c == 0 && (!r.lowerInclusive || !r.upperInclusive) {
			return VersionRange{}, ErrInvalidVersionRange
		}
	}
	return r, nil
}

func (r VersionRange) Contains(version string) bool {
	if !versionPattern.MatchString(version) || r.lower == "" && r.upper == "" {
		return false
	}
	if r.lower != "" {
		c := compareSemanticVersions(version, r.lower)
		if c < 0 || c == 0 && !r.lowerInclusive {
			return false
		}
	}
	if r.upper != "" {
		c := compareSemanticVersions(version, r.upper)
		if c > 0 || c == 0 && !r.upperInclusive {
			return false
		}
	}
	return true
}

// Decimal text comparison avoids overflow for every numeric version already
// accepted by the signed-bundle validator, rather than silently treating a
// ParseUint overflow as a different release.
func compareSemanticVersions(left, right string) int {
	l, r := strings.Split(left, "."), strings.Split(right, ".")
	for i := range 3 {
		a, b := strings.TrimLeft(l[i], "0"), strings.TrimLeft(r[i], "0")
		if len(a) < len(b) {
			return -1
		}
		if len(a) > len(b) {
			return 1
		}
		if c := strings.Compare(a, b); c != 0 {
			return c
		}
	}
	return 0
}

// CompareRoleVersions compares the numeric triples used by signed bundles.
func CompareRoleVersions(left, right string) (int, error) {
	if !versionPattern.MatchString(left) || !versionPattern.MatchString(right) {
		return 0, ErrInvalidVersionRange
	}
	return compareSemanticVersions(left, right), nil
}

func incrementVersionComponent(component string) string {
	b := []byte(component)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}
