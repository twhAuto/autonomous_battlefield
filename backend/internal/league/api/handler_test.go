// Checks the league routes: request parsing, response shape, and the status each
// service error maps to.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
	leagueservice "github.com/twh09/autonomous_battlefield/backend/internal/league/service"
)

const testLeagueID = "9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e10"

type harness struct {
	repo    *memoryRepo
	battles *fakeBattles
	mux     *http.ServeMux
}

func newHarness() *harness {
	h := &harness{repo: &memoryRepo{leagues: map[string]*league.League{}}, battles: &fakeBattles{}, mux: http.NewServeMux()}
	NewHandler(leagueservice.New(h.repo, h.battles, fakeFactions{}, leagueservice.Options{BattleSlots: 4})).RegisterRoutes(h.mux)
	return h
}

func (h *harness) do(role authn.Role, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(authn.WithActor(request.Context(), authn.Actor{ID: "admin-1", Role: role}))
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	return recorder
}

func createBody(mutate func(map[string]any)) string {
	entrants := []map[string]any{}
	for i := range 8 {
		entrants = append(entrants, map[string]any{"factionId": fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "group": string(rune('A' + i%2)), "controller": map[string]any{"type": "scripted"}})
	}
	body := map[string]any{"id": testLeagueID, "name": "Autumn cup", "scenarioIds": []string{"frontier-siege"}, "tickCount": 500, "entrants": entrants}
	if mutate != nil {
		mutate(body)
	}
	data, _ := json.Marshal(body)
	return string(data)
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return body
}

// createdHarness creates the test league through the route.
func createdHarness(t *testing.T) (*harness, map[string]any) {
	t.Helper()
	h := newHarness()
	created := h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
	if created.Code != http.StatusCreated || created.Header().Get("Location") != "/v1/leagues/"+testLeagueID {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	return h, decode(t, created)
}

func TestCreateLeagueRouteReturnsTheSchedule(t *testing.T) {
	_, body := createdHarness(t)
	groups, fixtures, entrants := body["groups"].([]any), body["fixtures"].([]any), body["entrants"].([]any)
	if body["status"] != "running" || len(groups) != 2 || len(fixtures) != 4 || len(entrants) != 8 || len(body["knockout"].([]any)) != 0 {
		t.Fatalf("league = %v", body)
	}
	if progress := body["progress"].(map[string]any); progress["settled"] != 0.0 || progress["total"] != 13.0 {
		t.Fatalf("progress = %v", progress)
	}
	if entrant := entrants[1].(map[string]any); entrant["group"] != "B" || entrant["factionName"] != "Faction" {
		t.Fatalf("entrant = %v", entrant)
	}
	fixture := fixtures[1].(map[string]any)
	if _, hasLeg := fixture["leg"]; hasLeg || fixture["group"] != "A" || fixture["match"] != "opening" || fixture["round"] != 1.0 || fixture["tie"] != 2.0 ||
		fixture["status"] != "pending" || fixture["battleId"] != nil || fixture["result"] != nil || fixture["winner"] != nil {
		t.Fatalf("fixture = %v", fixture)
	}
	standing := groups[0].(map[string]any)["standings"].([]any)[0].(map[string]any)
	if len(groups[0].(map[string]any)["standings"].([]any)) != 4 || standing["placement"] != nil || standing["won"] != 0.0 || standing["lost"] != 0.0 {
		t.Fatalf("standing = %v", standing)
	}
}

func TestGetLeagueRouteShowsFixtureResults(t *testing.T) {
	h, _ := createdHarness(t)
	stored := h.repo.leagues[testLeagueID]
	f := &stored.Fixtures[0]
	battleID, winner := "11111111-1111-4111-8111-111111111111", stored.Entrants[f.Home].FactionID
	f.Status, f.BattleID = league.FixtureCompleted, &battleID
	f.Battle = &league.BattleOutcome{Status: league.BattleCompleted, WinnerFactionID: &winner, Reason: "castle_destroyed"}
	// A void fixture has no result, but its match goes to the earlier entrant.
	void := &stored.Fixtures[1]
	void.Status = league.FixtureVoid
	read := h.do(authn.Tester, http.MethodGet, "/v1/leagues/"+testLeagueID, "")
	body := decode(t, read)
	fixtures := body["fixtures"].([]any)
	completed, voided := fixtures[0].(map[string]any), fixtures[1].(map[string]any)
	result := completed["result"].(map[string]any)
	if read.Code != http.StatusOK || result["winner"] != float64(f.Home) || result["reason"] != "castle_destroyed" || completed["winner"] != float64(f.Home) {
		t.Fatalf("read = %d, fixture %v", read.Code, completed)
	}
	if voided["result"] != nil || voided["winner"] != float64(min(void.Home, void.Away)) {
		t.Fatalf("void fixture = %v", voided)
	}
	standings := body["groups"].([]any)[0].(map[string]any)["standings"].([]any)
	if first := standings[0].(map[string]any); first["won"] != 1.0 || first["placement"] != nil {
		t.Fatalf("standings = %v", standings)
	}
}

func TestGetLeagueRouteShowsRunningBattleProgress(t *testing.T) {
	h, _ := createdHarness(t)
	stored := h.repo.leagues[testLeagueID]
	battleID := "11111111-1111-4111-8111-111111111111"
	stored.Fixtures[0].Status, stored.Fixtures[0].BattleID = league.FixtureRunning, &battleID
	stored.Fixtures[0].Battle = &league.BattleOutcome{RulesVersion: "13", Status: "running", CurrentTick: 120, RequestedTicks: 500}
	read := h.do(authn.Tester, http.MethodGet, "/v1/leagues/"+testLeagueID, "")
	body := decode(t, read)
	if versions := body["rulesVersions"].([]any); len(versions) != 1 || versions[0] != "13" {
		t.Fatalf("rules versions = %v", versions)
	}
	fixtures := body["fixtures"].([]any)
	progress, ok := fixtures[0].(map[string]any)["battleProgress"].(map[string]any)
	if read.Code != http.StatusOK || !ok || progress["currentTick"] != 120.0 || progress["requestedTicks"] != 500.0 {
		t.Fatalf("read = %d, fixture %v", read.Code, fixtures[0])
	}
	if _, pending := fixtures[1].(map[string]any)["battleProgress"]; pending {
		t.Fatalf("pending fixture = %v", fixtures[1])
	}
}

func TestListLeaguesRouteShowsChampions(t *testing.T) {
	h, _ := createdHarness(t)
	champion := 3
	stored := h.repo.leagues[testLeagueID]
	stored.Status, stored.Champion = league.StatusCompleted, &champion
	list := h.do(authn.Tester, http.MethodGet, "/v1/leagues?limit=5&rulesVersion=13", "")
	body := decode(t, list)
	item := body["leagues"].([]any)[0].(map[string]any)
	if list.Code != http.StatusOK || item["entrantCount"] != 8.0 || item["champion"].(map[string]any)["factionName"] != "Faction" || h.repo.listQuery != (leagueservice.ListQuery{Limit: 5, RulesVersion: "13"}) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	if versions, ok := item["rulesVersions"].([]any); !ok || len(versions) != 0 {
		t.Fatalf("league without battles lists versions %v", item["rulesVersions"])
	}
	if scheduling := body["scheduling"].(map[string]any); scheduling["battleSlots"] != 4.0 || scheduling["cliSlots"] != 3.0 {
		t.Fatalf("scheduling = %v", scheduling)
	}
}

func TestLeagueRoutesMapErrors(t *testing.T) {
	cases := []struct {
		name   string
		role   authn.Role
		method string
		path   string
		body   string
		setup  func(*harness)
		status int
		code   string
	}{
		{"malformed body", authn.Admin, http.MethodPost, "/v1/leagues", "{", nil, 400, ""},
		{"unknown group", authn.Admin, http.MethodPost, "/v1/leagues", createBody(func(b map[string]any) { b["entrants"].([]map[string]any)[0]["group"] = "E" }), nil, 400, ""},
		{"invalid league", authn.Admin, http.MethodPost, "/v1/leagues", createBody(func(b map[string]any) { b["name"] = "" }), nil, 400, ""},
		{"tester create", authn.Tester, http.MethodPost, "/v1/leagues", createBody(nil), nil, 403, ""},
		{"missing map", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.battles.err = battleservice.ErrScenarioNotFound }, 404, ""},
		{"unavailable model", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.battles.err = battleservice.ErrModelProfileUnavailable }, 422, ""},
		{"reused ID", authn.Admin, http.MethodPost, "/v1/leagues", createBody(func(b map[string]any) { b["tickCount"] = 400 }), func(h *harness) {
			h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
		}, 409, "already_exists"},
		{"storage failure", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.repo.err = errors.New("database down") }, 500, ""},
		{"bad limit", authn.Tester, http.MethodGet, "/v1/leagues?limit=zero", "", nil, 400, ""},
		{"zero limit", authn.Tester, http.MethodGet, "/v1/leagues?limit=0", "", nil, 400, ""},
		{"limit too high", authn.Tester, http.MethodGet, "/v1/leagues?limit=101", "", nil, 400, ""},
		{"service identity list", authn.CI, http.MethodGet, "/v1/leagues", "", nil, 403, ""},
		{"list failure", authn.Tester, http.MethodGet, "/v1/leagues", "", func(h *harness) { h.repo.err = errors.New("database down") }, 500, ""},
		{"slot read failure", authn.Tester, http.MethodGet, "/v1/leagues", "", func(h *harness) { h.repo.slotsErr = errors.New("database down") }, 500, ""},
		{"missing league", authn.Tester, http.MethodGet, "/v1/leagues/" + testLeagueID, "", nil, 404, ""},
		{"bad league ID", authn.Tester, http.MethodGet, "/v1/leagues/league", "", nil, 400, ""},
		{"completed cancel", authn.Admin, http.MethodPost, "/v1/leagues/" + testLeagueID + "/cancel", "", func(h *harness) {
			h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
			h.repo.leagues[testLeagueID].Status = league.StatusCompleted
		}, 409, ""},
		{"tester disqualify", authn.Tester, http.MethodPost, disqualifyPath("0"), "{}", created, 403, ""},
		{"entrant not a number", authn.Admin, http.MethodPost, disqualifyPath("first"), "{}", created, 400, ""},
		{"malformed disqualify body", authn.Admin, http.MethodPost, disqualifyPath("0"), `{"reason": 1}`, created, 400, ""},
		{"missing entrant", authn.Admin, http.MethodPost, disqualifyPath("8"), "{}", created, 404, ""},
		{"cancelled disqualify", authn.Admin, http.MethodPost, disqualifyPath("0"), "{}", func(h *harness) {
			created(h)
			h.repo.leagues[testLeagueID].Status = league.StatusCancelled
		}, 409, ""},
		{"eliminated entrant", authn.Admin, http.MethodPost, disqualifyPath("6"), "{}", func(h *harness) {
			created(h)
			// Group A is 0, 2, 4, 6: void matches go to the earlier entrant, so 6 loses
			// its opening and the losers' match.
			stored := h.repo.leagues[testLeagueID]
			stored.Fixtures[0].Status, stored.Fixtures[1].Status = league.FixtureVoid, league.FixtureVoid
			next, _ := stored.Advance()
			stored.Fixtures = append(stored.Fixtures, next...)
			stored.Fixtures[len(stored.Fixtures)-1].Status = league.FixtureVoid
		}, 409, ""},
		{"last contender", authn.Admin, http.MethodPost, disqualifyPath("0"), "{}", func(h *harness) {
			created(h)
			for i := 1; i < 8; i++ {
				h.repo.leagues[testLeagueID].Entrants[i].Disqualified = &league.Disqualification{By: "admin-1"}
			}
		}, 409, ""},
	}
	for _, test := range cases {
		h := newHarness()
		if test.setup != nil {
			test.setup(h)
		}
		response := h.do(test.role, test.method, test.path, test.body)
		if response.Code != test.status {
			t.Errorf("%s: status %d %s, want %d", test.name, response.Code, response.Body.String(), test.status)
			continue
		}
		if test.code != "" && !bytes.Contains(response.Body.Bytes(), []byte(test.code)) {
			t.Errorf("%s: body %s lacks code %s", test.name, response.Body.String(), test.code)
		}
	}
}

func disqualifyPath(entrant string) string {
	return "/v1/leagues/" + testLeagueID + "/entrants/" + entrant + "/disqualify"
}

func created(h *harness) { h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil)) }

func TestDisqualifyEntrantRoute(t *testing.T) {
	h, _ := createdHarness(t)
	// Group A's first opening is 0 v 6.
	response := h.do(authn.Admin, http.MethodPost, disqualifyPath("6"), `{"reason": "Too slow"}`)
	body := decode(t, response)
	entrant := body["entrants"].([]any)[6].(map[string]any)
	disqualification, _ := entrant["disqualification"].(map[string]any)
	if response.Code != http.StatusOK || disqualification["by"] != "admin-1" || disqualification["reason"] != "Too slow" || entrant["eliminated"] != false {
		t.Fatalf("disqualify = %d, entrant %v", response.Code, entrant)
	}
	opening := body["fixtures"].([]any)[0].(map[string]any)
	if opening["status"] != "forfeited" || (opening["forfeit"] != "home" && opening["forfeit"] != "away") || opening["winner"] != 0.0 {
		t.Fatalf("opening = %v", opening)
	}
	if other := body["entrants"].([]any)[0].(map[string]any); other["disqualification"] != nil {
		t.Fatalf("entrant 0 = %v", other)
	}
}

func TestCancelLeagueRoute(t *testing.T) {
	h := newHarness()
	h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
	response := h.do(authn.Admin, http.MethodPost, "/v1/leagues/"+testLeagueID+"/cancel", "")
	if response.Code != http.StatusOK || decode(t, response)["status"] != "cancelled" {
		t.Fatalf("cancel = %d %s", response.Code, response.Body.String())
	}
}

type memoryRepo struct {
	leagues   map[string]*league.League
	err       error
	slotsErr  error
	listQuery leagueservice.ListQuery
}

func (r *memoryRepo) CLISlots(context.Context) (int, error) { return 3, r.slotsErr }

func (r *memoryRepo) CreateLeague(_ context.Context, l league.League) error {
	if r.err != nil {
		return r.err
	}
	if _, ok := r.leagues[l.ID]; ok {
		return leagueservice.ErrLeagueAlreadyExists
	}
	stored := l
	r.leagues[l.ID] = &stored
	return nil
}

func (r *memoryRepo) GetLeague(_ context.Context, id string) (*league.League, error) {
	if l, ok := r.leagues[id]; ok {
		copied := *l
		return &copied, nil
	}
	return nil, leagueservice.ErrLeagueNotFound
}

func (r *memoryRepo) ListLeagues(_ context.Context, query leagueservice.ListQuery) ([]league.Summary, error) {
	r.listQuery = query
	if r.err != nil {
		return nil, r.err
	}
	summaries := []league.Summary{}
	for _, l := range r.leagues {
		name := "Faction"
		summaries = append(summaries, league.Summary{ID: l.ID, Name: l.Name, Status: l.Status, GroupCount: l.GroupCount, Champion: l.Champion, ChampionName: &name})
	}
	return summaries, nil
}

func (r *memoryRepo) CancelLeague(_ context.Context, id string) (bool, []string, error) {
	l := r.leagues[id]
	if l.Status == league.StatusCompleted {
		return false, nil, leagueservice.ErrLeagueNotActive
	}
	l.Status = league.StatusCancelled
	return true, nil, nil
}

func (r *memoryRepo) Schedule(context.Context, func(context.Context, leagueservice.SchedulerStore) error) (bool, error) {
	return false, nil
}

func (r *memoryRepo) Exclusive(ctx context.Context, fn func(context.Context, leagueservice.SchedulerStore) error) error {
	return fn(ctx, memoryStore{r})
}

// memoryStore serves a disqualification from the stored leagues; the reads only a
// scheduling pass makes find nothing.
type memoryStore struct{ *memoryRepo }

func (s memoryStore) League(_ context.Context, id string) (*league.League, error) {
	l, ok := s.leagues[id]
	if !ok {
		return nil, leagueservice.ErrLeagueNotFound
	}
	copied := *l
	copied.Entrants, copied.Fixtures = slices.Clone(l.Entrants), slices.Clone(l.Fixtures)
	return &copied, nil
}

func (s memoryStore) Disqualify(_ context.Context, id string, entrant int, d league.Disqualification) error {
	s.leagues[id].Entrants[entrant].Disqualified = &d
	return nil
}

func (s memoryStore) SaveFixture(_ context.Context, f league.Fixture) error {
	for _, l := range s.leagues {
		for i := range l.Fixtures {
			if l.Fixtures[i].ID == f.ID {
				l.Fixtures[i] = f
			}
		}
	}
	return nil
}

func (s memoryStore) AddFixtures(_ context.Context, id string, fixtures []league.Fixture) error {
	s.leagues[id].Fixtures = append(s.leagues[id].Fixtures, fixtures...)
	return nil
}

func (s memoryStore) CompleteLeague(_ context.Context, id string, champion int) error {
	s.leagues[id].Status, s.leagues[id].Champion = league.StatusCompleted, &champion
	return nil
}

func (memoryStore) RunningLeagues(context.Context) ([]league.League, error) { return nil, nil }
func (memoryStore) Capacity(context.Context) (leagueservice.Capacity, error) {
	return leagueservice.Capacity{}, nil
}
func (memoryStore) Battle(context.Context, string) (*leagueservice.BattleProvenance, error) {
	return nil, nil
}
func (memoryStore) StrandedBattles(context.Context) ([]leagueservice.StrandedBattle, error) {
	return nil, nil
}

type fakeBattles struct{ err error }

func (b *fakeBattles) ValidateBattle(context.Context, battleservice.CreateBattleInput) error {
	return b.err
}

func (b *fakeBattles) CreateBattle(context.Context, battleservice.CreateBattleInput) (*battleservice.BattleRun, error) {
	return nil, b.err
}

func (b *fakeBattles) StopBattle(context.Context, string) (*battleservice.BattleRun, error) {
	return nil, b.err
}

type fakeFactions struct{}

func (fakeFactions) GetFaction(_ context.Context, id string) (*faction.Faction, error) {
	return &faction.Faction{ID: id, Name: "Faction"}, nil
}
