// Checks every authored map plays a full scripted battle to a decision.
package catalog

import (
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Chokepoints and forward deposits can let both sides hold a front for the whole
// battle, so scripted bots must break through on every map from either side
// before the shared tick limit.
func TestScriptedBotsFinishEveryMap(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range c.IDs() {
		for _, sides := range [][2]string{{"a", "b"}, {"b", "a"}} {
			t.Run(id+"/"+sides[0]+"-left", func(t *testing.T) {
				t.Parallel()
				last := playScriptedBattle(t, c, id, sides)
				if last.Result.WinnerFactionID == nil {
					t.Fatalf("scripted bots stalemated at tick %d", last.Tick)
				}
				t.Logf("winner=%s ticks=%d", *last.Result.WinnerFactionID, last.Tick)
			})
		}
	}
}

// playScriptedBattle runs map id to the end with factions sides[0] on the left and
// sides[1] on the right, and returns the final transition.
func playScriptedBattle(t *testing.T, c *Catalog, id string, sides [2]string) game.Transition {
	t.Helper()
	s, err := c.Resolve(id, [2]service.FactionIdentity{{ID: sides[0], Name: sides[0]}, {ID: sides[1], Name: sides[1]}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := game.NewMatch("scripted-"+id, s, s.MaximumTicks)
	if err != nil {
		t.Fatal(err)
	}
	var last game.Transition
	for !m.Finished() {
		if last, err = m.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	return last
}
