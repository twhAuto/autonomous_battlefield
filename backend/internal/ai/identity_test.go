package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
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

func TestGenerationPromptVersionCoversBothInstructions(t *testing.T) {
	session := DecisionSession{JobType: "faction_background", RequestID: "draft-%s"}
	want := "Generate background for request draft-%s. First call get_background_context, then submit_faction_background."
	if got := SessionDecisionPrompt(session, nil); got != want {
		t.Fatalf("request prompt = %q", got)
	}
	if got := SessionDecisionPrompt(session, []byte(`{"name":"A"}`)); got != want+"\nBEGIN_BACKGROUND_CONTEXT\n{\"name\":\"A\"}\nEND_BACKGROUND_CONTEXT" {
		t.Fatalf("inline prompt = %q", got)
	}
	system := SessionSystemPrompt(session)
	if !strings.Contains(system, fmt.Sprintf("between 1 and %d Unicode characters", faction.MaxBackgroundLength)) {
		t.Fatal("prompt does not use the enforced background limit")
	}
	if GenerationPromptVersion != generationPromptVersion(system, backgroundDecisionTemplate) {
		t.Fatal("published version does not describe the embedded instructions")
	}
	for _, version := range []string{
		generationPromptVersion(system+" changed", backgroundDecisionTemplate),
		generationPromptVersion(system, backgroundDecisionTemplate+" changed"),
	} {
		if version == GenerationPromptVersion {
			t.Fatal("instruction change did not affect provenance")
		}
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
