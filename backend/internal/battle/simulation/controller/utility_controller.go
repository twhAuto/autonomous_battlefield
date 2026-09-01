package controller

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// UtilityController ranks engine-provided candidates using a current strategy
// plan and data-driven trait modifiers. Its state is safe to access from a tick
// worker and an asynchronous strategy worker concurrently.
type UtilityController struct {
	mu sync.Mutex

	config          Config
	actionKinds     map[string]struct{}
	traitModifiers  map[string]int64
	fallback        PlanProposal
	plans           []StrategyPlan
	pending         map[string]DecisionRequest
	expired         map[string]Tick
	lastRequestTick Tick
	hasRequested    bool
	lastTick        Tick
	hasTicked       bool
}

func NewUtilityController(config Config) (*UtilityController, error) {
	actionKinds, err := validateConfig(config)
	if err != nil {
		return nil, err
	}

	traitModifiers := make(map[string]int64)
	for _, trait := range config.Traits {
		for kind, modifier := range trait.ActionWeightModifiers {
			traitModifiers[kind] += modifier
			if traitModifiers[kind] < -maxWeight || traitModifiers[kind] > maxWeight {
				return nil, fmt.Errorf("%w: combined trait modifier for %q is outside [%d, %d]", ErrInvalidConfig, kind, -maxWeight, maxWeight)
			}
		}
	}

	return &UtilityController{
		config:         cloneConfig(config),
		actionKinds:    actionKinds,
		traitModifiers: traitModifiers,
		fallback:       cloneProposal(config.FallbackPlan),
		pending:        make(map[string]DecisionRequest),
		expired:        make(map[string]Tick),
	}, nil
}

func (c *UtilityController) Tick(observation Observation) (TickResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateObservation(observation); err != nil {
		return TickResult{}, err
	}
	if c.hasTicked && observation.Tick < c.lastTick {
		return TickResult{}, fmt.Errorf("%w: tick moved backwards from %d to %d", ErrInvalidObservation, c.lastTick, observation.Tick)
	}
	c.lastTick = observation.Tick
	c.hasTicked = true
	c.pruneExpired(observation.Tick)

	result := TickResult{}
	if c.decisionDue(observation.Tick) {
		if !c.hasRequested || c.lastRequestTick != observation.Tick {
			request := c.newDecisionRequest(observation)
			c.pending[request.ID] = request
			c.lastRequestTick = observation.Tick
			c.hasRequested = true
			requestCopy := cloneRequest(request)
			result.DecisionRequest = &requestCopy
		}
	}

	proposal, planID, usingFallback := c.planForTick(observation.Tick)
	result.PlanID = planID
	result.UsedFallback = usingFallback
	result.Selection = c.selectCandidate(observation, proposal)
	return result, nil
}

func (c *UtilityController) SubmitPlan(requestID string, proposal PlanProposal) (StrategyPlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	request, ok := c.pending[requestID]
	if !ok {
		if expiredAt, expired := c.expired[requestID]; expired {
			return StrategyPlan{}, fmt.Errorf("%w: %q expired at tick %d", ErrRequestExpired, requestID, expiredAt)
		}
		return StrategyPlan{}, fmt.Errorf("%w: %q", ErrRequestNotFound, requestID)
	}
	if c.hasTicked && c.lastTick >= request.SubmitBeforeTick {
		delete(c.pending, requestID)
		return StrategyPlan{}, fmt.Errorf("%w: %q expired at tick %d", ErrRequestExpired, requestID, request.SubmitBeforeTick)
	}
	if err := validateProposal(proposal, c.actionKinds); err != nil {
		return StrategyPlan{}, err
	}

	plan := StrategyPlan{
		ID:             request.ID,
		Name:           strings.TrimSpace(proposal.Name),
		Rationale:      strings.TrimSpace(proposal.Rationale),
		ObservedTick:   request.Observation.Tick,
		ValidFromTick:  request.ApplyAtTick,
		ValidUntilTick: request.PlanValidUntilTick,
		ActionWeights:  cloneMap(proposal.ActionWeights),
		TargetBonuses:  cloneMap(proposal.TargetBonuses),
		ReserveGold:    proposal.ReserveGold, SpendOrder: append([]string(nil), proposal.SpendOrder...),
		ConstructionPriorities: append([]Coordinate(nil), proposal.ConstructionPriorities...),
		RecruitmentPriorities:  append([]string(nil), proposal.RecruitmentPriorities...),
	}
	c.plans = append(c.plans, plan)
	sort.Slice(c.plans, func(i, j int) bool {
		if c.plans[i].ValidFromTick != c.plans[j].ValidFromTick {
			return c.plans[i].ValidFromTick < c.plans[j].ValidFromTick
		}
		return c.plans[i].ID < c.plans[j].ID
	})
	delete(c.pending, requestID)

	return clonePlan(plan), nil
}

func (c *UtilityController) decisionDue(tick Tick) bool {
	return tick >= c.config.FirstDecisionTick &&
		(tick-c.config.FirstDecisionTick)%c.config.DecisionIntervalTicks == 0
}

func (c *UtilityController) newDecisionRequest(observation Observation) DecisionRequest {
	applyAt := observation.Tick + c.config.DecisionLeadTicks
	return DecisionRequest{
		ID:                 fmt.Sprintf("%s:%s:%d", observation.BattlefieldID, observation.FactionID, observation.Tick),
		Observation:        cloneObservation(observation),
		SubmitBeforeTick:   applyAt,
		ApplyAtTick:        applyAt,
		PlanValidUntilTick: applyAt + c.config.DecisionIntervalTicks,
	}
}

func (c *UtilityController) planForTick(tick Tick) (PlanProposal, string, bool) {
	for i := len(c.plans) - 1; i >= 0; i-- {
		plan := c.plans[i]
		if plan.ValidFromTick <= tick && tick < plan.ValidUntilTick {
			return PlanProposal{
				Name: plan.Name, Rationale: plan.Rationale,
				ActionWeights: plan.ActionWeights, TargetBonuses: plan.TargetBonuses,
				ReserveGold: plan.ReserveGold, SpendOrder: append([]string(nil), plan.SpendOrder...),
				ConstructionPriorities: append([]Coordinate(nil), plan.ConstructionPriorities...),
				RecruitmentPriorities:  append([]string(nil), plan.RecruitmentPriorities...),
			}, plan.ID, false
		}
	}
	return c.fallback, "fallback", true
}

func (c *UtilityController) selectCandidate(observation Observation, proposal PlanProposal) *Selection {
	var selected *Selection
	for _, candidate := range observation.Candidates {
		weight := c.config.DefaultActionWeight
		if configured, ok := proposal.ActionWeights[candidate.Kind]; ok {
			weight = configured
		}
		weight = clamp(weight+c.traitModifiers[candidate.Kind], 0, maxWeight)
		score := candidate.BaseUtility*weight + proposal.TargetBonuses[candidate.TargetID]

		if selected == nil || score > selected.Score ||
			(score == selected.Score && candidate.ID < selected.CandidateID) {
			selected = &Selection{
				CandidateID:     candidate.ID,
				ExpectedVersion: observation.Version,
				Score:           score,
			}
		}
	}
	return selected
}

func (c *UtilityController) validateObservation(observation Observation) error {
	if strings.TrimSpace(observation.BattlefieldID) == "" || strings.TrimSpace(observation.FactionID) == "" {
		return fmt.Errorf("%w: battlefield and faction IDs are required", ErrInvalidObservation)
	}
	seen := make(map[string]struct{}, len(observation.Candidates))
	for _, candidate := range observation.Candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			return fmt.Errorf("%w: candidate ID is required", ErrInvalidObservation)
		}
		if _, duplicate := seen[candidate.ID]; duplicate {
			return fmt.Errorf("%w: duplicate candidate ID %q", ErrInvalidObservation, candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		if _, ok := c.actionKinds[candidate.Kind]; !ok {
			return fmt.Errorf("%w: unsupported action kind %q", ErrInvalidObservation, candidate.Kind)
		}
		if candidate.BaseUtility < 0 || candidate.BaseUtility > maxBaseUtility {
			return fmt.Errorf("%w: base utility for %q is outside [0, %d]", ErrInvalidObservation, candidate.ID, maxBaseUtility)
		}
	}
	return nil
}

func (c *UtilityController) pruneExpired(tick Tick) {
	plans := c.plans[:0]
	for _, plan := range c.plans {
		if tick < plan.ValidUntilTick {
			plans = append(plans, plan)
		}
	}
	c.plans = plans

	for id, request := range c.pending {
		if tick >= request.SubmitBeforeTick {
			delete(c.pending, id)
			c.expired[id] = request.SubmitBeforeTick
		}
	}
	for id, expiredAt := range c.expired {
		if tick >= expiredAt+c.config.DecisionIntervalTicks {
			delete(c.expired, id)
		}
	}
}

func validateConfig(config Config) (map[string]struct{}, error) {
	if err := validateDecisionSchedule(config); err != nil {
		return nil, err
	}
	actionKinds, err := validateActionKinds(config.ActionKinds)
	if err != nil {
		return nil, err
	}
	if err := validateProposal(config.FallbackPlan, actionKinds); err != nil {
		return nil, fmt.Errorf("%w: fallback plan: %v", ErrInvalidConfig, err)
	}
	if err := validateTraits(config.Traits, actionKinds); err != nil {
		return nil, err
	}
	return actionKinds, nil
}

func validateDecisionSchedule(config Config) error {
	if config.DecisionIntervalTicks == 0 {
		return fmt.Errorf("%w: decision interval must be positive", ErrInvalidConfig)
	}
	if config.DecisionLeadTicks == 0 {
		return fmt.Errorf("%w: decision lead must be positive", ErrInvalidConfig)
	}
	if config.DecisionLeadTicks >= config.DecisionIntervalTicks {
		return fmt.Errorf("%w: decision lead must be less than the interval", ErrInvalidConfig)
	}
	if config.DefaultActionWeight <= 0 || config.DefaultActionWeight > maxWeight {
		return fmt.Errorf("%w: default action weight must be in [1, %d]", ErrInvalidConfig, maxWeight)
	}
	return nil
}

func validateActionKinds(kinds []string) (map[string]struct{}, error) {
	actionKinds := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		if strings.TrimSpace(kind) == "" {
			return nil, fmt.Errorf("%w: action kinds cannot be empty", ErrInvalidConfig)
		}
		if _, duplicate := actionKinds[kind]; duplicate {
			return nil, fmt.Errorf("%w: duplicate action kind %q", ErrInvalidConfig, kind)
		}
		actionKinds[kind] = struct{}{}
	}
	if len(actionKinds) == 0 {
		return nil, fmt.Errorf("%w: at least one action kind is required", ErrInvalidConfig)
	}
	return actionKinds, nil
}

func validateTraits(traits []Trait, actionKinds map[string]struct{}) error {
	traitIDs := make(map[string]struct{}, len(traits))
	for _, trait := range traits {
		if strings.TrimSpace(trait.ID) == "" {
			return fmt.Errorf("%w: trait ID is required", ErrInvalidConfig)
		}
		if _, duplicate := traitIDs[trait.ID]; duplicate {
			return fmt.Errorf("%w: duplicate trait ID %q", ErrInvalidConfig, trait.ID)
		}
		traitIDs[trait.ID] = struct{}{}
		for kind, modifier := range trait.ActionWeightModifiers {
			if _, ok := actionKinds[kind]; !ok {
				return fmt.Errorf("%w: trait %q refers to unsupported action kind %q", ErrInvalidConfig, trait.ID, kind)
			}
			if modifier < -maxWeight || modifier > maxWeight {
				return fmt.Errorf("%w: trait modifier for %q is outside [%d, %d]", ErrInvalidConfig, kind, -maxWeight, maxWeight)
			}
		}
	}
	return nil
}

func validateProposal(proposal PlanProposal, actionKinds map[string]struct{}) error {
	if strings.TrimSpace(proposal.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidPlan)
	}
	if utf8.RuneCountInString(strings.TrimSpace(proposal.Rationale)) > maxRationaleCharacters {
		return fmt.Errorf("%w: rationale exceeds %d characters", ErrInvalidPlan, maxRationaleCharacters)
	}
	for kind, weight := range proposal.ActionWeights {
		if _, ok := actionKinds[kind]; !ok {
			return fmt.Errorf("%w: unsupported action kind %q", ErrInvalidPlan, kind)
		}
		if weight < 0 || weight > maxWeight {
			return fmt.Errorf("%w: action weight for %q is outside [0, %d]", ErrInvalidPlan, kind, maxWeight)
		}
	}
	for target, bonus := range proposal.TargetBonuses {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("%w: target bonus key is required", ErrInvalidPlan)
		}
		if bonus < -maxTargetBonus || bonus > maxTargetBonus {
			return fmt.Errorf("%w: target bonus for %q is outside [%d, %d]", ErrInvalidPlan, target, -maxTargetBonus, maxTargetBonus)
		}
	}
	if proposal.ReserveGold < 0 {
		return fmt.Errorf("%w: reserve gold cannot be negative", ErrInvalidPlan)
	}
	seenSpend := make(map[string]bool, len(proposal.SpendOrder))
	for _, kind := range proposal.SpendOrder {
		if kind != "construction" && kind != "recruitment" {
			return fmt.Errorf("%w: unsupported spend kind %q", ErrInvalidPlan, kind)
		}
		if seenSpend[kind] {
			return fmt.Errorf("%w: duplicate spend kind %q", ErrInvalidPlan, kind)
		}
		seenSpend[kind] = true
	}
	seenRecruitment := make(map[string]bool, len(proposal.RecruitmentPriorities))
	for _, unitType := range proposal.RecruitmentPriorities {
		if strings.TrimSpace(unitType) == "" || seenRecruitment[unitType] {
			return fmt.Errorf("%w: recruitment priorities must be unique and non-empty", ErrInvalidPlan)
		}
		seenRecruitment[unitType] = true
	}
	seenSites := make(map[Coordinate]bool, len(proposal.ConstructionPriorities))
	for _, site := range proposal.ConstructionPriorities {
		if seenSites[site] {
			return fmt.Errorf("%w: construction priorities must be unique", ErrInvalidPlan)
		}
		seenSites[site] = true
	}
	return nil
}

// ValidatePlanProposal validates a data-driven plan against the action kinds
// supported by a ruleset. It lets scenario loaders reject invalid plans before
// a battle is queued.
func ValidatePlanProposal(proposal PlanProposal, actionKinds []string) error {
	allowed := make(map[string]struct{}, len(actionKinds))
	for _, kind := range actionKinds {
		if strings.TrimSpace(kind) == "" {
			return fmt.Errorf("%w: action kinds cannot be empty", ErrInvalidPlan)
		}
		if _, duplicate := allowed[kind]; duplicate {
			return fmt.Errorf("%w: duplicate action kind %q", ErrInvalidPlan, kind)
		}
		allowed[kind] = struct{}{}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("%w: at least one action kind is required", ErrInvalidPlan)
	}
	return validateProposal(proposal, allowed)
}

func cloneConfig(config Config) Config {
	copy := config
	copy.ActionKinds = append([]string(nil), config.ActionKinds...)
	copy.FallbackPlan = cloneProposal(config.FallbackPlan)
	copy.Traits = make([]Trait, len(config.Traits))
	for i, trait := range config.Traits {
		copy.Traits[i] = Trait{ID: trait.ID, ActionWeightModifiers: cloneMap(trait.ActionWeightModifiers)}
	}
	return copy
}

func cloneProposal(proposal PlanProposal) PlanProposal {
	return PlanProposal{
		Name: proposal.Name, Rationale: proposal.Rationale, ActionWeights: cloneMap(proposal.ActionWeights), TargetBonuses: cloneMap(proposal.TargetBonuses),
		ReserveGold: proposal.ReserveGold, SpendOrder: append([]string(nil), proposal.SpendOrder...),
		ConstructionPriorities: append([]Coordinate(nil), proposal.ConstructionPriorities...),
		RecruitmentPriorities:  append([]string(nil), proposal.RecruitmentPriorities...),
	}
}

func clonePlan(plan StrategyPlan) StrategyPlan {
	plan.ActionWeights = cloneMap(plan.ActionWeights)
	plan.TargetBonuses = cloneMap(plan.TargetBonuses)
	plan.SpendOrder = append([]string(nil), plan.SpendOrder...)
	plan.ConstructionPriorities = append([]Coordinate(nil), plan.ConstructionPriorities...)
	plan.RecruitmentPriorities = append([]string(nil), plan.RecruitmentPriorities...)
	return plan
}

func cloneObservation(observation Observation) Observation {
	observation.Candidates = append([]Candidate(nil), observation.Candidates...)
	return observation
}

func cloneRequest(request DecisionRequest) DecisionRequest {
	request.Observation = cloneObservation(request.Observation)
	return request
}

func cloneMap(values map[string]int64) map[string]int64 {
	if values == nil {
		return nil
	}
	copy := make(map[string]int64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func clamp(value, minimum, maximum int64) int64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
