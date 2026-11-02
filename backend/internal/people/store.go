// Package people records the role and optional display name of each Admin, Tester and
// Player who signs in, and turns owner IDs into the labels shown on the battles and
// factions they own. Guests and service identities are never recorded, and labels
// never fall back to an email.

package people

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

var (
	ErrInvalidName = errors.New("display names are 3 to 24 letters, digits, spaces, hyphens or underscores")
	ErrNameTaken   = errors.New("that display name is taken")
)

const (
	// AdminLabel names whatever an Admin owns, including league battles and rows
	// created before ownership existed.
	AdminLabel = "Admin"
	// UnnamedLabel names what a Player or Tester owns before choosing a name.
	UnnamedLabel = "Unnamed commander"
)

// labelTimeout bounds the optional owner lookup, so a stalled database delays a read
// by at most this long before the response leaves the names out.
const labelTimeout = time.Second

// namePattern matches the people_display_name check: no leading or trailing space.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9 _-]{1,22}[A-Za-z0-9_-]$`)

type Store struct {
	DB postgres.DBTX
}

func NewStore(db postgres.DBTX) *Store {
	return &Store{DB: db}
}

// NormalizeName trims a requested display name and checks it.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return "", ErrInvalidName
	}
	return name, nil
}

// recorded reports the roles that are stored: the people who can own something.
func recorded(actor authn.Actor) bool {
	return actor.ID != "" && (actor.Role == authn.Admin || actor.Role == authn.Tester || actor.Role == authn.Player)
}

// Record stores the actor's current role and returns its display name, "" when none is
// set. It writes only when the actor is new or its role changed. Other roles are
// ignored.
func (s *Store) Record(ctx context.Context, actor authn.Actor) (string, error) {
	if !recorded(actor) {
		return "", nil
	}
	var role string
	var name *string
	err := s.DB.QueryRow(ctx, `SELECT role, display_name FROM people WHERE actor_id = $1`, actor.ID).Scan(&role, &name)
	if err == nil && role == string(actor.Role) {
		return deref(name), nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("read person: %w", err)
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO people(actor_id, role) VALUES ($1, $2)
		ON CONFLICT (actor_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()
		RETURNING display_name`, actor.ID, string(actor.Role)).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("record person: %w", err)
	}
	return deref(name), nil
}

// SessionName is the session hook: Record, with failures logged as a fixed reason and
// SQLSTATE rather than the raw error, which can name the database host and user.
func (s *Store) SessionName(ctx context.Context, actor authn.Actor) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	name, err := s.Record(ctx, actor)
	if err != nil {
		logFailure(ctx, "display_name_read_failed", err)
	}
	return name, err
}

// SetName validates and stores the actor's display name, recording its role too.
func (s *Store) SetName(ctx context.Context, actor authn.Actor, name string) (string, error) {
	if !recorded(actor) {
		return "", authn.ErrForbidden
	}
	name, err := NormalizeName(name)
	if err != nil {
		return "", err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO people(actor_id, role, display_name) VALUES ($1, $2, $3)
		ON CONFLICT (actor_id) DO UPDATE SET role = EXCLUDED.role, display_name = EXCLUDED.display_name, updated_at = now()`,
		actor.ID, string(actor.Role), name)
	var database *pgconn.PgError
	if errors.As(err, &database) && database.Code == "23505" && database.ConstraintName == "people_display_name_key" {
		return "", ErrNameTaken
	}
	if err != nil {
		return "", fmt.Errorf("save display name: %w", err)
	}
	return name, nil
}

// Labels returns a label for each distinct, nonempty owner ID in one query: "Admin"
// for an Admin or the legacy owner, otherwise the display name, otherwise UnnamedLabel.
func (s *Store) Labels(ctx context.Context, ownerIDs []string) (map[string]string, error) {
	ids := slices.DeleteFunc(slices.Compact(slices.Sorted(slices.Values(ownerIDs))), func(id string) bool { return id == "" })
	labels := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return labels, nil
	}
	for _, id := range ids {
		labels[id] = Label(id, "", "")
	}
	ctx, cancel := context.WithTimeout(ctx, labelTimeout)
	defer cancel()
	rows, err := s.DB.Query(ctx, `SELECT actor_id, role, display_name FROM people WHERE actor_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("read owner names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, role string
		var name *string
		if err := rows.Scan(&id, &role, &name); err != nil {
			return nil, fmt.Errorf("scan owner name: %w", err)
		}
		labels[id] = Label(id, authn.Role(role), deref(name))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate owner names: %w", err)
	}
	return labels, nil
}

// Label names an owner from its recorded role and display name. An Admin's items say
// "Admin" whatever name the person holds.
func Label(ownerID string, role authn.Role, name string) string {
	switch {
	case ownerID == authn.LegacyOwner || role == authn.Admin:
		return AdminLabel
	case name != "":
		return name
	default:
		return UnnamedLabel
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func logFailure(ctx context.Context, event string, err error) {
	attrs := []any{"reason", "database_unavailable"}
	var database *pgconn.PgError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		attrs = []any{"reason", "deadline_exceeded"}
	case errors.Is(err, context.Canceled):
		attrs = []any{"reason", "canceled"}
	case errors.As(err, &database):
		attrs = []any{"reason", "database_error", "sqlstate", database.Code}
	}
	slog.ErrorContext(ctx, event, append([]any{"request_id", httpapi.RequestID(ctx)}, attrs...)...)
}
