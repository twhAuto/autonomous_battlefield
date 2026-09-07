// Stores background-generation jobs and enforces per-account quotas, idempotency, claims,
// expiry, and completion accounting.

package generation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct{ DB postgres.DBTX }

const columns = `id,actor_id,input,profile_id,model_label,prompt_version,status,background,error_category,created_at,started_at,finished_at,expires_at,execution_seconds`

func scan(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var raw []byte
	err := row.Scan(&j.ID, &j.ActorID, &raw, &j.ProfileID, &j.ModelLabel, &j.PromptVersion, &j.Status, &j.Background, &j.ErrorCategory, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.ExpiresAt, &j.ExecutionSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(raw, &j.Input)
	return j, err
}
func (p *Postgres) begin(ctx context.Context, actor string) (pgx.Tx, error) {
	db, ok := p.DB.(interface {
		Begin(context.Context) (pgx.Tx, error)
	})
	if !ok {
		return nil, errors.New("generation requires transactional database")
	}
	tx, err := db.Begin(ctx)
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
func quota(ctx context.Context, tx postgres.DBTX, actor string, c Config) (Options, error) {
	var o Options
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
	err = tx.QueryRow(ctx, `SELECT count(*) FROM faction_background_generations WHERE actor_id=$1 AND created_at >=$2 AND status<>'expired'`, actor, day).Scan(&count)
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
func (p *Postgres) Options(ctx context.Context, actor string, c Config) (Options, error) {
	tx, err := p.begin(ctx, actor)
	if err != nil {
		return Options{}, err
	}
	defer tx.Rollback(ctx)
	if err = expire(ctx, tx, actor); err != nil {
		return Options{}, err
	}
	o, err := quota(ctx, tx, actor, c)
	if err != nil {
		return o, err
	}
	return o, tx.Commit(ctx)
}
func (p *Postgres) Admit(ctx context.Context, actor, key string, input Input, c Config, label string) (Job, Options, error) {
	input, err := input.Normalized()
	if err != nil || strings.TrimSpace(key) == "" || len(key) > 200 {
		return Job{}, Options{}, ErrInvalid
	}
	raw, _ := json.Marshal(input)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := p.begin(ctx, actor)
	if err != nil {
		return Job{}, Options{}, err
	}
	defer tx.Rollback(ctx)
	if err = expire(ctx, tx, actor); err != nil {
		return Job{}, Options{}, err
	}
	var storedHash string
	var id string
	err = tx.QueryRow(ctx, `SELECT id,input_hash FROM faction_background_generations WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&id, &storedHash)
	if err == nil {
		return existingAdmission(ctx, tx, id, hash, storedHash)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Options{}, err
	}
	o, err := quota(ctx, tx, actor, c)
	if err != nil {
		return Job{}, o, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Job{}, o, err
	}
	if o.Remaining == 0 || o.ActiveJobID != "" || o.RetryAt.After(now) {
		if err = tx.Commit(ctx); err != nil {
			return Job{}, o, err
		}
		return Job{}, o, ErrLimited
	}
	bytes := make([]byte, 16)
	if _, err = rand.Read(bytes); err != nil {
		return Job{}, o, err
	}
	bytes[6] = (bytes[6] & 15) | 64
	bytes[8] = (bytes[8] & 63) | 128
	id = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
	j, err := scan(tx.QueryRow(ctx, `INSERT INTO faction_background_generations(id,actor_id,idempotency_key,input_hash,input,profile_id,model_label,prompt_version,status,expires_at,execution_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queued',now()+$9::interval,$10) RETURNING `+columns, id, actor, key, hash, raw, c.ProfileID, label, ai.GenerationPromptVersion, c.QueueTimeout.String(), max(1, int(c.ExecutionTimeout.Seconds()))))
	if err != nil {
		return j, o, err
	}
	if _, err = tx.Exec(ctx, `UPDATE faction_generation_accounts SET last_admitted_at=now() WHERE actor_id=$1`, actor); err != nil {
		return j, o, err
	}
	return j, o, tx.Commit(ctx)
}
func (p *Postgres) Get(ctx context.Context, actor, id string) (Job, error) {
	if err := expire(ctx, p.DB, actor); err != nil {
		return Job{}, err
	}
	return scan(p.DB.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id::text=$1 AND actor_id=$2`, id, actor))
}
func (p *Postgres) Context(ctx context.Context, id string) (Job, error) {
	return scan(p.DB.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id::text=$1 AND status='running' AND expires_at>now()`, id))
}
func (p *Postgres) Claim(ctx context.Context, worker string, profiles []string) (Job, error) {
	if err := expire(ctx, p.DB, ""); err != nil {
		return Job{}, err
	}
	return scan(p.DB.QueryRow(ctx, `UPDATE faction_background_generations SET status='running',started_at=now(),expires_at=now()+execution_seconds*interval '1 second',worker_id=$1 WHERE id=(SELECT id FROM faction_background_generations WHERE status='queued' AND expires_at>now() AND profile_id=ANY($2) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+columns, worker, profiles))
}
func (p *Postgres) Submit(ctx context.Context, id, background string) (bool, error) {
	background = strings.TrimSpace(background)
	if background == "" || utf8.RuneCountInString(background) > faction.MaxBackgroundLength {
		_, err := p.DB.Exec(ctx, `UPDATE faction_background_generations SET submissions=submissions+1,status=CASE WHEN submissions>=2 THEN 'failed' ELSE status END,error_category=CASE WHEN submissions>=2 THEN 'invalid_output' ELSE error_category END,finished_at=CASE WHEN submissions>=2 THEN now() ELSE finished_at END WHERE id::text=$1 AND status='running' AND expires_at>now()`, id)
		if err != nil {
			return false, err
		}
		return false, ErrInvalid
	}
	tag, err := p.DB.Exec(ctx, `UPDATE faction_background_generations SET status='succeeded',background=$2,finished_at=COALESCE(finished_at,now()) WHERE id::text=$1 AND ((status='running' AND expires_at>now()) OR (status='succeeded' AND background=$2))`, id, background)
	return tag.RowsAffected() == 1, err
}
func (p *Postgres) Finish(ctx context.Context, id, category string, usage ai.Usage, metadata map[string]any) (bool, error) {
	raw, _ := json.Marshal(usage)
	meta, _ := json.Marshal(metadata)
	if category == "" {
		category = "no_submission"
	}
	tag, err := p.DB.Exec(ctx, `UPDATE faction_background_generations SET usage=$2,metadata=$3,model_label=COALESCE(NULLIF($3::jsonb->>'modelIdentifier',''),model_label),status=CASE WHEN status='running' THEN 'failed' ELSE status END,error_category=CASE WHEN status='running' THEN $4 ELSE error_category END,finished_at=COALESCE(finished_at,now()) WHERE id::text=$1 AND status IN ('running','succeeded','failed')`, id, raw, meta, category)
	return tag.RowsAffected() == 1, err
}

func existingAdmission(ctx context.Context, tx pgx.Tx, id, hash, storedHash string) (Job, Options, error) {
	if hash != storedHash {
		return Job{}, Options{}, ErrConflict
	}
	j, e := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM faction_background_generations WHERE id=$1`, id))
	if e != nil {
		return j, Options{}, e
	}
	return j, Options{}, tx.Commit(ctx)
}
