// Stores background-generation jobs and enforces per-account quotas, idempotency, claims,
// expiry, and completion accounting.

package repo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// Postgres needs transactions: admission serializes each account inside one.
type Postgres struct{ db postgres.Pool }

func NewPostgres(db postgres.Pool) *Postgres { return &Postgres{db: db} }

// uuidPattern guards lookups so the uuid primary key is compared directly, and so a
// malformed ID reads as "not found" rather than a database cast error.
var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

const columns = `id,actor_id,input,profile_id,model_label,prompt_version,status,background,error_category,created_at,started_at,finished_at,expires_at,execution_seconds`

func scan(row interface{ Scan(...any) error }) (generation.Job, error) {
	var j generation.Job
	var raw []byte
	err := row.Scan(&j.ID, &j.ActorID, &raw, &j.ProfileID, &j.ModelLabel, &j.PromptVersion, &j.Status, &j.Background, &j.ErrorCategory, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.ExpiresAt, &j.ExecutionSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, generation.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(raw, &j.Input)
	return j, err
}
func (p *Postgres) begin(ctx context.Context, actor string) (pgx.Tx, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO faction_generation_accounts(actor_id) VALUES($1) ON CONFLICT DO NOTHING`, actor); err == nil {
		_, err = tx.Exec(ctx, `SELECT actor_id FROM faction_generation_accounts WHERE actor_id=$1 FOR UPDATE`, actor)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func expire(ctx context.Context, db postgres.DBTX, actor string) error {
	_, err := db.Exec(ctx, `UPDATE faction_background_generations SET status=CASE WHEN status='queued' THEN 'expired' ELSE 'failed' END,error_category=CASE WHEN status='queued' THEN 'queue_timeout' ELSE 'timeout' END,finished_at=now() WHERE ($1='' OR actor_id=$1) AND status IN ('queued','running') AND expires_at<=now()`, actor)
	return err
}
func quota(ctx context.Context, tx postgres.DBTX, actor string, c generation.Config) (generation.Options, error) {
	var o generation.Options
	var count int
	var last *time.Time
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT now(),last_admitted_at FROM faction_generation_accounts WHERE actor_id=$1`, actor).Scan(&now, &last)
	if err != nil {
		return o, err
	}
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	o.ResetAt = day.Add(24 * time.Hour)
	o.RetryAt = now
	err = tx.QueryRow(ctx, `SELECT count(*) FROM faction_background_generations WHERE actor_id=$1 AND created_at >=$2`, actor, day).Scan(&count)
	if err != nil {
		return o, err
	}
	o.Remaining = max(0, c.DailyAllowance-count)
	if last != nil && last.Add(c.Cooldown).After(o.RetryAt) {
		o.RetryAt = last.Add(c.Cooldown)
	}
	if o.Remaining == 0 && o.ResetAt.After(o.RetryAt) {
		o.RetryAt = o.ResetAt
	}
	var expiry time.Time
	err = tx.QueryRow(ctx, `SELECT id,expires_at FROM faction_background_generations WHERE actor_id=$1 AND status IN ('queued','running')`, actor).Scan(&o.ActiveJobID, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	} else if err == nil && expiry.After(o.RetryAt) {
		o.RetryAt = expiry
	}
	return o, err
}
func (p *Postgres) Options(ctx context.Context, actor string, c generation.Config) (generation.Options, error) {
	tx, err := p.begin(ctx, actor)
	if err != nil {
		return generation.Options{}, err
	}
	defer tx.Rollback(ctx)
	if err = expire(ctx, tx, actor); err != nil {
		return generation.Options{}, err
	}
	o, err := quota(ctx, tx, actor, c)
	if err != nil {
		return o, err
	}
	return o, tx.Commit(ctx)
}
func (p *Postgres) Admit(ctx context.Context, actor, key string, input generation.Input, c generation.Config, model generation.Admission) (generation.Job, generation.Options, error) {
	input, err := input.Normalized()
	if err != nil || strings.TrimSpace(key) == "" || len(key) > 200 || strings.TrimSpace(model.ProfileID) == "" || (model.Runtime != "api" && model.Runtime != "cli") {
		return generation.Job{}, generation.Options{}, generation.ErrInvalid
	}
	raw, _ := json.Marshal(input)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := p.begin(ctx, actor)
	if err != nil {
		return generation.Job{}, generation.Options{}, err
	}
	defer tx.Rollback(ctx)
	if err = expire(ctx, tx, actor); err != nil {
		return generation.Job{}, generation.Options{}, err
	}
	var storedHash string
	var id string
	err = tx.QueryRow(ctx, `SELECT id,input_hash FROM faction_background_generations WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&id, &storedHash)
	if err == nil {
		return existingAdmission(ctx, tx, id, hash, storedHash)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return generation.Job{}, generation.Options{}, err
	}
	o, err := admissionQuota(ctx, tx, actor, c)
	if err != nil {
		return generation.Job{}, o, err
	}
	id, err = newGenerationID()
	if err != nil {
		return generation.Job{}, o, err
	}
	j, err := scan(tx.QueryRow(ctx, `INSERT INTO faction_background_generations(id,actor_id,idempotency_key,input_hash,input,profile_id,model_label,prompt_version,status,expires_at,execution_seconds,runtime) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queued',now()+$9::interval,$10,$11) RETURNING `+columns, id, actor, key, hash, raw, model.ProfileID, model.ModelLabel, prompts.GenerationVersion, c.QueueTimeout.String(), max(1, int(c.ExecutionTimeout.Seconds())), model.Runtime))
	if err != nil {
		return j, o, admissionError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE faction_generation_accounts SET last_admitted_at=now() WHERE actor_id=$1`, actor); err != nil {
		return j, o, err
	}
	return j, o, tx.Commit(ctx)
}
func admissionQuota(ctx context.Context, tx pgx.Tx, actor string, c generation.Config) (generation.Options, error) {
	o, err := quota(ctx, tx, actor, c)
	if err != nil {
		return o, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return o, err
	}
	if o.Remaining == 0 || o.ActiveJobID != "" || o.RetryAt.After(now) {
		if err = tx.Commit(ctx); err != nil {
			return o, err
		}
		return o, generation.ErrLimited
	}
	return o, nil
}

func admissionError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "P0429" {
		return generation.ErrLimited
	}
	return err
}

func newGenerationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 15) | 64
	bytes[8] = (bytes[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

func (p *Postgres) Get(ctx context.Context, actor, id string) (generation.Job, error) {
	if !uuidPattern.MatchString(id) {
		return generation.Job{}, generation.ErrNotFound
	}
	if err := expire(ctx, p.db, actor); err != nil {
		return generation.Job{}, err
	}
	return scan(p.db.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id=$1 AND actor_id=$2`, id, actor))
}
func (p *Postgres) Context(ctx context.Context, id string) (generation.Job, error) {
	if !uuidPattern.MatchString(id) {
		return generation.Job{}, generation.ErrNotFound
	}
	return scan(p.db.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id=$1 AND status='running' AND expires_at>now()`, id))
}
func (p *Postgres) Claim(ctx context.Context, worker string, profiles []string) (generation.Job, error) {
	if err := expire(ctx, p.db, ""); err != nil {
		return generation.Job{}, err
	}
	return scan(p.db.QueryRow(ctx, `UPDATE faction_background_generations SET status='running',started_at=now(),expires_at=now()+execution_seconds*interval '1 second',worker_id=$1 WHERE id=(SELECT id FROM faction_background_generations WHERE status='queued' AND expires_at>now() AND profile_id=ANY($2) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+columns, worker, profiles))
}
func (p *Postgres) Submit(ctx context.Context, id, background string) (bool, error) {
	if !uuidPattern.MatchString(id) {
		return false, nil
	}
	background = strings.TrimSpace(background)
	if background == "" || utf8.RuneCountInString(background) > faction.MaxBackgroundLength {
		_, err := p.db.Exec(ctx, `UPDATE faction_background_generations SET submissions=submissions+1,status=CASE WHEN submissions>=2 THEN 'failed' ELSE status END,error_category=CASE WHEN submissions>=2 THEN 'invalid_output' ELSE error_category END,finished_at=CASE WHEN submissions>=2 THEN now() ELSE finished_at END WHERE id=$1 AND status='running' AND expires_at>now()`, id)
		if err != nil {
			return false, err
		}
		return false, generation.ErrInvalid
	}
	tag, err := p.db.Exec(ctx, `UPDATE faction_background_generations SET status='succeeded',background=$2,finished_at=COALESCE(finished_at,now()) WHERE id=$1 AND ((status='running' AND expires_at>now()) OR (status='succeeded' AND background=$2))`, id, background)
	return tag.RowsAffected() == 1, err
}
func (p *Postgres) Finish(ctx context.Context, id, category string, usage ai.Usage, metadata map[string]any) (bool, error) {
	if !uuidPattern.MatchString(id) {
		return false, nil
	}
	raw, _ := json.Marshal(usage)
	meta, _ := json.Marshal(metadata)
	if category == "" {
		category = "no_submission"
	}
	tag, err := p.db.Exec(ctx, `UPDATE faction_background_generations SET usage=$2,metadata=$3,model_label=COALESCE(NULLIF($3::jsonb->>'modelIdentifier',''),model_label),status=CASE WHEN status='running' THEN 'failed' ELSE status END,error_category=CASE WHEN status='running' THEN $4 ELSE error_category END,finished_at=COALESCE(finished_at,now()) WHERE id=$1 AND status IN ('running','succeeded','failed')`, id, raw, meta, category)
	return tag.RowsAffected() == 1, err
}

func existingAdmission(ctx context.Context, tx pgx.Tx, id, hash, storedHash string) (generation.Job, generation.Options, error) {
	if hash != storedHash {
		return generation.Job{}, generation.Options{}, generation.ErrConflict
	}
	j, e := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id=$1`, id))
	if e != nil {
		return j, generation.Options{}, e
	}
	return j, generation.Options{}, tx.Commit(ctx)
}
