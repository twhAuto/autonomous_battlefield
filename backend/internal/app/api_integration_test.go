// Drives the assembled API over real HTTP against PostgreSQL: production (Cloudflare
// Access) authentication, faction and battle creation, an in-process engine run with
// polling and views, authorization failures, retries, and malformed input.

package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

const (
	integrationAudience = "battlefield-integration"
	integrationOrigin   = "https://battlefield.test"
	integrationCIClient = "integration-ci.access"
	integrationScenario = "ranged-skirmish-v1"
)

// Each identity maps to the role it receives from the configured role table.
var integrationRoles = map[string]authn.Role{
	"alice@example.com": authn.Player, "bob@example.com": authn.Player,
	"dev@example.com": authn.Developer, "admin@example.com": authn.Admin,
}

func TestAPIIntegrationAgainstPostgres(t *testing.T) {
	db := isolatedAPIDatabase(t)
	issuer := newAccessIssuer(t)
	api := startIntegrationAPI(t, db, issuer)
	startIntegrationEngine(t, db)

	alice, bob := api.as(issuer.user("alice")), api.as(issuer.user("bob"))
	t.Run("probes", func(t *testing.T) { api.expect(t, api.anonymous().get("/readyz"), http.StatusOK) })
	t.Run("authentication", func(t *testing.T) { checkAuthentication(t, api, issuer) })

	blue, red := randomID(t), randomID(t)
	t.Run("factions", func(t *testing.T) { checkFactions(t, api, alice, bob, blue, red) })
	t.Run("malformed input", func(t *testing.T) { checkMalformedBattleInput(t, api, alice, blue, red) })

	battleID := randomID(t)
	t.Run("create and retry", func(t *testing.T) { checkBattleCreationRetries(t, api, alice, bob, battleID, blue, red) })
	t.Run("poll to completion and view", func(t *testing.T) { checkCompletedBattle(t, api, bob, battleID, blue) })
	users := integrationUsers{alice: alice, bob: bob, developer: api.as(issuer.user("dev")), admin: api.as(issuer.user("admin"))}
	t.Run("authorization", func(t *testing.T) { checkAuthorization(t, api, users, battleID, blue, red) })
}

// isolatedAPIDatabase migrates a private schema so the in-process engine only claims
// this test's battles while other packages share the same database concurrently.
func isolatedAPIDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	schema := fmt.Sprintf("api_integration_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	address, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	query.Set("search_path", schema)
	address.RawQuery = query.Encode()
	if _, err = postgres.Migrate(ctx, address.String(), 5*time.Second); err != nil {
		t.Fatalf("postgres.Migrate() returned an error: %v", err)
	}
	db, err := pgxpool.New(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func startIntegrationAPI(t *testing.T, db *pgxpool.Pool, issuer *accessIssuer) *integrationAPI {
	t.Helper()
	scenarios, err := catalog.LoadCatalog("../../scenarios", "../../rulesets")
	if err != nil {
		t.Fatalf("LoadCatalog() returned an error: %v", err)
	}
	application := New(db, scenarios, 10_000, Options{Auth: authn.Config{
		Mode: authn.ModeCloudflare, Issuer: issuer.server.URL, Audience: integrationAudience,
		Origin: integrationOrigin, CIClientID: integrationCIClient, Roles: integrationRoles,
	}})
	lifecycle := httpapi.NewLifecycleHandler()
	lifecycle.Activate(application.HTTPHandler(), db, nil)
	server := httptest.NewServer(lifecycle)
	t.Cleanup(server.Close)
	return &integrationAPI{base: server.URL, client: server.Client()}
}

func startIntegrationEngine(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	battles := battlerepo.NewPostgres(db)
	engine, err := worker.New(battles, battles, worker.Config{
		WorkerID: "api-integration", PollInterval: 50 * time.Millisecond, LeaseDuration: 30 * time.Second,
		HeartbeatInterval: time.Second, ShutdownTimeout: 5 * time.Second, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("worker.New() returned an error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- engine.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-stopped; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("engine stopped with an error: %v", err)
		}
	})
}

func checkAuthentication(t *testing.T, api *integrationAPI, issuer *accessIssuer) {
	anonymous := api.anonymous()
	api.expectError(t, anonymous.get("/v1/factions"), http.StatusUnauthorized, apierror.CodeAuthenticationRequired)
	api.expect(t, anonymous.get("/v1/auth/session"), http.StatusUnauthorized)
	for name, claims := range map[string]map[string]any{
		"expired":        {"exp": time.Now().Add(-time.Minute).Unix()},
		"wrong audience": {"aud": []string{"another-application"}},
		"wrong issuer":   {"iss": "https://attacker.example"},
		"no role":        {"email": "stranger@example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			client := api.as(issuer.token(t, "alice", claims))
			api.expectError(t, client.get("/v1/factions"), http.StatusUnauthorized, apierror.CodeAuthenticationRequired)
		})
	}
	var session struct {
		Authenticated bool
		Mode          string
		Actor         struct{ Email, Role string }
	}
	api.decode(t, api.as(issuer.user("alice")).get("/v1/auth/session"), http.StatusOK, &session)
	if !session.Authenticated || session.Mode != authn.ModeCloudflare || session.Actor.Email != "alice@example.com" || session.Actor.Role != string(authn.Player) {
		t.Fatalf("session = %+v, want an authenticated Cloudflare player", session)
	}
	// Production never accepts the legacy password login.
	api.expect(t, anonymous.post("/v1/auth/login", `{"username":"admin","password":"secret"}`), http.StatusNotFound)
	// A service token reaches only the CI smoke routes.
	ci := api.as(issuer.service(t))
	api.expect(t, ci.get("/v1/scenarios"), http.StatusOK)
	api.expectError(t, ci.get("/v1/factions"), http.StatusForbidden, apierror.CodePermissionDenied)
}

func checkFactions(t *testing.T, api *integrationAPI, alice, bob *integrationClient, blue, red string) {
	for _, id := range []string{blue, red} {
		body := factionBody(id, "Faction "+id[:8])
		api.expect(t, alice.post("/v1/factions", body), http.StatusCreated)
		api.expect(t, alice.post("/v1/factions", body), http.StatusCreated) // Identical retry.
	}
	api.expectError(t, bob.post("/v1/factions", factionBody(blue, "Taken")), http.StatusConflict, apierror.CodeAlreadyExists)
	var listed struct {
		Factions []struct{ ID, OwnerID string }
	}
	api.decode(t, bob.get("/v1/factions"), http.StatusOK, &listed)
	if len(listed.Factions) != 2 || listed.Factions[0].ID != blue || listed.Factions[1].ID != red {
		t.Fatalf("factions = %+v, want the two created factions in creation order", listed.Factions)
	}
}

func checkMalformedBattleInput(t *testing.T, api *integrationAPI, alice *integrationClient, blue, red string) {
	valid := func(change func(map[string]any)) string {
		body := battleBody(randomID(t), blue, red, 3)
		change(body)
		raw, _ := json.Marshal(body)
		return string(raw)
	}
	good := valid(func(map[string]any) {})
	api.expectError(t, alice.send(http.MethodPost, "/v1/battles", good, map[string]string{"Origin": "https://elsewhere.example"}), http.StatusForbidden, apierror.CodePermissionDenied)
	api.expectError(t, alice.send(http.MethodPost, "/v1/battles", good, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType, apierror.CodeUnsupportedMedia)
	for name, test := range map[string]struct {
		body   string
		status int
		code   apierror.ErrorCode
	}{
		"invalid JSON":         {`{"id":`, http.StatusBadRequest, apierror.CodeInvalidRequest},
		"two JSON values":      {good + good, http.StatusBadRequest, apierror.CodeInvalidRequest},
		"unknown field":        {valid(func(b map[string]any) { b["seed"] = 7 }), http.StatusBadRequest, apierror.CodeInvalidRequest},
		"oversized body":       {`{"id":"` + strings.Repeat("x", 70<<10) + `"}`, http.StatusBadRequest, apierror.CodeInvalidRequest},
		"non-UUID ID":          {valid(func(b map[string]any) { b["id"] = "battle-1" }), http.StatusBadRequest, apierror.CodeInvalidRequest},
		"zero ticks":           {valid(func(b map[string]any) { b["tickCount"] = 0 }), http.StatusBadRequest, apierror.CodeInvalidRequest},
		"ticks above scenario": {valid(func(b map[string]any) { b["tickCount"] = 501 }), http.StatusBadRequest, apierror.CodeInvalidRequest},
		"same faction twice":   {valid(func(b map[string]any) { b["participants"] = battleBody("", blue, blue, 3)["participants"] }), http.StatusBadRequest, apierror.CodeInvalidRequest},
		"unknown scenario":     {valid(func(b map[string]any) { b["scenarioId"] = "no-such-scenario" }), http.StatusNotFound, apierror.CodeNotFound},
		"unknown faction":      {valid(func(b map[string]any) { b["participants"] = battleBody("", blue, randomID(t), 3)["participants"] }), http.StatusNotFound, apierror.CodeNotFound},
		"unknown controller": {valid(func(b map[string]any) {
			b["participants"].([]map[string]any)[0]["controller"] = map[string]any{"type": "oracle"}
		}), http.StatusBadRequest, apierror.CodeInvalidRequest},
	} {
		t.Run(name, func(t *testing.T) { api.expectError(t, alice.post("/v1/battles", test.body), test.status, test.code) })
	}
	for name, path := range map[string]string{
		"history limit":  "/v1/battles?limit=0",
		"history status": "/v1/battles?status=unknown",
		"history cursor": "/v1/battles?cursor=not-a-cursor",
		"battle ID":      "/v1/battles/not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			api.expectError(t, alice.get(path), http.StatusBadRequest, apierror.CodeInvalidRequest)
		})
	}
	api.expectError(t, alice.get("/v1/battles/"+randomID(t)), http.StatusNotFound, apierror.CodeNotFound)
}

func checkBattleCreationRetries(t *testing.T, api *integrationAPI, alice, bob *integrationClient, battleID, blue, red string) {
	body, _ := json.Marshal(battleBody(battleID, blue, red, 3))
	for attempt := 1; attempt <= 2; attempt++ {
		response := alice.post("/v1/battles", string(body))
		if location := response.Header.Get("Location"); response.StatusCode == http.StatusAccepted && location != "/v1/battles/"+battleID {
			t.Fatalf("attempt %d Location = %q", attempt, location)
		}
		var created struct{ ID, Status string }
		api.decode(t, response, http.StatusAccepted, &created)
		// A new battle is queued; a retry reports its current status, which the
		// concurrent engine may have advanced as far as completion.
		want := map[string]bool{"queued": true}
		if attempt > 1 {
			want = map[string]bool{"queued": true, "running": true, "completed": true}
		}
		if created.ID != battleID || !want[created.Status] {
			t.Fatalf("attempt %d battle = %+v", attempt, created)
		}
	}
	var stored struct{ OwnerID string }
	api.decode(t, bob.get("/v1/battles/"+battleID), http.StatusOK, &stored)
	if !strings.HasSuffix(stored.OwnerID, "|subject-alice") {
		t.Fatalf("owner = %q, want the server-assigned identity of the creating player", stored.OwnerID)
	}
	api.expectError(t, bob.post("/v1/battles", string(body)), http.StatusConflict, apierror.CodeAlreadyExists)
	changed, _ := json.Marshal(battleBody(battleID, blue, red, 4))
	api.expectError(t, alice.post("/v1/battles", string(changed)), http.StatusConflict, apierror.CodeAlreadyExists)
}

// checkCompletedBattle polls as another Player, since battle reads are shared.
func checkCompletedBattle(t *testing.T, api *integrationAPI, viewer *integrationClient, battleID, faction string) {
	var battle struct {
		Status      string
		CurrentTick uint64 `json:"currentTick"`
		Attempt     int
	}
	deadline := time.Now().Add(45 * time.Second)
	for battle.Status != "completed" {
		if time.Now().After(deadline) {
			t.Fatalf("battle did not complete: %+v", battle)
		}
		time.Sleep(200 * time.Millisecond)
		api.decode(t, viewer.get("/v1/battles/"+battleID), http.StatusOK, &battle)
		if battle.Status == "failed" || battle.Status == "stopped" {
			t.Fatalf("battle settled as %q", battle.Status)
		}
	}
	if battle.CurrentTick != 3 || battle.Attempt != 1 {
		t.Fatalf("completed battle = %+v, want tick 3 on attempt 1", battle)
	}
	var events struct {
		Events []struct {
			Sequence uint64
			Type     string
		}
	}
	api.decode(t, viewer.get("/v1/battles/"+battleID+"/events?attempt=1&after=0&limit=500"), http.StatusOK, &events)
	if len(events.Events) == 0 || events.Events[len(events.Events)-1].Type != "battle_completed" {
		t.Fatalf("events = %+v, want a history ending in battle_completed", events.Events)
	}
	api.expect(t, viewer.get("/v1/battles/"+battleID+"/factions/"+faction+"/view"), http.StatusOK)
	var history struct{ Battles []struct{ ID string } }
	api.decode(t, viewer.get("/v1/battles?status=completed&limit=10"), http.StatusOK, &history)
	if len(history.Battles) != 1 || history.Battles[0].ID != battleID {
		t.Fatalf("completed history = %+v", history.Battles)
	}
}

// integrationUsers holds one signed-in client per role: two Players, a Developer and an Admin.
type integrationUsers struct{ alice, bob, developer, admin *integrationClient }

func checkAuthorization(t *testing.T, api *integrationAPI, users integrationUsers, completed, blue, red string) {
	alice, bob, developer, admin := users.alice, users.bob, users.developer, users.admin
	decisions := "/v1/battles/" + completed + "/decisions?attempt=1"
	api.expectError(t, bob.get(decisions), http.StatusForbidden, apierror.CodePermissionDenied)
	api.expect(t, developer.get(decisions), http.StatusOK)
	api.expectError(t, developer.get("/v1/admin/model-access"), http.StatusForbidden, apierror.CodePermissionDenied)
	api.expect(t, admin.get("/v1/admin/model-access"), http.StatusOK)
	api.expectError(t, bob.get("/v1/no-such-route"), http.StatusForbidden, apierror.CodePermissionDenied)
	api.expectError(t, bob.send(http.MethodDelete, "/v1/factions/"+blue, "", nil), http.StatusForbidden, apierror.CodePermissionDenied)
	api.expectError(t, alice.post("/v1/battles/"+completed+"/stop", ""), http.StatusConflict, apierror.CodeConflict)

	// A long battle stays active long enough to exercise controls and their retries.
	active := randomID(t)
	body, _ := json.Marshal(battleBody(active, blue, red, 500))
	api.expect(t, alice.post("/v1/battles", string(body)), http.StatusAccepted)
	for _, control := range []string{"pause", "resume", "stop"} {
		api.expectError(t, bob.post("/v1/battles/"+active+"/"+control, ""), http.StatusForbidden, apierror.CodePermissionDenied)
	}
	for _, control := range []string{"pause", "pause", "resume", "stop", "stop"} {
		api.expect(t, alice.post("/v1/battles/"+active+"/"+control, ""), http.StatusOK)
	}
	api.expectError(t, alice.post("/v1/battles/"+active+"/resume", ""), http.StatusConflict, apierror.CodeConflict)
	api.expect(t, admin.send(http.MethodDelete, "/v1/factions/"+red, "", nil), http.StatusNoContent)
}

func factionBody(id, name string) string {
	raw, _ := json.Marshal(map[string]string{"id": id, "name": name, "doctrine": "Hold the castle and strike when the enemy overextends."})
	return string(raw)
}

func battleBody(id, first, second string, ticks int) map[string]any {
	return map[string]any{"id": id, "scenarioId": integrationScenario, "tickCount": ticks, "participants": []map[string]any{
		{"factionId": first, "controller": map[string]any{"type": "scripted"}},
		{"factionId": second, "controller": map[string]any{"type": "scripted"}},
	}}
}

func randomID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	raw[6], raw[8] = raw[6]&0x0f|0x40, raw[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

type integrationAPI struct {
	base   string
	client *http.Client
}

type integrationClient struct {
	api   *integrationAPI
	token string
}

func (a *integrationAPI) anonymous() *integrationClient { return &integrationClient{api: a} }
func (a *integrationAPI) as(token string) *integrationClient {
	return &integrationClient{api: a, token: token}
}
func (c *integrationClient) get(path string) *http.Response {
	return c.send(http.MethodGet, path, "", nil)
}
func (c *integrationClient) post(path, body string) *http.Response {
	return c.send(http.MethodPost, path, body, nil)
}

// send issues a browser-like request: unsafe methods carry the configured Origin and
// a JSON body unless a header override replaces them.
func (c *integrationClient) send(method, path, body string, headers map[string]string) *http.Response {
	request, err := http.NewRequest(method, c.api.base+path, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", integrationOrigin)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Cf-Access-Jwt-Assertion", c.token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.api.client.Do(request)
	if err != nil {
		panic(err)
	}
	return response
}

func (a *integrationAPI) expect(t *testing.T, response *http.Response, status int) []byte {
	t.Helper()
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s %s = %d %s, want %d", response.Request.Method, response.Request.URL.Path, response.StatusCode, bytes.TrimSpace(body), status)
	}
	return body
}

func (a *integrationAPI) decode(t *testing.T, response *http.Response, status int, target any) {
	t.Helper()
	if err := json.Unmarshal(a.expect(t, response, status), target); err != nil {
		t.Fatalf("%s %s response is not JSON: %v", response.Request.Method, response.Request.URL.Path, err)
	}
}

// expectError checks the documented envelope, including the request ID echo.
func (a *integrationAPI) expectError(t *testing.T, response *http.Response, status int, code apierror.ErrorCode) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code      apierror.ErrorCode
			RequestID string
		} `json:"error"`
	}
	a.decode(t, response, status, &envelope)
	if envelope.Error.Code != code || envelope.Error.RequestID == "" || envelope.Error.RequestID != response.Header.Get("X-Request-ID") {
		t.Fatalf("%s %s error = %+v (X-Request-ID %q), want code %q", response.Request.Method, response.Request.URL.Path, envelope.Error, response.Header.Get("X-Request-ID"), code)
	}
}

// accessIssuer stands in for Cloudflare Access: it publishes a signing key at the
// real certificate path so the production verifier fetches and checks it over HTTP.
type accessIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newAccessIssuer(t *testing.T) *accessIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificates, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kid": "integration", "kty": "RSA", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cdn-cgi/access/certs", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(certificates) })
	issuer := &accessIssuer{server: httptest.NewServer(mux), key: key}
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (i *accessIssuer) user(name string) string {
	return i.sign(i.claims(name, nil))
}

func (i *accessIssuer) token(t *testing.T, name string, overrides map[string]any) string {
	t.Helper()
	return i.sign(i.claims(name, overrides))
}

func (i *accessIssuer) service(t *testing.T) string {
	t.Helper()
	claims := i.claims("", map[string]any{"common_name": integrationCIClient})
	delete(claims, "sub")
	delete(claims, "email")
	return i.sign(claims)
}

func (i *accessIssuer) claims(name string, overrides map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{"iss": i.server.URL, "aud": []string{integrationAudience}, "sub": "subject-" + name,
		"email": name + "@example.com", "type": "app", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	for key, value := range overrides {
		claims[key] = value
	}
	return claims
}

func (i *accessIssuer) sign(claims map[string]any) string {
	encode := func(value any) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	payload := encode(map[string]string{"alg": "RS256", "kid": "integration", "typ": "JWT"}) + "." + encode(claims)
	digest := sha256.Sum256([]byte(payload))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature)
}
