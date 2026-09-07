// Checks decision authority and schema validation, repository errors, capability
// integrity, and malformed-token fuzzing.

package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type memoryRepository struct {
	record      DecisionRecord
	submitted   PlanSubmission
	result      SubmissionResult
	err         error
	validations int
}

func (r *memoryRepository) CreateOrReuseDecision(context.Context, CreateDecisionParams) (DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) GetDecision(context.Context, string) (DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) ListWindowDecisions(context.Context, string, int, uint64) ([]DecisionRecord, error) {
	return []DecisionRecord{r.record}, r.err
}
func (r *memoryRepository) SubmitDecisionPlan(_ context.Context, value PlanSubmission) (SubmissionResult, error) {
	r.submitted = value
	return r.result, r.err
}
func (r *memoryRepository) ResolveDecisionFallback(context.Context, string, int64, controller.PlanProposal, string, string) (bool, error) {
	return true, r.err
}
func (r *memoryRepository) ClaimModelDecision(context.Context, string, []string, time.Duration) (DecisionRecord, error) {
	return r.record, r.err
}
func (r *memoryRepository) FinishDecisionFailure(context.Context, string, string, Usage, map[string]any) error {
	return r.err
}
func (r *memoryRepository) RecordDecisionUsage(context.Context, string, Usage, map[string]any) error {
	return r.err
}
func (r *memoryRepository) RecordDecisionValidation(context.Context, string) error {
	r.validations++
	return r.err
}

func TestDecisionServiceValidatesAndSubmits(t *testing.T) {
	repository := &memoryRepository{record: validRecord(), result: SubmissionResult{Accepted: true, Status: "accepted"}}
	service := NewDecisionService(repository)
	contextValue, err := service.GetContext(context.Background(), "request")
	if err != nil || contextValue.RequestID != "request" {
		t.Fatalf("GetContext() = %#v, %v", contextValue, err)
	}
	submission := PlanSubmission{RequestID: "request", IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 10, Plan: controller.PlanProposal{Name: "advance", ActionWeights: map[string]int64{"attack": 1200}, SpendOrder: []string{"recruitment"}, RecruitmentPriorities: []string{"vanguard"}}}
	result, err := service.SubmitPlan(context.Background(), submission)
	if err != nil || !result.Accepted || repository.submitted.IdempotencyKey != "once" {
		t.Fatalf("SubmitPlan() = %#v, %v", result, err)
	}
}

func TestDecisionValidationRejectsAuthorityAndSchemaViolations(t *testing.T) {
	record := validRecord()
	bad := PlanSubmission{RequestID: "request", ExpectedVersion: 2, TargetTick: 11, Plan: controller.PlanProposal{Name: "", RecruitmentPriorities: []string{"dragon"}, ConstructionPriorities: []controller.Coordinate{{X: 99, Y: 99}}}}
	issues := ValidateSubmission(record, bad)
	if len(issues) < 5 {
		t.Fatalf("issues = %#v", issues)
	}
	repository := &memoryRepository{record: record}
	result, err := NewDecisionService(repository).SubmitPlan(context.Background(), bad)
	if err != nil || result.Accepted || repository.submitted.RequestID != "" || repository.validations != 1 {
		t.Fatalf("result = %#v, err=%v", result, err)
	}
}

func TestCapabilityAndObservationHash(t *testing.T) {
	expires := time.Now().Add(time.Minute)
	token, err := MintCapability("secret", "request", expires)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := VerifyCapability("secret", token, time.Now()); err != nil || got != "request" {
		t.Fatalf("VerifyCapability=%q,%v", got, err)
	}
	for _, test := range []struct {
		secret, token string
		now           time.Time
	}{{"bad", token, time.Now()}, {"secret", "bad", time.Now()}, {"secret", token, expires.Add(time.Second)}} {
		if _, err := VerifyCapability(test.secret, test.token, test.now); err == nil {
			t.Fatal("invalid capability accepted")
		}
	}
	first := validRecord().Observation
	second := first
	second.RequestID = "other"
	second.ObservationHash = "ignored"
	a, _ := ObservationHash(first)
	b, _ := ObservationHash(second)
	if a != b || a == "" {
		t.Fatalf("hashes %q %q", a, b)
	}
	if _, err := MintCapability("", "request", expires); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func FuzzVerifyCapability(f *testing.F) {
	valid, err := MintCapability("secret", "request", time.Unix(2_000_000_000, 0))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add("not-a-capability")
	f.Fuzz(func(t *testing.T, token string) {
		requestID, err := VerifyCapability("secret", token, time.Unix(1_900_000_000, 0))
		if err == nil && requestID == "" {
			t.Fatal("accepted capability has an empty request ID")
		}
	})
}

func TestDecisionServicePropagatesRepositoryFailure(t *testing.T) {
	want := errors.New("database")
	service := NewDecisionService(&memoryRepository{err: want})
	if _, err := service.GetContext(context.Background(), "request"); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func validRecord() DecisionRecord {
	return DecisionRecord{ID: "request", Version: 1, TargetTick: 10, Observation: DecisionContext{RequestID: "request", TargetTick: 10, ExpectedVersion: 1, Legal: LegalChoices{ActionKinds: []string{"attack", "defend"}, Units: []game.UnitType{{ID: "vanguard"}}, EconomySites: []controller.Coordinate{{X: 2, Y: 2}}}}}
}

type boundedCLIRepository struct {
	memoryRepository
	failed string
}

func (r *boundedCLIRepository) FinishDecisionFailure(_ context.Context, _ string, category string, _ Usage, _ map[string]any) error {
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
	submission := PlanSubmission{RequestID: "request", IdempotencyKey: "once", ExpectedVersion: record.Version, TargetTick: record.TargetTick, Plan: controller.PlanProposal{Name: "Strategy", Rationale: "Test target tick."}}
	issues := ValidateSubmission(record, submission)
	if len(issues) != 1 || issues[0].Code != "empty_decision" {
		t.Fatalf("placeholder accepted: %+v", issues)
	}
	submission.Plan.ActionWeights = map[string]int64{"defend": 1000}
	if issues = ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("explicit defensive decision rejected: %+v", issues)
	}
	submission.TargetTick = 999
	record.ProviderMetadata = map[string]any{"validationFailures": float64(2)}
	repository := &boundedCLIRepository{memoryRepository: memoryRepository{record: record}}
	result, err := NewDecisionService(repository).SubmitPlan(context.Background(), submission)
	if err != nil || result.Status != "failed" || repository.failed != "validation_limit" {
		t.Fatalf("guessing not bounded: %+v %v", result, err)
	}
	repository.record.Status = "failed"
	result, err = NewDecisionService(repository).SubmitPlan(context.Background(), submission)
	if err != nil || result.Errors[0].Code != "terminal" || repository.validations != 1 {
		t.Fatal("terminal decision revalidated")
	}
}

func (r *memoryRepository) ListFactionMemory(context.Context, string, int, string, uint64) ([]FactionMemoryEntry, error) {
	return nil, nil
}
