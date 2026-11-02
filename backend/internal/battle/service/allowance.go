// Reports the caller's battle allowance from the counts the admission trigger enforces.

package service

import (
	"context"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

// Allowance is the caller's battle allowance when it was read. Admin battles skip the
// allowances, so an Admin's allowance is not limited and carries no limits.
type Allowance struct {
	Limited                bool      `json:"limited"`
	StartedToday           int       `json:"startedToday"`
	DailyLimit             int       `json:"dailyLimit,omitempty"`
	Unfinished             int       `json:"unfinished"`
	UnfinishedLimit        int       `json:"unfinishedLimit,omitempty"`
	ServiceUnfinished      int       `json:"serviceUnfinished"`
	ServiceUnfinishedLimit int       `json:"serviceUnfinishedLimit,omitempty"`
	ResetAt                time.Time `json:"resetAt"`
}

// AllowanceService reports the caller's battle allowance.
type AllowanceService interface {
	BattleAllowance(context.Context) (Allowance, error)
}

// BattleAllowance selects the allowances the way CreateBattle does: Tester for a Tester,
// none for an Admin, and Player for everyone else.
func (s *battleService) BattleAllowance(ctx context.Context) (Allowance, error) {
	allowance, err := s.battles.BattleAllowance(ctx, authn.Owner(ctx), hasRole(ctx, authn.Tester))
	if err != nil || !isAdmin(ctx) {
		return allowance, err
	}
	return Allowance{StartedToday: allowance.StartedToday, Unfinished: allowance.Unfinished, ServiceUnfinished: allowance.ServiceUnfinished, ResetAt: allowance.ResetAt}, nil
}
