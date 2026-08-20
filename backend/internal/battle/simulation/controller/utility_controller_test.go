package controller

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewUtilityControllerRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "zero decision interval",
			mutate: func(config *Config) {
				config.DecisionIntervalTicks = 0
			},
		},
		{
			name: "decision lead reaches next interval",
			mutate: func(config *Config) {
				config.DecisionLeadTicks = config.DecisionIntervalTicks
			},
		},
		{
			name: "zero decision lead",
			mutate: func(config *Config) {
				config.DecisionLeadTicks = 0
			},
		},
		{
			name: "no action kinds",
			mutate: func(config *Config) {
				config.ActionKinds = nil
			},
		},
		{
			name: "fallback uses unknown action kind",
			mutate: func(config *Config) {
				config.FallbackPlan.ActionWeights["unknown"] = 1000
			},
		},
		{
			name: "trait uses unknown action kind",
			mutate: func(config *Config) {
				config.Traits = []Trait{{
					ID:                    "invalid",
					ActionWeightModifiers: map[string]int64{"unknown": 100},
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := testConfig()
			tt.mutate(&config)

			_, err := NewUtilityController(config)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewUtilityController() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestUtilityControllerSchedulesAndAppliesPlans(t *testing.T) {
	controller, err := NewUtilityController(testConfig())
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}

	observation := testObservation(0)
	result, err := controller.Tick(observation)
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	if result.DecisionRequest == nil {
		t.Fatal("Tick() did not request a strategy decision at the configured first tick")
	}
	request := *result.DecisionRequest
	if request.SubmitBeforeTick != 2 || request.ApplyAtTick != 2 || request.PlanValidUntilTick != 12 {
		t.Fatalf("decision deadline/window = %d/[%d, %d), want 2/[2, 12)", request.SubmitBeforeTick, request.ApplyAtTick, request.PlanValidUntilTick)
	}
	assertSelection(t, result, "defend", true)

	plan, err := controller.SubmitPlan(request.ID, PlanProposal{
		Name: "aggressive",
		ActionWeights: map[string]int64{
			"attack": 2000,
			"defend": 500,
		},
	})
	if err != nil {
		t.Fatalf("SubmitPlan() returned an error: %v", err)
	}
	if plan.ValidFromTick != 2 || plan.ValidUntilTick != 12 {
		t.Fatalf("accepted plan window = [%d, %d), want [2, 12)", plan.ValidFromTick, plan.ValidUntilTick)
	}

	result, err = controller.Tick(testObservation(1))
	if err != nil {
		t.Fatalf("Tick(1) returned an error: %v", err)
	}
	assertSelection(t, result, "defend", true)

	result, err = controller.Tick(testObservation(2))
	if err != nil {
		t.Fatalf("Tick(2) returned an error: %v", err)
	}
	assertSelection(t, result, "attack", false)
	if result.PlanID != request.ID {
		t.Fatalf("plan ID = %q, want %q", result.PlanID, request.ID)
	}

	result, err = controller.Tick(testObservation(10))
	if err != nil {
		t.Fatalf("Tick(10) returned an error: %v", err)
	}
	if result.DecisionRequest == nil {
		t.Fatal("Tick() did not request the next decision at the configured interval")
	}
	assertSelection(t, result, "attack", false)

	result, err = controller.Tick(testObservation(12))
	if err != nil {
		t.Fatalf("Tick(12) returned an error: %v", err)
	}
	assertSelection(t, result, "defend", true)
}

func TestUtilityControllerEmitsOneRequestPerDecisionTick(t *testing.T) {
	controller, err := NewUtilityController(testConfig())
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}

	first, err := controller.Tick(testObservation(0))
	if err != nil {
		t.Fatalf("first Tick() returned an error: %v", err)
	}
	second, err := controller.Tick(testObservation(0))
	if err != nil {
		t.Fatalf("second Tick() returned an error: %v", err)
	}
	if first.DecisionRequest == nil || second.DecisionRequest != nil {
		t.Fatalf("decision requests = (%v, %v), want one request", first.DecisionRequest, second.DecisionRequest)
	}
}

func TestUtilityControllerUsesDataDrivenTraits(t *testing.T) {
	config := testConfig()
	config.FallbackPlan = PlanProposal{Name: "balanced"}
	config.Traits = []Trait{{
		ID:                    "reckless-custom-trait",
		ActionWeightModifiers: map[string]int64{"attack": 1000},
	}}
	controller, err := NewUtilityController(config)
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}

	observation := testObservation(1)
	observation.Candidates = []Candidate{
		{ID: "attack", Kind: "attack", TargetID: "north", BaseUtility: 60},
		{ID: "defend", Kind: "defend", TargetID: "home", BaseUtility: 100},
	}
	result, err := controller.Tick(observation)
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	assertSelection(t, result, "attack", true)
}

func TestUtilityControllerUsesTargetBonuses(t *testing.T) {
	controller, err := NewUtilityController(testConfig())
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}
	result, err := controller.Tick(testObservation(0))
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	_, err = controller.SubmitPlan(result.DecisionRequest.ID, PlanProposal{
		Name:          "take the north",
		ActionWeights: map[string]int64{"attack": 1000, "defend": 1000},
		TargetBonuses: map[string]int64{"north": 50_000},
	})
	if err != nil {
		t.Fatalf("SubmitPlan() returned an error: %v", err)
	}

	result, err = controller.Tick(testObservation(2))
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	assertSelection(t, result, "attack", false)
}

func TestUtilityControllerBreaksTiesDeterministically(t *testing.T) {
	config := testConfig()
	config.FirstDecisionTick = 10
	config.FallbackPlan = PlanProposal{Name: "balanced"}

	want := "action-a"
	orders := [][]Candidate{
		{
			{ID: "action-b", Kind: "attack", BaseUtility: 100},
			{ID: "action-a", Kind: "defend", BaseUtility: 100},
		},
		{
			{ID: "action-a", Kind: "defend", BaseUtility: 100},
			{ID: "action-b", Kind: "attack", BaseUtility: 100},
		},
	}

	for _, candidates := range orders {
		controller, err := NewUtilityController(config)
		if err != nil {
			t.Fatalf("NewUtilityController() returned an error: %v", err)
		}
		observation := testObservation(1)
		observation.Candidates = candidates
		result, err := controller.Tick(observation)
		if err != nil {
			t.Fatalf("Tick() returned an error: %v", err)
		}
		if result.Selection == nil || result.Selection.CandidateID != want {
			t.Fatalf("selection = %#v, want candidate %q", result.Selection, want)
		}
	}
}

func TestUtilityControllerRejectsInvalidAndExpiredPlans(t *testing.T) {
	controller, err := NewUtilityController(testConfig())
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}
	result, err := controller.Tick(testObservation(0))
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	requestID := result.DecisionRequest.ID

	_, err = controller.SubmitPlan(requestID, PlanProposal{
		Name:          "invalid",
		ActionWeights: map[string]int64{"teleport": 1000},
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("SubmitPlan() error = %v, want ErrInvalidPlan", err)
	}

	if _, err := controller.Tick(testObservation(2)); err != nil {
		t.Fatalf("Tick(2) returned an error: %v", err)
	}
	_, err = controller.SubmitPlan(requestID, PlanProposal{Name: "too late"})
	if !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("SubmitPlan() error = %v, want ErrRequestExpired", err)
	}
}

func TestUtilityControllerValidatesObservations(t *testing.T) {
	controller, err := NewUtilityController(testConfig())
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}

	tests := []struct {
		name        string
		observation Observation
	}{
		{name: "missing battlefield", observation: Observation{FactionID: "faction-1"}},
		{name: "unknown action kind", observation: Observation{
			BattlefieldID: "battlefield-1",
			FactionID:     "faction-1",
			Candidates:    []Candidate{{ID: "action-1", Kind: "teleport", BaseUtility: 1}},
		}},
		{name: "duplicate candidate", observation: Observation{
			BattlefieldID: "battlefield-1",
			FactionID:     "faction-1",
			Candidates: []Candidate{
				{ID: "action-1", Kind: "attack", BaseUtility: 1},
				{ID: "action-1", Kind: "defend", BaseUtility: 1},
			},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := controller.Tick(tt.observation)
			if !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("Tick() error = %v, want ErrInvalidObservation", err)
			}
		})
	}
}

func TestUtilityControllerCopiesConfigAndAcceptedPlans(t *testing.T) {
	config := testConfig()
	controller, err := NewUtilityController(config)
	if err != nil {
		t.Fatalf("NewUtilityController() returned an error: %v", err)
	}
	config.FallbackPlan.ActionWeights["attack"] = 10_000

	result, err := controller.Tick(testObservation(0))
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	assertSelection(t, result, "defend", true)

	proposal := PlanProposal{Name: "aggressive", ActionWeights: map[string]int64{"attack": 2000}}
	plan, err := controller.SubmitPlan(result.DecisionRequest.ID, proposal)
	if err != nil {
		t.Fatalf("SubmitPlan() returned an error: %v", err)
	}
	proposal.ActionWeights["attack"] = 0
	plan.ActionWeights["attack"] = 0

	result, err = controller.Tick(testObservation(2))
	if err != nil {
		t.Fatalf("Tick() returned an error: %v", err)
	}
	assertSelection(t, result, "attack", false)
}

func testConfig() Config {
	return Config{
		DecisionIntervalTicks: 10,
		DecisionLeadTicks:     2,
		FirstDecisionTick:     0,
		DefaultActionWeight:   1000,
		ActionKinds:           []string{"attack", "defend", "expand", "gather"},
		FallbackPlan: PlanProposal{
			Name: "defensive fallback",
			ActionWeights: map[string]int64{
				"attack": 500,
				"defend": 1500,
			},
		},
	}
}

func testObservation(tick Tick) Observation {
	return Observation{
		BattlefieldID: "battlefield-1",
		FactionID:     "faction-1",
		Tick:          tick,
		Version:       uint64(tick) + 20,
		Candidates: []Candidate{
			{ID: "attack", Kind: "attack", TargetID: "north", BaseUtility: 100},
			{ID: "defend", Kind: "defend", TargetID: "home", BaseUtility: 80},
		},
	}
}

func assertSelection(t *testing.T, result TickResult, candidateID string, fallback bool) {
	t.Helper()
	if result.Selection == nil {
		t.Fatal("selection is nil")
	}
	if result.Selection.CandidateID != candidateID {
		t.Fatalf("selected candidate = %q, want %q", result.Selection.CandidateID, candidateID)
	}
	if result.UsedFallback != fallback {
		t.Fatalf("UsedFallback = %t, want %t", result.UsedFallback, fallback)
	}
}

func TestCloneObservation(t *testing.T) {
	original := testObservation(3)
	copy := cloneObservation(original)
	copy.Candidates[0].ID = "changed"
	if reflect.DeepEqual(original, copy) {
		t.Fatal("cloneObservation() shares candidate storage with its input")
	}
}
