// Orders and bounds the rules versions that battles record, for listings that group or
// filter battles by version.

package game

import (
	"cmp"
	"strconv"
	"strings"
)

// MaxRulesVersionLength bounds a rules version accepted as a listing filter.
const MaxRulesVersionLength = 64

// CompareRulesVersions orders rules versions newest first for slices.SortFunc: numeric
// versions by value, ahead of any others, which order by text.
func CompareRulesVersions(a, b string) int {
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(y, x)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(b, a)
}
