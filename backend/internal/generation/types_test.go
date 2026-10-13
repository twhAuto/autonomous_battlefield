// Checks generation configuration defaults and validation, and input normalization.

package generation

import (
	"errors"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"
)

func TestInputAndConfiguration(t *testing.T) {
	{
		err := DefaultConfig().Validate()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		err := (Config{}).Validate()
		testutil.Require(t, err != nil, func() { t.Fatal("invalid config accepted") })
	}
	for _, input := range []Input{{}, {Name: "a", Doctrine: " "}, {Name: "a", Doctrine: strings.Repeat("😀", 4001)}, {Name: "a", Doctrine: "d", Background: strings.Repeat("😀", 1001)}} {
		{
			_, err := input.Normalized()
			testutil.Require(t, errors.Is(err, ErrInvalid), func() { t.Fatal(err) })
		}
	}
	for _, bg := range []string{"", strings.Repeat("😀", 1000)} {
		got, err := (Input{Name: " a ", Doctrine: " d ", Background: " " + bg + " "}).Normalized()
		testutil.Require(t, !(err != nil || got.Name != "a" || got.Doctrine != "d" || got.Background != bg), func() { t.Fatal(got, err) })
	}
}
