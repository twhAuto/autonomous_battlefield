package api

import "net/http"

func NewRouter(factionHandler *FactionHandler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /v1/factions/{id}/name", factionHandler.GetFactionName)
	mux.HandleFunc("POST /v1/factions", factionHandler.CreateFaction)

	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
