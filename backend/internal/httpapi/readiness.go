package httpapi

import (
	"context"
	"time"
)

const readinessTimeout = time.Second

// Pinger is the dependency health contract used by LifecycleHandler.
type Pinger interface {
	Ping(context.Context) error
}
