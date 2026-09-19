package members

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

var ErrInvalid = errors.New("enter a valid email address")
var ErrProtected = errors.New("administrator and developer identities are managed outside this page")
var ErrNotFound = errors.New("member not found")

type Store struct {
	DB     *pgxpool.Pool
	Roles  map[string]authn.Role
	Writer GroupWriter
}
type Member struct {
	ID      string    `json:"id"`
	Email   string    `json:"email"`
	Desired bool      `json:"desiredAccess"`
	Version int64     `json:"version"`
	Synced  int64     `json:"syncedVersion"`
	Updated time.Time `json:"updatedAt"`
}

func (s *Store) protected(email string) bool {
	return s.Roles[email] == authn.Admin || s.Roles[email] == authn.Developer
}

var emailDomain = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func normalize(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return "", ErrInvalid
	}
	at := strings.LastIndexByte(email, '@')
	if at < 1 || at > 64 || !emailDomain.MatchString(email[at+1:]) {
		return "", ErrInvalid
	}
	return email, nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// Initialize imports configuration exactly once; tombstones survive every restart.
func (s *Store) Initialize(ctx context.Context) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var initialized bool
	if err = tx.QueryRow(ctx, "SELECT initialized FROM player_membership_sync WHERE id FOR UPDATE").Scan(&initialized); err != nil {
		return err
	}
	// Configured privileged identities must leave a durable revocation behind.
	// Filtering alone would restore an old invitation when a role is removed later.
	if err = s.retirePrivileged(ctx, tx); err != nil {
		return err
	}
	if initialized {
		return tx.Commit(ctx)
	}
	for email, role := range s.Roles {
		if role != authn.Player {
			continue
		}
		normalized, e := normalize(email)
		if e != nil {
			return e
		}
		var id string
		if err = tx.QueryRow(ctx, "INSERT INTO player_members(email,desired_access) VALUES($1,true) RETURNING id", normalized).Scan(&id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO player_membership_audit(member_id,actor_id,action) VALUES($1,'configuration','import')", id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "UPDATE player_membership_sync SET initialized=true,version=version+1,next_attempt=now() WHERE id")
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) retirePrivileged(ctx context.Context, tx pgx.Tx) error {
	emails := []string{}
	for email := range s.Roles {
		if s.protected(email) {
			emails = append(emails, email)
		}
	}
	result, err := tx.Exec(ctx, `WITH removed AS (
 UPDATE player_members SET desired_access=false,version=version+1,updated_at=now()
 WHERE email=ANY($1::text[]) AND desired_access RETURNING id)
 INSERT INTO player_membership_audit(member_id,actor_id,action)
 SELECT id,'configuration','remove' FROM removed`, emails)
	if err != nil {
		return err
	}
	if result.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, "UPDATE player_membership_sync SET version=version+1,next_attempt=now(),attempts=0,last_error='' WHERE id")
	}
	return err
}

func (s *Store) Allowed(ctx context.Context, email string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var allowed bool
	err := s.DB.QueryRow(ctx, "SELECT desired_access AND version=synced_version FROM player_members WHERE email=$1", email).Scan(&allowed)
	return err == nil && allowed && !s.protected(email)
}
func (s *Store) List(ctx context.Context) ([]Member, string, error) {
	rows, err := s.DB.Query(ctx, "SELECT id,email,desired_access,version,synced_version,updated_at FROM player_members ORDER BY email")
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result := []Member{}
	for rows.Next() {
		var m Member
		if err = rows.Scan(&m.ID, &m.Email, &m.Desired, &m.Version, &m.Synced, &m.Updated); err != nil {
			return nil, "", err
		}
		if !s.protected(m.Email) {
			result = append(result, m)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	var lastError string
	err = s.DB.QueryRow(ctx, "SELECT last_error FROM player_membership_sync WHERE id").Scan(&lastError)
	return result, lastError, err
}

// Change serializes desired-state writes on the singleton row. Remote calls never
// hold that row lock, so removing a member does not wait on Cloudflare.
func (s *Store) Change(ctx context.Context, actor, email, id string, grant bool) error {
	email, err := s.changeEmail(email, grant)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM player_membership_sync WHERE id FOR UPDATE"); err != nil {
		return err
	}
	change, err := s.prepareChange(ctx, tx, email, id, grant)
	if err != nil {
		return err
	}
	if !change.changed {
		return tx.Commit(ctx)
	}
	action := "remove"
	if grant {
		action = "grant"
	}
	if _, err = tx.Exec(ctx, "INSERT INTO player_membership_audit(member_id,actor_id,action) VALUES($1,$2,$3)", change.id, actor, action); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE player_membership_sync SET version=version+1,attempts=0,next_attempt=now(),last_error='' WHERE id"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type preparedChange struct {
	id      string
	changed bool
}

func (s *Store) changeEmail(email string, grant bool) (string, error) {
	if !grant {
		return email, nil
	}
	email, err := normalize(email)
	if err != nil {
		return "", err
	}
	if s.protected(email) {
		return "", ErrProtected
	}
	return email, nil
}

func (s *Store) prepareChange(ctx context.Context, tx pgx.Tx, email, id string, grant bool) (preparedChange, error) {
	var desired bool
	var err error
	if grant {
		err = tx.QueryRow(ctx, "SELECT id,desired_access FROM player_members WHERE email=$1", email).Scan(&id, &desired)
	} else {
		err = tx.QueryRow(ctx, "SELECT email,desired_access FROM player_members WHERE id::text=$1", id).Scan(&email, &desired)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if !grant {
			return preparedChange{}, ErrNotFound
		}
		err = tx.QueryRow(ctx, "INSERT INTO player_members(email,desired_access) VALUES($1,true) RETURNING id", email).Scan(&id)
		return preparedChange{id: id, changed: true}, err
	}
	if err != nil {
		return preparedChange{}, err
	}
	if s.protected(email) {
		return preparedChange{}, ErrProtected
	}
	if desired == grant {
		return preparedChange{id: id}, nil
	}
	_, err = tx.Exec(ctx, "UPDATE player_members SET desired_access=$2,version=version+1,updated_at=now() WHERE id=$1", id, grant)
	return preparedChange{id: id, changed: true}, err
}
func (s *Store) Retry(ctx context.Context, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "UPDATE player_membership_sync SET next_attempt=now(),attempts=0 WHERE id"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO player_membership_audit(actor_id,action) VALUES($1,'retry')", actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
