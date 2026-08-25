package worker

import (
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Pinger = httpapi.Pinger

func HealthHandler(worker *Worker, pinger Pinger) http.Handler {
	lifecycle := httpapi.NewLifecycleHandler()
	lifecycle.Activate(http.NotFoundHandler(), pinger, worker.Ready)
	return lifecycle
}
