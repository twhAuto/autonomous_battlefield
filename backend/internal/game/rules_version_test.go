package game

import (
	"slices"
	"testing"
)

func TestCompareRulesVersionsOrdersNewestFirst(t *testing.T) {
	versions := []string{"9", "beta", "13", "alpha", "10", "2"}
	slices.SortFunc(versions, CompareRulesVersions)
	if want := []string{"13", "10", "9", "2", "beta", "alpha"}; !slices.Equal(versions, want) {
		t.Fatalf("sorted = %v, want %v", versions, want)
	}
	if CompareRulesVersions("13", "13") != 0 {
		t.Fatal("equal versions should compare equal")
	}
}
