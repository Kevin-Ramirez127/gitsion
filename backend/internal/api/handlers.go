package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gitsion/backend/internal/github"
	"gitsion/backend/internal/model"
	"gitsion/backend/internal/service"
)

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorPayload{Code: code, Message: message}})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.tracker.Health(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repos, err := s.tracker.List(r.Context(), owner)
	if err != nil {
		s.log.Error("list repositories failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list repositories")
		return
	}
	if repos == nil {
		repos = []model.Repository{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
}

func (s *server) getRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.tracker.Get(r.Context(), r.PathValue("owner"), r.PathValue("name"))
	if err != nil {
		if errors.Is(err, service.ErrNotTracked) {
			writeError(w, http.StatusNotFound, "not_tracked", "repository is not tracked")
			return
		}
		s.log.Error("get repository failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load repository")
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

type trackRequest struct {
	Repository string `json:"repository"`
}

func (s *server) trackRepo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	defer r.Body.Close()

	var req trackRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be JSON with a repository field")
		return
	}

	repo, err := s.tracker.Track(r.Context(), req.Repository)
	if err != nil {
		s.writeTrackError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (s *server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	if _, err := s.tracker.Untrack(r.Context(), r.PathValue("owner"), r.PathValue("name")); err != nil {
		s.log.Error("untrack repository failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not remove repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) refreshRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.tracker.Refresh(r.Context(), r.PathValue("owner"), r.PathValue("name"))
	if err != nil {
		s.writeTrackError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *server) syncAll(w http.ResponseWriter, r *http.Request) {
	if err := s.tracker.TriggerSyncAll(); err != nil {
		s.writeTrackError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
}

func (s *server) listOwners(w http.ResponseWriter, r *http.Request) {
	owners, err := s.tracker.Owners(r.Context())
	if err != nil {
		s.log.Error("list owners failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list owners")
		return
	}
	if owners == nil {
		owners = []model.OwnerSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"owners": owners})
}

// writeTrackError maps tracker and GitHub failures to API responses.
func (s *server) writeTrackError(w http.ResponseWriter, err error) {
	var cooldown *service.CooldownError
	if errors.As(err, &cooldown) {
		seconds := int(cooldown.RetryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "cooldown", cooldown.Error())
		return
	}

	var rateLimit *github.RateLimitError
	if errors.As(err, &rateLimit) {
		retry := int(time.Until(rateLimit.ResetAt).Seconds())
		if retry < 1 {
			retry = 60
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeError(w, http.StatusTooManyRequests, "github_rate_limited", rateLimit.Error())
		return
	}

	switch {
	case errors.Is(err, service.ErrInvalidRef):
		writeError(w, http.StatusUnprocessableEntity, "invalid_repository",
			"use owner/name, github.com/owner/name or a GitHub URL")
	case errors.Is(err, service.ErrAlreadyTracked):
		writeError(w, http.StatusConflict, "already_tracked", "repository is already tracked")
	case errors.Is(err, service.ErrNotTracked):
		writeError(w, http.StatusNotFound, "not_tracked", "repository is not tracked")
	case errors.Is(err, service.ErrRepoNotFound):
		writeError(w, http.StatusNotFound, "repository_not_found",
			"repository was not found, or it is private and no usable GITHUB_TOKEN is configured")
	case errors.Is(err, github.ErrUnauthorized):
		s.log.Error("github rejected credentials", "error", err)
		writeError(w, http.StatusBadGateway, "github_auth_failed", "GitHub rejected the configured token")
	default:
		s.log.Error("github sync failed", "error", err)
		writeError(w, http.StatusBadGateway, "github_error", fmt.Sprintf("GitHub request failed: %v", err))
	}
}
