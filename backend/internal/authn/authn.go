package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sessionCookieName = "battlefield_session"

type Config struct {
	Enabled       bool
	Username      string
	Password      string
	SessionSecret string
	SessionTTL    time.Duration
	CookieSecure  bool
}

type Actor struct {
	ID string `json:"id"`
}

type actorContextKey struct{}

func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	return actor, ok
}

type Authenticator interface {
	Authenticate(*http.Request) (Actor, bool)
}

type Manager struct {
	config Config
	now    func() time.Time
}

func NewManager(config Config) *Manager {
	return &Manager{config: config, now: time.Now}
}

func (m *Manager) Enabled() bool {
	return m.config.Enabled
}

func (m *Manager) Authenticate(request *http.Request) (Actor, bool) {
	if !m.config.Enabled {
		return Actor{ID: "local-development"}, true
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return Actor{}, false
	}
	return m.parseToken(cookie.Value)
}

func (m *Manager) credentialsMatch(username, password string) bool {
	if !m.config.Enabled {
		return true
	}
	wantUsername := sha256.Sum256([]byte(m.config.Username))
	gotUsername := sha256.Sum256([]byte(username))
	wantPassword := sha256.Sum256([]byte(m.config.Password))
	gotPassword := sha256.Sum256([]byte(password))
	usernameMatches := subtle.ConstantTimeCompare(wantUsername[:], gotUsername[:])
	passwordMatches := subtle.ConstantTimeCompare(wantPassword[:], gotPassword[:])
	return usernameMatches&passwordMatches == 1
}

func (m *Manager) setSessionCookie(response http.ResponseWriter, actor Actor) {
	expires := m.now().Add(m.config.SessionTTL)
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    m.createToken(actor, expires),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.config.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   m.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (m *Manager) clearSessionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (m *Manager) createToken(actor Actor, expires time.Time) string {
	encodedActor := base64.RawURLEncoding.EncodeToString([]byte(actor.ID))
	payload := encodedActor + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(m.config.SessionSecret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) parseToken(token string) (Actor, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Actor{}, false
	}
	payload := parts[0] + "." + parts[1]
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Actor{}, false
	}
	mac := hmac.New(sha256.New, []byte(m.config.SessionSecret))
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return Actor{}, false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !m.now().Before(time.Unix(expires, 0)) {
		return Actor{}, false
	}
	actorID, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(actorID) == 0 {
		return Actor{}, false
	}
	return Actor{ID: string(actorID)}, true
}

type Routes struct {
	manager *Manager
}

const (
	cacheControlHeader = "Cache-Control"
	noStoreDirective   = "no-store"
)

func NewRoutes(manager *Manager) *Routes {
	return &Routes{manager: manager}
}

func (r *Routes) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/login", r.login)
	mux.HandleFunc("GET /v1/auth/session", r.session)
	mux.HandleFunc("POST /v1/auth/logout", r.logout)
}

func (r *Routes) login(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		http.Error(response, "invalid login request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(response, "invalid login request", http.StatusBadRequest)
		return
	}
	if !r.manager.credentialsMatch(credentials.Username, credentials.Password) {
		response.Header().Set(cacheControlHeader, noStoreDirective)
		http.Error(response, "invalid username or password", http.StatusUnauthorized)
		return
	}
	actor := Actor{ID: r.manager.config.Username}
	if !r.manager.Enabled() {
		actor.ID = "local-development"
	}
	r.manager.setSessionCookie(response, actor)
	response.Header().Set(cacheControlHeader, noStoreDirective)
	response.WriteHeader(http.StatusNoContent)
}

func (r *Routes) session(response http.ResponseWriter, request *http.Request) {
	actor, ok := r.manager.Authenticate(request)
	response.Header().Set(cacheControlHeader, noStoreDirective)
	response.Header().Set("Content-Type", "application/json")
	if !ok {
		response.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(response).Encode(map[string]any{
			"enabled": true, "authenticated": false,
		})
		return
	}
	_ = json.NewEncoder(response).Encode(map[string]any{
		"enabled": r.manager.Enabled(), "authenticated": true, "actor": actor,
	})
}

func (r *Routes) logout(response http.ResponseWriter, _ *http.Request) {
	r.manager.clearSessionCookie(response)
	response.Header().Set(cacheControlHeader, noStoreDirective)
	response.WriteHeader(http.StatusNoContent)
}

func RequireAuthentication(enabled bool, authenticator Authenticator, next http.Handler) http.Handler {
	if !enabled {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, Actor{ID: "local-development"})))
		})
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/v1/") || isPublicAuthenticationPath(request.URL.Path) {
			next.ServeHTTP(response, request)
			return
		}
		actor, ok := authenticator.Authenticate(request)
		if !ok {
			response.Header().Set(cacheControlHeader, noStoreDirective)
			http.Error(response, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor)))
	})
}

func isPublicAuthenticationPath(path string) bool {
	return path == "/v1/auth/login" || path == "/v1/auth/session"
}
