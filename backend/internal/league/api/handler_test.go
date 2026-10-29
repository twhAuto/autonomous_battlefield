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
	if body["status"] != "running" || len(groups) != 2 || len(fixtures) != 24 || len(entrants) != 8 || len(body["knockout"].([]any)) != 0 {
		t.Fatalf("league = %v", body)
	}
	if progress := body["progress"].(map[string]any); progress["settled"] != 0.0 || progress["total"] != 30.0 {
		t.Fatalf("progress = %v", progress)
	}
	if entrant := entrants[1].(map[string]any); entrant["group"] != "B" || entrant["factionName"] != "Faction" {
		t.Fatalf("entrant = %v", entrant)
	}
	if fixture := fixtures[0].(map[string]any); fixture["group"] != "A" || fixture["status"] != "pending" || fixture["battleId"] != nil || fixture["result"] != nil {
		t.Fatalf("fixture = %v", fixture)
	}
	if standings := groups[0].(map[string]any)["standings"].([]any); len(standings) != 4 || standings[0].(map[string]any)["position"] != 1.0 {
		t.Fatalf("standings = %v", standings)
	}
}

func TestGetLeagueRouteShowsFixtureResults(t *testing.T) {
	h, _ := createdHarness(t)
	stored := h.repo.leagues[testLeagueID]
	f := &stored.Fixtures[0]
	battleID, winner := "11111111-1111-4111-8111-111111111111", stored.Entrants[f.Home].FactionID
	f.Status, f.BattleID = league.FixtureCompleted, &battleID
	f.Battle = &league.BattleOutcome{Status: league.BattleCompleted, WinnerFactionID: &winner, Reason: "castle_destroyed"}
	read := h.do(authn.Developer, http.MethodGet, "/v1/leagues/"+testLeagueID, "")
	result := decode(t, read)["fixtures"].([]any)[0].(map[string]any)["result"].(map[string]any)
	if read.Code != http.StatusOK || result["winner"] != float64(f.Home) || result["reason"] != "castle_destroyed" {
		t.Fatalf("read = %d, result %v", read.Code, result)
	}
}

func TestGetLeagueRouteShowsRunningBattleProgress(t *testing.T) {
	h, _ := createdHarness(t)
	stored := h.repo.leagues[testLeagueID]
	battleID := "11111111-1111-4111-8111-111111111111"
	stored.Fixtures[0].Status, stored.Fixtures[0].BattleID = league.FixtureRunning, &battleID
	stored.Fixtures[0].Battle = &league.BattleOutcome{Status: "running", CurrentTick: 120, RequestedTicks: 500}
	read := h.do(authn.Developer, http.MethodGet, "/v1/leagues/"+testLeagueID, "")
	fixtures := decode(t, read)["fixtures"].([]any)
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
	list := h.do(authn.Developer, http.MethodGet, "/v1/leagues?limit=5", "")
	body := decode(t, list)
	item := body["leagues"].([]any)[0].(map[string]any)
	if list.Code != http.StatusOK || item["entrantCount"] != 8.0 || item["champion"].(map[string]any)["factionName"] != "Faction" || h.repo.listLimit != 5 {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
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
		{"developer create", authn.Developer, http.MethodPost, "/v1/leagues", createBody(nil), nil, 403, ""},
		{"missing map", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.battles.err = battleservice.ErrScenarioNotFound }, 404, ""},
		{"unavailable model", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.battles.err = battleservice.ErrModelProfileUnavailable }, 422, ""},
		{"reused ID", authn.Admin, http.MethodPost, "/v1/leagues", createBody(func(b map[string]any) { b["tickCount"] = 400 }), func(h *harness) {
			h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
		}, 409, "already_exists"},
		{"storage failure", authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil), func(h *harness) { h.repo.err = errors.New("database down") }, 500, ""},
		{"bad limit", authn.Developer, http.MethodGet, "/v1/leagues?limit=zero", "", nil, 400, ""},
		{"zero limit", authn.Developer, http.MethodGet, "/v1/leagues?limit=0", "", nil, 400, ""},
		{"limit too high", authn.Developer, http.MethodGet, "/v1/leagues?limit=101", "", nil, 400, ""},
		{"player list", authn.Player, http.MethodGet, "/v1/leagues", "", nil, 403, ""},
		{"list failure", authn.Developer, http.MethodGet, "/v1/leagues", "", func(h *harness) { h.repo.err = errors.New("database down") }, 500, ""},
		{"slot read failure", authn.Developer, http.MethodGet, "/v1/leagues", "", func(h *harness) { h.repo.slotsErr = errors.New("database down") }, 500, ""},
		{"missing league", authn.Developer, http.MethodGet, "/v1/leagues/" + testLeagueID, "", nil, 404, ""},
		{"bad league ID", authn.Developer, http.MethodGet, "/v1/leagues/league", "", nil, 400, ""},
		{"completed cancel", authn.Admin, http.MethodPost, "/v1/leagues/" + testLeagueID + "/cancel", "", func(h *harness) {
			h.do(authn.Admin, http.MethodPost, "/v1/leagues", createBody(nil))
			h.repo.leagues[testLeagueID].Status = league.StatusCompleted
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
	listLimit int
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

func (r *memoryRepo) ListLeagues(_ context.Context, limit int) ([]league.Summary, error) {
	r.listLimit = limit
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
