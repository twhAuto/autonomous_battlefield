// Checks generation configuration defaults and validation, and input normalization.

package generation

import (
	"errors"
	"strings"
	"testing"
)

func TestInputAndConfiguration(t *testing.T) {
	{
		err := DefaultConfig().Validate()
		if err != nil {
			t.Fatal(err)
		}
	}
	{
		err := (Config{}).Validate()
		if err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	for _, input := range []Input{{}, {Name: "a", Doctrine: " "}, {Name: "a", Doctrine: strings.Repeat("😀", 4001)}, {Name: "a", Doctrine: "d", Background: strings.Repeat("😀", 1001)}} {
		{
			_, err := input.Normalized()
			if !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		}
	}
	for _, bg := range []string{"", strings.Repeat("😀", 1000)} {
		got, err := (Input{Name: " a ", Doctrine: " d ", Background: " " + bg + " "}).Normalized()
		if err != nil || got.Name != "a" || got.Doctrine != "d" || got.Background != bg {
			t.Fatal(got, err)
		}
	}
}
