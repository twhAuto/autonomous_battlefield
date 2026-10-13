// Package queue defines durable battle jobs and the ownership lease a worker holds
// while it runs one. The engine and the PostgreSQL adapter share these types, so
// neither depends on the other.
package queue

import (
	"errors"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var (
	ErrNoBattleAvailable = errors.New("no battle available")
	ErrLeaseLost         = errors.New("battle lease lost")
)

// Lease identifies one claimed attempt of a battle and the worker that owns it.
type Lease struct {
	BattleID string
	Attempt  int
	Owner    string
}

type Job struct {
	Lease          Lease
	Scenario       game.Scenario
	RequestedTicks uint64
	Traceparent    string
	Tracestate     string
}
