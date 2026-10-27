// Reads the decision-window replay checkpoint that starts the segment containing a tick.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

// GetReplayFrame returns the attempt's latest checkpoint at or before tick, or
// ErrReplayUnavailable when the attempt saved none.
func (r *Postgres) GetReplayFrame(ctx context.Context, battleID string, attempt int, tick uint64) (battleservice.StoredReplayFrame, error) {
	var frame battleservice.StoredReplayFrame
	var checkpoint, events []byte
	err := r.db.QueryRow(ctx, `
		SELECT tick, checkpoint, events
		FROM battle_replay_frames
		WHERE battle_id = $1 AND attempt = $2 AND tick <= $3
		ORDER BY tick DESC
		LIMIT 1
	`, battleID, attempt, tick).Scan(&frame.Tick, &checkpoint, &events)
	if errors.Is(err, pgx.ErrNoRows) {
		return frame, battleservice.ErrReplayUnavailable
	}
	if err != nil {
		return frame, fmt.Errorf("get battle %q replay frame at tick %d: %w", battleID, tick, err)
	}
	if err := json.Unmarshal(checkpoint, &frame.Checkpoint); err != nil {
		return frame, fmt.Errorf("decode battle %q replay checkpoint: %w", battleID, err)
	}
	if err := json.Unmarshal(events, &frame.Events); err != nil {
		return frame, fmt.Errorf("decode battle %q replay events: %w", battleID, err)
	}
	return frame, nil
}
