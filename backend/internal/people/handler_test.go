package people

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type fakeNames struct {
	err   error
	actor authn.Actor
	name  string
}

func (f *fakeNames) SetName(_ context.Context, actor authn.Actor, name string) (string, error) {
	f.actor, f.name = actor, name
	if f.err != nil {
		return "", f.err
	}
	return NormalizeName(name)
}

func putProfile(t *testing.T, names Names, actor *authn.Actor, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/v1/profile", strings.NewReader(body))
	if actor != nil {
		request = request.WithContext(authn.WithActor(request.Context(), *actor))
	}
	response := httptest.NewRecorder()
	httpapi.NewRouter(NewHandler(names)).ServeHTTP(response, request)
	return response
}

func TestSetDisplayNameResponses(t *testing.T) {
	player := authn.Actor{ID: "issuer|ada", Role: authn.Player}
	for _, test := range []struct {
		name, body, code string
		err              error
		status           int
	}{
		{name: "saved", body: `{"displayName":" Iron Duke "}`, status: http.StatusOK},
		{name: "malformed", body: `{"displayName":`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unknown field", body: `{"displayName":"Ada","email":"a@example.com"}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "invalid", body: `{"displayName":"Ada"}`, err: ErrInvalidName, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "taken", body: `{"displayName":"Ada"}`, err: ErrNameTaken, status: http.StatusConflict, code: "conflict"},
		{name: "forbidden", body: `{"displayName":"Ada"}`, err: authn.ErrForbidden, status: http.StatusForbidden, code: "permission_denied"},
		{name: "unavailable", body: `{"displayName":"Ada"}`, err: errors.New("database down"), status: http.StatusServiceUnavailable, code: "service_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			names := &fakeNames{err: test.err}
			response := putProfile(t, names, &player, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, body %s", response.Code, response.Body)
			}
			var body struct {
				DisplayName string `json:"displayName"`
				Error       struct{ Code string }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != test.code || (test.status == http.StatusOK && (body.DisplayName != "Iron Duke" || names.actor != player)) {
				t.Fatalf("body = %s", response.Body)
			}
		})
	}
}

func TestOnlyPeopleWhoPlayCanSetANameThroughTheRoute(t *testing.T) {
	for _, test := range []struct {
		actor  *authn.Actor
		status int
	}{
		{nil, http.StatusUnauthorized},
		{&authn.Actor{ID: "issuer|guest", Role: authn.Guest}, http.StatusForbidden},
		{&authn.Actor{ID: "issuer|service:ci", Role: authn.CI}, http.StatusForbidden},
		{&authn.Actor{ID: "issuer|grace", Role: authn.Tester}, http.StatusOK},
		{&authn.Actor{ID: "issuer|boss", Role: authn.Admin}, http.StatusOK},
	} {
		names := &fakeNames{}
		if response := putProfile(t, names, test.actor, `{"displayName":"Iron Duke"}`); response.Code != test.status {
			t.Fatalf("%+v: status = %d", test.actor, response.Code)
		}
		if test.status != http.StatusOK && names.name != "" {
			t.Fatalf("%+v reached the store", test.actor)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	for name, want := range map[string]string{" Ada ": "Ada", "Iron_Duke-7": "Iron_Duke-7", "Grace_Hopper-24 chars ok": "Grace_Hopper-24 chars ok"} {
		if got, err := NormalizeName(name); err != nil || got != want {
			t.Fatalf("%q = %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"Al", "   ", "Ada!", "Ada\nLovelace", "Zoë Smith", "Twenty-five characters ok"} {
		if _, err := NormalizeName(name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("%q accepted", name)
		}
	}
}

func TestLabel(t *testing.T) {
	for _, test := range []struct {
		owner, name, want string
		role              authn.Role
	}{
		{"issuer|ada", "Ada", "Ada", authn.Player},
		{"issuer|grace", "", UnnamedLabel, authn.Tester},
		{"issuer|boss", "The Boss", AdminLabel, authn.Admin},
		{authn.LegacyOwner, "", AdminLabel, ""},
		{"issuer|unknown", "", UnnamedLabel, ""},
	} {
		if got := Label(test.owner, test.role, test.name); got != test.want {
			t.Fatalf("Label(%q, %q, %q) = %q, want %q", test.owner, test.role, test.name, got, test.want)
		}
	}
}

func TestFailureLogsNeverIncludeTheRawError(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for err, want := range map[error]string{
		context.DeadlineExceeded:                            `"reason":"deadline_exceeded"`,
		context.Canceled:                                    `"reason":"canceled"`,
		&pgconn.PgError{Code: "57P01", Message: "x"}:        `"reason":"database_error","sqlstate":"57P01"`,
		errors.New("dial tcp db.internal user=battlefield"): `"reason":"database_unavailable"`,
	} {
		output.Reset()
		logFailure(context.Background(), "display_name_read_failed", err)
		if line := output.String(); !strings.Contains(line, want) || strings.Contains(line, "db.internal") || strings.Contains(line, `"x"`) {
			t.Fatalf("%v logged %s", err, line)
		}
	}
}
