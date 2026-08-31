package server

import "net/http"

// healthHandler reports that the service is up. It intentionally has no
// external dependencies (DB, etc.) so it always reflects process liveness.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
