// Package server sets up the HTTP transport layer for the application.
package server

import (
	"net/http"
	"time"
)

// New builds an *http.Server listening on addr with all routes registered.
func New(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
