package members

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
)

var ErrInvalid = errors.New("enter a valid email address")
var ErrProtected = errors.New("this email has a role set in server configuration and is managed outside this page")
var ErrNotFound = errors.New("member not found")
var ErrInvalidRole = errors.New("choose the Player or Tester role")

type Store struct {
	DB     *pgxpool.Pool
	Roles  map[string]authn.Role
	Writer GroupWriter
}

// NewStore checks every configured Player email before the API starts, so a bad
// BATTLEFIELD_ACCESS_ROLES entry fails startup rather than the first import.
func NewStore(db *pgxpool.Pool, roles map[string]authn.Role, writer GroupWriter) (*Store, error) {
	for email, role := range roles {
		if role != authn.Player {
			continue
		}
		if _, err := normalize(email); err != nil {
			return nil, fmt.Errorf("BATTLEFIELD_ACCESS_ROLES contains an invalid Player email")
		}
	}
	return &Store{DB: db, Roles: roles, Writer: writer}, nil
}

type Member struct {
	ID      string     `json:"id"`
	Email   string     `json:"email"`
	Role    authn.Role `json:"role"`
	Desired bool       `json:"desiredAccess"`
	Version int64      `json:"version"`
	Synced  int64      `json:"syncedVersion"`
	Updated time.Time  `json:"updatedAt"`
}

func (s *Store) protected(email string) bool {
	return s.Roles[email] == authn.Admin || s.Roles[email] == authn.Tester
}

// memberID guards removal so the uuid primary key is compared directly.
var memberID = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

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
		if _, err = tx.Exec(ctx, "INSERT INTO player_membership_audit(member_id,actor_id,action,role) VALUES($1,'configuration','import','player')", id); err != nil {
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

// ActiveRole returns the stored role of a member whose grant Cloudflare has confirmed,
// or "" for anyone else, including a configured Admin or Tester.
func (s *Store) ActiveRole(ctx context.Context, email string) authn.Role {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var role authn.Role
	var active bool
	err := s.DB.QueryRow(ctx, "SELECT role,desired_access AND version=synced_version FROM player_members WHERE email=$1", email).Scan(&role, &active)
	if err != nil || !active || s.protected(email) {
		return ""
	}
	return role
}
func (s *Store) List(ctx context.Context) ([]Member, string, error) {
	rows, err := s.DB.Query(ctx, "SELECT id,email,role,desired_access,version,synced_version,updated_at FROM player_members ORDER BY email")
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result := []Member{}
	for rows.Next() {
		var m Member
		if err = rows.Scan(&m.ID, &m.Email, &m.Role, &m.Desired, &m.Version, &m.Synced, &m.Updated); err != nil {
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
// hold that row lock, so removing a member does not wait on Cloudflare. A grant sets
// role when it is non-empty; a new member without one is a Player. Changing only the
// role of an active member needs no Cloudflare update, so it applies at once.
func (s *Store) Change(ctx context.Context, actor, email, id string, grant bool, role authn.Role) error {
	if !grant && !memberID.MatchString(id) {
		return ErrNotFound
	}
	if !assignable(role) {
		return ErrInvalidRole
	}
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
	change, err := s.prepareChange(ctx, tx, email, id, grant, role)
	if err != nil {
		return err
	}
	if change.action == "" {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO player_membership_audit(member_id,actor_id,action,role) VALUES($1,$2,$3,NULLIF($4,''))", change.id, actor, change.action, change.role); err != nil {
		return err
	}
	if change.action != auditRole {
		if _, err = tx.Exec(ctx, "UPDATE player_membership_sync SET version=version+1,attempts=0,next_attempt=now(),last_error='' WHERE id"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// assignable reports whether a grant may set role; empty keeps the stored role.
func assignable(role authn.Role) bool {
	return role == "" || role == authn.Player || role == authn.Tester
}

// Audit actions a change records. A role change alone leaves Cloudflare untouched.
const (
	auditGrant  = "grant"
	auditRemove = "remove"
	auditRole   = "role"
)

// preparedChange is the audited result of a change; an empty action changed nothing.
type preparedChange struct {
	id     string
	action string
	role   authn.Role
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

func (s *Store) prepareChange(ctx context.Context, tx pgx.Tx, email, id string, grant bool, role authn.Role) (preparedChange, error) {
	var desired bool
	var stored authn.Role
	var err error
	if grant {
		err = tx.QueryRow(ctx, "SELECT id,desired_access,role FROM player_members WHERE email=$1", email).Scan(&id, &desired, &stored)
	} else {
		err = tx.QueryRow(ctx, "SELECT email,desired_access,role FROM player_members WHERE id=$1", id).Scan(&email, &desired, &stored)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if !grant {
			return preparedChange{}, ErrNotFound
		}
		role = cmp.Or(role, authn.Player)
		err = tx.QueryRow(ctx, "INSERT INTO player_members(email,desired_access,role) VALUES($1,true,$2) RETURNING id", email, role).Scan(&id)
		return preparedChange{id: id, action: auditGrant, role: role}, err
	}
	if err != nil {
		return preparedChange{}, err
	}
	if s.protected(email) {
		return preparedChange{}, ErrProtected
	}
	if !grant {
		return s.prepareRemoval(ctx, tx, id, desired)
	}
	return s.prepareGrant(ctx, tx, id, desired, stored, cmp.Or(role, stored))
}

func (s *Store) prepareRemoval(ctx context.Context, tx pgx.Tx, id string, desired bool) (preparedChange, error) {
	if !desired {
		return preparedChange{id: id}, nil
	}
	_, err := tx.Exec(ctx, "UPDATE player_members SET desired_access=false,version=version+1,updated_at=now() WHERE id=$1", id)
	return preparedChange{id: id, action: auditRemove}, err
}

// prepareGrant restores access with role, or changes only the role of an active member
// without a new version, so the member stays synchronized.
func (s *Store) prepareGrant(ctx context.Context, tx pgx.Tx, id string, desired bool, stored, role authn.Role) (preparedChange, error) {
	if !desired {
		_, err := tx.Exec(ctx, "UPDATE player_members SET desired_access=true,role=$2,version=version+1,updated_at=now() WHERE id=$1", id, role)
		return preparedChange{id: id, action: auditGrant, role: role}, err
	}
	if role == stored {
		return preparedChange{id: id}, nil
	}
	_, err := tx.Exec(ctx, "UPDATE player_members SET role=$2,updated_at=now() WHERE id=$1", id, role)
	return preparedChange{id: id, action: auditRole, role: role}, err
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
