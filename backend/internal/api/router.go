// Package api exposes the dashboard HTTP API.
package api

import (
	"context"
	"log/slog"
	"net/http"

	"gitsion/backend/internal/model"
)

// Tracker is the service surface the HTTP layer needs.
type Tracker interface {
	List(ctx context.Context, owner string) ([]model.Repository, error)
	Get(ctx context.Context, owner, name string) (*model.Repository, error)
	Owners(ctx context.Context) ([]model.OwnerSummary, error)
	Track(ctx context.Context, input string) (*model.Repository, error)
	Untrack(ctx context.Context, owner, name string) (bool, error)
	Refresh(ctx context.Context, owner, name string) (*model.Repository, error)
	TriggerSyncAll() error
	Health(ctx context.Context) error
}

// NewRouter builds the complete handler tree. The dashboard is intentionally
// unauthenticated: it only displays public repository data.
func NewRouter(tracker Tracker, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	s := &server{tracker: tracker, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/repos", s.listRepos)
	mux.HandleFunc("POST /api/v1/repos", s.trackRepo)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{name}", s.getRepo)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{name}", s.deleteRepo)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{name}/refresh", s.refreshRepo)
	mux.HandleFunc("GET /api/v1/owners", s.listOwners)
	mux.HandleFunc("POST /api/v1/sync", s.syncAll)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})

	return withRequestLogging(log, withRecovery(log, withSecurityHeaders(mux)))
}
