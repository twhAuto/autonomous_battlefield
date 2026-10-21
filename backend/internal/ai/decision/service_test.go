// Checks decision submission, the validation limit, repository errors, and observation
// hashing.

package decision

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type memoryRepository struct {
	record      ai.DecisionRecord
	submitted   ai.PlanSubmission
	result      ai.SubmissionResult
	err         error
	validations int
}

type timingRepository struct {
	*memoryRepository
	timingError error
	recordedID  string
}

func (r *timingRepository) RecordDecisionContextServed(_ context.Context, id string) error {
	r.recordedID = id
	return r.timingError
}

func TestContextRemainsAvailableWhenTimingWriteFails(t *testing.T) {
	repository := &timingRepository{memoryRepository: &memoryRepository{record: validRecord()}, timingError: errors.New("timing write failed")}
	value, err := NewService(repository).GetContext(context.Background(), "request")
	if err != nil || value.RequestID != "request" || repository.recordedID != "request" {
		t.Fatalf("context = %+v, err = %v, recorded = %q", value, err, repository.recordedID)
	}
}

func (r *memoryRepository) CreateOrReuseDecision(context.Context, ai.CreateDecisionParams) (ai.DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) GetDecision(context.Context, string) (ai.DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) ListWindowDecisions(context.Context, string, int, uint64) ([]ai.DecisionRecord, error) {
	return []ai.DecisionRecord{r.record}, r.err
}
func (r *memoryRepository) SubmitDecisionPlan(_ context.Context, value ai.PlanSubmission) (ai.SubmissionResult, error) {
	r.submitted = value
	return r.result, r.err
}
func (r *memoryRepository) ResolveDecisionFallback(context.Context, string, int64, controller.PlanProposal, string, string) (bool, error) {
	return true, r.err
}
func (r *memoryRepository) ClaimModelDecision(context.Context, string, []string, time.Duration) (ai.DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error {
	return r.err
}
func (r *memoryRepository) RecordDecisionUsage(context.Context, string, ai.Usage, map[string]any) error {
	return r.err
}
func (r *memoryRepository) RecordDecisionValidation(context.Context, string) (int, error) {
	r.validations++
	return r.validations, r.err
}

func TestDecisionServiceValidatesAndSubmits(t *testing.T) {
	repository := &memoryRepository{record: validRecord(), result: ai.SubmissionResult{Accepted: true, Status: "accepted"}}
	service := NewService(repository)
	contextValue, err := service.GetContext(context.Background(), "request")
	if err != nil || contextValue.RequestID != "request" {
		t.Fatalf("GetContext() = %#v, %v", contextValue, err)
	}
	submission := ai.PlanSubmission{RequestID: "request", IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 10, Plan: controller.PlanProposal{Name: "advance", ActionWeights: map[string]int64{"attack": 1200}, SpendOrder: []string{"recruitment"}, RecruitmentPriorities: []string{"vanguard"}}}
	result, err := service.SubmitPlan(context.Background(), submission)
	if err != nil || !result.Accepted || repository.submitted.IdempotencyKey != "once" {
		t.Fatalf("SubmitPlan() = %#v, %v", result, err)
	}
}

func TestDecisionValidationRejectsAuthorityAndSchemaViolations(t *testing.T) {
	record := validRecord()
	bad := ai.PlanSubmission{RequestID: "request", ExpectedVersion: 2, TargetTick: 11, Plan: controller.PlanProposal{Name: "", RecruitmentPriorities: []string{"dragon"}, ConstructionPriorities: []controller.Coordinate{{X: 99, Y: 99}}}}
	issues := ValidateSubmission(record, bad)
	if len(issues) < 5 {
		t.Fatalf("issues = %#v", issues)
	}
	repository := &memoryRepository{record: record}
	result, err := NewService(repository).SubmitPlan(context.Background(), bad)
	if err != nil || result.Accepted || repository.submitted.RequestID != "" || repository.validations != 1 {
		t.Fatalf("result = %#v, err=%v", result, err)
	}
}

func TestObservationHashIgnoresBookkeepingFields(t *testing.T) {
	first := validRecord().Observation
	second := first
	second.RequestID = "other"
	second.ObservationHash = "ignored"
	a, _ := ObservationHash(first)
	b, _ := ObservationHash(second)
	if a != b || a == "" {
		t.Fatalf("hashes %q %q", a, b)
	}
}

func TestDecisionServicePropagatesRepositoryFailure(t *testing.T) {
	want := errors.New("database")
	service := NewService(&memoryRepository{err: want})
	if _, err := service.GetContext(context.Background(), "request"); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func validRecord() ai.DecisionRecord {
	return ai.DecisionRecord{ID: "request", Version: 1, TargetTick: 10, Observation: ai.DecisionContext{RequestID: "request", TargetTick: 10, ExpectedVersion: 1, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}, Units: []game.UnitType{{ID: "vanguard"}}, EconomySites: []controller.Coordinate{{X: 2, Y: 2}}}}}
}

type boundedCLIRepository struct {
	memoryRepository
	failed string
}

func (r *boundedCLIRepository) FinishDecisionFailure(_ context.Context, _ string, category string, _ ai.Usage, _ map[string]any) error {
	r.failed = category
	return nil
}
func TestModelRuntimesRejectPlaceholderAndStopGuessing(t *testing.T) {
	for _, runtime := range []string{"cli", "api"} {
		t.Run(runtime, func(t *testing.T) { testModelRejectsPlaceholderAndStopsGuessing(t, runtime) })
	}
}

func testModelRejectsPlaceholderAndStopsGuessing(t *testing.T, runtime string) {
	record := validRecord()
	record.Runtime = runtime
	record.Status = "running"
	submission := ai.PlanSubmission{RequestID: "request", IdempotencyKey: "once", ExpectedVersion: record.Version, TargetTick: record.TargetTick, Plan: controller.PlanProposal{Name: "Strategy", Rationale: "Test target tick."}}
	issues := ValidateSubmission(record, submission)
	if len(issues) != 1 || issues[0].Code != "empty_decision" {
		t.Fatalf("placeholder accepted: %+v", issues)
	}
	submission.Plan.ActionWeights = map[string]int64{"defend": 1000}
	if issues = ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("explicit defensive decision rejected: %+v", issues)
	}
	submission.TargetTick = 999
	repository := &boundedCLIRepository{memoryRepository: memoryRepository{record: record, validations: 2}}
	result, err := NewService(repository).SubmitPlan(context.Background(), submission)
	if err != nil || result.Status != "failed" || repository.failed != "validation_limit" {
		t.Fatalf("guessing not bounded: %+v %v", result, err)
	}
	repository.record.Status = "failed"
	result, err = NewService(repository).SubmitPlan(context.Background(), submission)
	if err != nil || result.Errors[0].Code != "terminal" || repository.validations != 3 {
		t.Fatal("terminal decision revalidated")
	}
}

func (r *memoryRepository) ListFactionMemory(context.Context, string, int, string, uint64) ([]ai.FactionMemoryEntry, error) {
	return nil, nil
}
