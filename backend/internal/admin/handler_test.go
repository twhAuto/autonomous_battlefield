package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type rowFunc func(...any) error

func (f rowFunc) Scan(v ...any) error { return f(v...) }

type fakeDB struct {
	readError, saveError error
	raw                  []byte
	written              []byte
}

func (d *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec")
}
func (d *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) { panic("unexpected Query") }
func (d *fakeDB) QueryRow(_ context.Context, q string, args ...any) pgx.Row {
	return rowFunc(func(v ...any) error {
		if len(args) > 0 {
			if d.saveError != nil {
				return d.saveError
			}
			d.written = args[0].([]byte)
			*v[0].(*int64) = 1
			return nil
		}
		if d.readError != nil {
			return d.readError
		}
		*v[0].(*int64) = 0
		*v[1].(*[]byte) = d.raw
		return nil
	})
}
func registry() *profile.Registry {
	return profile.NewCatalog("cluster", []ai.ModelProfile{
		{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true},
		{ID: "cli", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "fast", ModelOptions: []ai.ModelOption{{ID: "fast"}, {ID: "deep"}}},
	})
}
func request(h *Handler, method string, body any, role authn.Role) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/v1/admin/model-access", bytes.NewReader(raw))
	r = r.WithContext(authn.WithActor(r.Context(), authn.Actor{ID: "admin", Role: role}))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestSettingsValidationPersistenceAndFailures(t *testing.T) {
	db := &fakeDB{raw: []byte(`{"providers":{"obsolete":false},"profiles":{"obsolete":{"enabled":false,"allowedRoles":[]}}}`)}
	h := &Handler{Service: &Service{Repository: &Postgres{DB: db}, Profiles: registry()}}
	w := request(h, "GET", nil, authn.Admin)
	var s Settings
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil || len(s.Policy.Profiles) != 2 || len(s.Policy.Providers) != 2 {
		t.Fatalf("response=%s", w.Body.String())
	}
	if !s.Policy.Profiles["api"].Enabled || len(s.Policy.Profiles["api"].AllowedRoles) != 2 {
		t.Fatal("incorrect defaults")
	}
	s.Policy.Providers["openai"] = false
	if w = request(h, "PUT", s, authn.Admin); w.Code != 200 || len(db.written) == 0 {
		t.Fatalf("save=%d %s", w.Code, w.Body.String())
	}
	db.saveError = pgx.ErrNoRows
	if w = request(h, "PUT", s, authn.Admin); w.Code != 409 {
		t.Fatal(w.Code)
	}
	db.saveError = errors.New("offline")
	if w = request(h, "PUT", s, authn.Admin); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestSettingsValidationAndAuthorization(t *testing.T) {
	db := &fakeDB{raw: []byte(`{"providers":{},"profiles":{}}`)}
	h := &Handler{Service: &Service{Repository: &Postgres{DB: db}, Profiles: registry()}}
	var s Settings
	w := request(h, "GET", nil, authn.Admin)
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	for _, role := range []authn.Role{authn.Player, authn.Tester} {
		if request(h, "PUT", s, role).Code != 403 || request(h, "GET", nil, role).Code != 403 {
			t.Fatal("unauthorized settings access")
		}
	}
	s.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"player"}}
	if request(h, "PUT", s, authn.Admin).Code != 200 {
		t.Fatal("explicit player CLI access rejected")
	}
	s.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"root"}}
	if h.Service.valid(s) {
		t.Fatal("unknown role")
	}
	s.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"admin", "admin"}}
	if h.Service.valid(s) {
		t.Fatal("duplicate role")
	}
	s.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{}}
	if !h.Service.valid(s) {
		t.Fatal("empty explicit role list rejected")
	}
	delete(s.Policy.Providers, "openai")
	if h.Service.valid(s) {
		t.Fatal("missing provider")
	}
	delete(s.Policy.Profiles, "api")
	if h.Service.valid(s) {
		t.Fatal("missing profile")
	}
}
func TestSettingsReadFailures(t *testing.T) {
	db := &fakeDB{}
	h := &Handler{Service: &Service{Repository: &Postgres{DB: db}, Profiles: registry()}}
	db.readError = errors.New("offline")
	if request(h, "GET", nil, authn.Admin).Code != 503 {
		t.Fatal("read failure hidden")
	}
	if _, err := h.Service.ReadPolicy(); err == nil {
		t.Fatal("policy failure hidden")
	}
	db.readError = nil
	db.raw = []byte("broken")
	if request(h, "GET", nil, authn.Admin).Code != 503 {
		t.Fatal("corrupt policy hidden")
	}
}

func TestModelRolesValidation(t *testing.T) {
	db := &fakeDB{raw: []byte(`{"providers":{},"profiles":{}}`)}
	h := &Handler{Service: &Service{Repository: &Postgres{DB: db}, Profiles: registry()}}
	var s Settings
	if err := json.Unmarshal(request(h, "GET", nil, authn.Admin).Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	profileRoles := []string{"admin", "tester"}
	for _, tt := range []struct {
		name    string
		profile string
		models  map[string]profile.ModelAccess
		want    int
	}{
		{"subset of the profile's roles", "cli", map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{"admin"}}}, 200},
		{"empty list denies everyone", "cli", map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{}}}, 200},
		{"role the profile denies", "cli", map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{"admin", "player"}}}, 400},
		{"unknown model", "cli", map[string]profile.ModelAccess{"retired": {AllowedRoles: []string{"admin"}}}, 400},
		{"missing role list", "cli", map[string]profile.ModelAccess{"deep": {}}, 400},
		{"unknown role", "cli", map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{"root"}}}, 400},
		{"duplicate role", "cli", map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{"admin", "admin"}}}, 400},
		{"profile without model options", "api", map[string]profile.ModelAccess{"gpt": {AllowedRoles: []string{"admin"}}}, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := s
			policy.Policy.Profiles = map[string]profile.Access{"api": s.Policy.Profiles["api"], "cli": s.Policy.Profiles["cli"]}
			policy.Policy.Profiles[tt.profile] = profile.Access{Enabled: true, AllowedRoles: profileRoles, Models: tt.models}
			db.written = nil
			if w := request(h, "PUT", policy, authn.Admin); w.Code != tt.want || (tt.want == 400) != (db.written == nil) {
				t.Fatalf("status %d, written %s", w.Code, db.written)
			}
		})
	}
	s.Policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: profileRoles, Models: map[string]profile.ModelAccess{"deep": {AllowedRoles: []string{"tester"}}}}
	if w := request(h, "PUT", s, authn.Admin); w.Code != 200 || !bytes.Contains(db.written, []byte(`"models":{"deep":{"allowedRoles":["tester"]}}`)) {
		t.Fatalf("model roles not persisted: %d %s", w.Code, db.written)
	}
}

func TestStoredModelRolesReadBack(t *testing.T) {
	db := &fakeDB{raw: []byte(`{"providers":{"codex-cli":true},"profiles":{"cli":{"enabled":true,"allowedRoles":["admin","player"],"models":{"deep":{"allowedRoles":["admin"]},"retired":{"allowedRoles":["player"]}}}}}`)}
	h := &Handler{Service: &Service{Repository: &Postgres{DB: db}, Profiles: registry()}}
	view, err := h.Service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	models := view.Policy.Profiles["cli"].Models
	if len(models) != 1 || len(models["deep"].AllowedRoles) != 1 || models["deep"].AllowedRoles[0] != "admin" {
		t.Fatalf("models=%+v", models)
	}
	if !h.Service.valid(view.Settings) {
		t.Fatal("a policy read back from storage does not validate")
	}
	db.raw = []byte(`{"providers":{"codex-cli":true},"profiles":{"cli":{"enabled":true,"allowedRoles":["admin","player"]}}}`)
	if view, err = h.Service.Get(context.Background()); err != nil || view.Policy.Profiles["cli"].Models != nil {
		t.Fatalf("policy without model entries: %+v %v", view.Policy.Profiles["cli"], err)
	}
	raw, _ := json.Marshal(view.Policy)
	if bytes.Contains(raw, []byte("models")) {
		t.Fatalf("absent model entries were serialized: %s", raw)
	}
}
