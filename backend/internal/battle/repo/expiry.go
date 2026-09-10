// Expires unfinished battles by wall-clock age, independently of pause and leases.
package repo

import (
	"context"
	"fmt"
)

func (r *Postgres) ExpireBattles(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `
 WITH expired AS (
  UPDATE battle_runs
  SET status = 'failed', paused = false, error_code = 'battle_timeout',
      error_message = 'Battle timed out after one hour, including queued and paused time.',
      lease_owner = NULL, lease_expires_at = NULL, completed_at = now(), updated_at = now()
  WHERE status IN ('queued', 'running') AND created_at <= now() - interval '1 hour'
  RETURNING id
 )
 UPDATE battle_decisions AS decision
 SET status = 'failed', error_category = 'battle_stopped',
     lease_owner = NULL, lease_expires_at = NULL, resolved_at = now()
 FROM expired
 WHERE decision.battle_id = expired.id AND decision.status IN ('pending', 'running')
 `)
	if err != nil {
		return fmt.Errorf("expire battles: %w", err)
	}
	return nil
}
