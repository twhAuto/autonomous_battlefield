// Parses and validates authentication configuration.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

const localPasswordMode = "local-password"

const disabledDevelopmentMode = "disabled-development"

var accessClientID = regexp.MustCompile(`^[a-f0-9]{32}\.access$`)

// legacyTesterKey is the retired name of the tester role in BATTLEFIELD_ACCESS_ROLES.
// It stays accepted so a rollback image and the current image can share one configuration.
const legacyTesterKey = "developer"

func parseAuthConfig(raw fileConfig, production bool) (authn.Config, error) {
	mode := strings.TrimSpace(os.Getenv(envAuthMode))
	if production && mode != authn.ModeCloudflare {
		return authn.Config{}, fmt.Errorf("production requires Cloudflare authentication")
	}
	if mode == authn.ModeCloudflare {
		return parseCloudflareConfig()
	}
	return parseLocalAuthConfig(raw, mode)
}

func parseCloudflareConfig() (authn.Config, error) {
	issuer := strings.TrimRight(os.Getenv("BATTLEFIELD_ACCESS_ISSUER"), "/")
	audience := strings.TrimSpace(os.Getenv("BATTLEFIELD_ACCESS_AUDIENCE"))
	origin := strings.TrimRight(os.Getenv("BATTLEFIELD_PUBLIC_ORIGIN"), "/")
	u, e := url.Parse(issuer)
	o, oe := url.Parse(origin)
	if e != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".cloudflareaccess.com") || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || audience == "" || oe != nil || o.Scheme != "https" || o.Host == "" || o.Path != "" || o.RawQuery != "" || o.Fragment != "" || o.User != nil {
		return authn.Config{}, fmt.Errorf("invalid Cloudflare issuer, audience or public origin")
	}
	roles, err := cloudflareRoles()
	if err != nil {
		return authn.Config{}, err
	}
	if v := os.Getenv(envAuthEnabled); v != "" && v != "true" {
		return authn.Config{}, fmt.Errorf("conflicting authentication settings")
	}
	ciClientID := os.Getenv("BATTLEFIELD_ACCESS_CI_CLIENT_ID")
	if ciClientID != "" && !accessClientID.MatchString(ciClientID) {
		return authn.Config{}, fmt.Errorf("invalid Access CI client ID")
	}
	return authn.Config{Mode: authn.ModeCloudflare, Enabled: true, Issuer: issuer, Audience: audience, Origin: origin, Roles: roles, CIClientID: ciClientID}, nil
}

func cloudflareRoles() (map[string]authn.Role, error) {
	assignments, err := parseRoleAssignments(os.Getenv("BATTLEFIELD_ACCESS_ROLES"))
	if err != nil {
		return nil, fmt.Errorf("invalid Access role assignments")
	}
	roles := map[string]authn.Role{}
	for role, emails := range assignments {
		if role == legacyTesterKey {
			role = string(authn.Tester)
		}
		if role != "admin" && role != "tester" && role != "player" {
			return nil, fmt.Errorf("unknown Access role")
		}
		for _, email := range emails {
			email = strings.ToLower(strings.TrimSpace(email))
			if !strings.Contains(email, "@") || roles[email] != "" {
				return nil, fmt.Errorf("invalid or duplicate Access identity")
			}
			roles[email] = authn.Role(role)
		}
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("Access requires assigned identities")
	}
	return roles, nil
}

func parseLocalAuthConfig(raw fileConfig, mode string) (authn.Config, error) {
	if mode != "" && mode != localPasswordMode && mode != disabledDevelopmentMode {
		return authn.Config{}, fmt.Errorf("unknown authentication mode")
	}
	enabled, err := localAuthEnabled(mode)
	if err != nil {
		return authn.Config{}, err
	}
	ttl, err := parseOptionalDuration("auth.sessionTTL", raw.Auth.SessionTTL, 12*time.Hour)
	if err != nil {
		return authn.Config{}, err
	}
	cookieSecure, err := parseBooleanEnvironmentVariable(envCookieSecure, false)
	if err != nil {
		return authn.Config{}, err
	}
	if !enabled {
		return authn.Config{Mode: disabledDevelopmentMode, SessionTTL: ttl, CookieSecure: cookieSecure}, nil
	}
	result, err := loadAuthenticationSecrets(ttl, cookieSecure)
	result.Mode = localPasswordMode
	return result, err
}

func localAuthEnabled(mode string) (bool, error) {
	enabledEnv := envAuthEnabled
	enabled, err := parseBooleanEnvironmentVariable(enabledEnv, false)
	if mode == localPasswordMode {
		if v := os.Getenv(enabledEnv); v != "" && v != "true" {
			return false, fmt.Errorf("conflicting authentication settings")
		}
		enabled = true
	}
	if mode == disabledDevelopmentMode {
		if v := os.Getenv(enabledEnv); v != "" && v != "false" {
			return false, fmt.Errorf("conflicting authentication settings")
		}
		enabled = false
	}
	if mode == "" && os.Getenv(enabledEnv) == "" {
		return false, fmt.Errorf("authentication mode must be explicitly configured")
	}
	if err != nil {
		return false, err
	}
	return enabled, nil
}

func loadAuthenticationSecrets(ttl time.Duration, cookieSecure bool) (authn.Config, error) {
	username := strings.TrimSpace(os.Getenv(envAdminUsername))
	password := os.Getenv(envAdminPassword)
	secret := os.Getenv(envSessionSecret)
	if username == "" {
		return authn.Config{}, fmt.Errorf("environment variable %q must not be empty", envAdminUsername)
	}
	if err := requireSecret(envAdminPassword, password, 12); err != nil {
		return authn.Config{}, err
	}
	if err := requireSecret(envSessionSecret, secret, minimumSecretBytes); err != nil {
		return authn.Config{}, err
	}
	return authn.Config{
		Enabled: true, Username: username, Password: password, SessionSecret: secret,
		SessionTTL: ttl, CookieSecure: cookieSecure,
	}, nil
}

// Reject repeated JSON role keys rather than silently accepting the last assignment.
func parseRoleAssignments(raw string) (map[string][]string, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	first, e := d.Token()
	if e != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("expected role object")
	}
	result := map[string][]string{}
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid role key")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate role key")
		}
		var values []string
		if e = d.Decode(&values); e != nil {
			return nil, e
		}
		result[key] = values
	}
	if _, e = d.Token(); e != nil {
		return nil, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("trailing role data")
	}
	return result, nil
}
