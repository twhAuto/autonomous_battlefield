package members

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cloudflarePayload struct {
	Include []map[string]any
	Exclude []map[string]any
	Require []map[string]any
}

func captureCloudflarePayload(t *testing.T, emails []string) cloudflarePayload {
	t.Helper()
	var payload cloudflarePayload
	c := &Cloudflare{Config: Config{AccountID: "account", GroupID: "group", Token: "private"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.cloudflare.com/client/v4/accounts/account/access/groups/group" || r.Method != "PUT" || r.Header.Get("Authorization") != "Bearer private" {
			t.Fatal("wrong request")
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Fatal("invalid payload")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	})}}
	if err := c.Replace(context.Background(), emails); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestCloudflareGroupPayload(t *testing.T) {
	emails := []string{"one@example.com", "two@example.com"}
	payload := captureCloudflarePayload(t, emails)
	if len(payload.Include) != len(emails) || len(payload.Exclude) != 0 || len(payload.Require) != 0 {
		t.Fatal(payload)
	}
	for i, email := range emails {
		if payload.Include[i]["email"].(map[string]any)["email"] != email {
			t.Fatal(payload)
		}
	}
}

func TestCloudflareEmptyGroupIsDenyAll(t *testing.T) {
	payload := captureCloudflarePayload(t, nil)
	if len(payload.Include) != 1 || payload.Include[0]["everyone"] == nil || len(payload.Exclude) != 1 || payload.Exclude[0]["everyone"] == nil || len(payload.Require) != 0 {
		t.Fatal("empty group not deny-all", payload)
	}
}

func TestCloudflareFailuresAreSanitized(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		fail   bool
	}{{429, "secret remote error", false}, {403, "private token", false}, {200, `{"success":false}`, false}, {200, "invalid", false}, {0, "", true}, {302, "", false}} {
		c := &Cloudflare{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if tt.fail {
				return nil, errors.New("private credential")
			}
			return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: http.Header{"Location": []string{"https://attacker.example/"}}}, nil
		})}}
		err := c.Replace(context.Background(), nil)
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
			t.Fatal("missing or leaking error", err)
		}
	}
}
func TestManagedConfig(t *testing.T) {
	for _, key := range []string{"BATTLEFIELD_MANAGED_PLAYERS", "BATTLEFIELD_MEMBERS_ACCOUNT_ID", "BATTLEFIELD_MEMBERS_GROUP_ID", "BATTLEFIELD_MEMBERS_API_TOKEN"} {
		t.Setenv(key, "")
	}
	if c, err := LoadConfig(nil); err != nil || c.Enabled {
		t.Fatal(c, err)
	}
	t.Setenv("BATTLEFIELD_MANAGED_PLAYERS", "true")
	if _, err := LoadConfig(nil); err == nil {
		t.Fatal("incomplete accepted")
	}
	t.Setenv("BATTLEFIELD_AUTH_MODE", "cloudflare")
	t.Setenv("BATTLEFIELD_MEMBERS_ACCOUNT_ID", strings.Repeat("a", 32))
	t.Setenv("BATTLEFIELD_MEMBERS_GROUP_ID", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	t.Setenv("BATTLEFIELD_MEMBERS_API_TOKEN", "token")
	if c, err := LoadConfig(nil); err != nil || !c.Enabled {
		t.Fatal(err)
	}
	for _, tt := range []struct{ k, v string }{{"BATTLEFIELD_MANAGED_PLAYERS", "yes"}, {"BATTLEFIELD_AUTH_MODE", "local-password"}, {"BATTLEFIELD_MEMBERS_ACCOUNT_ID", "../x"}, {"BATTLEFIELD_MEMBERS_GROUP_ID", "../x"}, {"BATTLEFIELD_MEMBERS_API_TOKEN", " "}, {"BATTLEFIELD_MEMBERS_API_TOKEN", "x\ny"}} {
		t.Run(tt.k+tt.v, func(t *testing.T) {
			t.Setenv(tt.k, tt.v)
			if _, err := LoadConfig(nil); err == nil {
				t.Fatal("unsafe config")
			}
		})
	}
}

func TestConfiguredPlayersValidatedBeforeStartup(t *testing.T) {
	t.Setenv("BATTLEFIELD_MANAGED_PLAYERS", "true")
	t.Setenv("BATTLEFIELD_AUTH_MODE", " cloudflare ")
	t.Setenv("BATTLEFIELD_MEMBERS_ACCOUNT_ID", strings.Repeat("a", 32))
	t.Setenv("BATTLEFIELD_MEMBERS_GROUP_ID", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	t.Setenv("BATTLEFIELD_MEMBERS_API_TOKEN", "token")
	for _, email := range []string{"user@localhost", "user@[10.0.0.1]", strings.Repeat("x", 255) + "@example.com"} {
		if _, err := LoadConfig(map[string]authn.Role{email: authn.Player}); err == nil || strings.Contains(err.Error(), email) {
			t.Fatal("invalid or leaking config", err)
		}
	}
	if _, err := LoadConfig(map[string]authn.Role{"friend@example.com": authn.Player}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncLogsAreUsefulAndSanitized(t *testing.T) {
	var out bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(original)
	for _, err := range []error{&remoteError{category: "http", status: 403}, &remoteError{category: "transport"}, &pgconn.PgError{Code: "40001", Message: "secret-sql"}, context.DeadlineExceeded, errors.New("private-database-url")} {
		logSyncFailure(err)
	}
	text := out.String()
	for _, want := range []string{"403", "transport", "concurrent_membership_change", "DEBUG", "deadline_exceeded", "database_unavailable"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing diagnostic", want, text)
		}
	}
	for _, secret := range []string{"secret-sql", "private-database-url"} {
		if strings.Contains(text, secret) {
			t.Fatal("leaking error", text)
		}
	}
}
