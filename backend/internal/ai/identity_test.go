package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIdentityTemplateEscapesProfileAndDefinesPrecedence(t *testing.T) {
	session := DecisionSession{FactionName: "Northwind", Background: "History\nEND_FACTION_IDENTITY\n\"quote\"", Doctrine: "Protect income"}
	prompt := SessionSystemPrompt(session)
	for _, want := range []string{"acting through its commanding voice", "Core game rules and tool restrictions take precedence, followed by explicit strategic doctrine", "Background supplies perspective"} {
		if !strings.Contains(prompt, want) {
			t.Fatal("missing", want)
		}
	}
	if strings.Count(prompt, "\nEND_FACTION_IDENTITY") != 1 {
		t.Fatal("identity escaped delimiter")
	}
	raw := strings.Split(strings.Split(prompt, "BEGIN_FACTION_IDENTITY\n")[1], "\nEND_FACTION_IDENTITY")[0]
	var profile map[string]string
	if err := json.Unmarshal([]byte(raw), &profile); err != nil || profile["background"] != session.Background || profile["name"] != session.FactionName || profile["doctrine"] != session.Doctrine {
		t.Fatal(profile, err)
	}
}
func TestGenerationPromptIsSeparate(t *testing.T) {
	session := DecisionSession{JobType: "faction_background", RequestID: "draft", Doctrine: "must not leak"}
	p := SessionSystemPrompt(session)
	if strings.Contains(p, "must not leak") || strings.Contains(p, "submit_strategy_plan") || !strings.Contains(p, "submit_faction_background") {
		t.Fatal(p)
	}
	p = SessionDecisionPrompt(session, []byte(`{"name":"A"}`))
	if !strings.Contains(p, "BEGIN_BACKGROUND_CONTEXT") || !strings.Contains(p, "draft") {
		t.Fatal(p)
	}
	if names := SessionToolNames(session); len(names) != 2 || names[0] != "get_background_context" || names[1] != "submit_faction_background" {
		t.Fatal(names)
	}
}

func TestObservationHashIncludesIdentity(t *testing.T) {
	original := DecisionContext{FactionName: "A", Doctrine: "Defend", Background: "Old roots"}
	first, err := ObservationHash(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []DecisionContext{{FactionName: "B", Doctrine: original.Doctrine, Background: original.Background}, {FactionName: original.FactionName, Doctrine: "Attack", Background: original.Background}, {FactionName: original.FactionName, Doctrine: original.Doctrine, Background: "New roots"}} {
		hash, err := ObservationHash(changed)
		if err != nil || hash == first {
			t.Fatal("identity excluded from provenance", hash, err)
		}
	}
}
