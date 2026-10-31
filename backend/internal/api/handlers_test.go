package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitsion/backend/internal/github"
	"gitsion/backend/internal/model"
	"gitsion/backend/internal/service"
)

func TestHealth(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "ok", status: http.StatusOK},
		{name: "degraded", err: errors.New("mongo down"), status: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestRouter(&fakeTracker{healthFn: func(context.Context) error { return tt.err }})
			rec := do(t, handler, http.MethodGet, "/healthz", "")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
		})
	}
}

func TestDashboardIsPublic(t *testing.T) {
	handler := newTestRouter(&fakeTracker{})
	rec := do(t, handler, http.MethodGet, "/api/v1/repos", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without credentials", rec.Code)
	}
}

func TestMutationsRequireAdminToken(t *testing.T) {
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/repos", `{"repository":"acme/widget"}`},
		{http.MethodDelete, "/api/v1/repos/acme/widget", ""},
		{http.MethodPost, "/api/v1/repos/acme/widget/refresh", ""},
		{http.MethodPost, "/api/v1/sync", ""},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			handler := newTestRouter(&fakeTracker{})
			rec := doWithToken(t, handler, route.method, route.path, route.body, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("WWW-Authenticate"); got == "" {
				t.Error("WWW-Authenticate header is missing")
			}
			var body errorEnvelope
			decode(t, rec, &body)
			if body.Error.Code != "unauthorized" {
				t.Errorf("code = %q, want unauthorized", body.Error.Code)
			}
		})
	}
}

func TestMutationsRejectWrongAdminToken(t *testing.T) {
	handler := newTestRouter(&fakeTracker{})
	rec := doWithToken(t, handler, http.MethodPost, "/api/v1/repos", `{"repository":"acme/widget"}`, "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestReadOnlyModeDisablesMutations(t *testing.T) {
	handler := newTestRouterWithToken(&fakeTracker{}, "")
	rec := doWithToken(t, handler, http.MethodPost, "/api/v1/repos", `{"repository":"acme/widget"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body errorEnvelope
	decode(t, rec, &body)
	if body.Error.Code != "writes_disabled" {
		t.Errorf("code = %q, want writes_disabled", body.Error.Code)
	}

	rec = doWithToken(t, handler, http.MethodGet, "/api/v1/repos", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public read status = %d, want 200", rec.Code)
	}
}

func TestAdminSessionProbe(t *testing.T) {
	handler := newTestRouter(&fakeTracker{})

	rec := doWithToken(t, handler, http.MethodGet, "/api/v1/admin/session", "", testAdminToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid token status = %d, want 204", rec.Code)
	}

	rec = doWithToken(t, handler, http.MethodGet, "/api/v1/admin/session", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rec.Code)
	}
}

func TestListRepos(t *testing.T) {
	handler := newTestRouter(&fakeTracker{
		listFn: func(_ context.Context, owner string) ([]model.Repository, error) {
			if owner != "acme" {
				t.Errorf("owner = %q", owner)
			}
			return []model.Repository{{FullName: "acme/widget"}}, nil
		},
	})
	rec := do(t, handler, http.MethodGet, "/api/v1/repos?owner=acme", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Repositories []model.Repository `json:"repositories"`
	}
	decode(t, rec, &body)
	if len(body.Repositories) != 1 || body.Repositories[0].FullName != "acme/widget" {
		t.Errorf("body = %+v", body)
	}
}

func TestListReposEmptyIsArray(t *testing.T) {
	handler := newTestRouter(&fakeTracker{
		listFn: func(context.Context, string) ([]model.Repository, error) {
			return nil, nil
		},
	})
	rec := do(t, handler, http.MethodGet, "/api/v1/repos", "")
	if strings.TrimSpace(rec.Body.String()) != `{"repositories":[]}` {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestTrackRepo(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		trackErr   error
		wantStatus int
		wantCode   string
	}{
		{name: "success", body: `{"repository":"acme/widget"}`, wantStatus: http.StatusCreated},
		{name: "bad json", body: `{`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{name: "unknown field", body: `{"repo":"acme/widget"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{name: "invalid ref", body: `{"repository":"nope"}`, trackErr: service.ErrInvalidRef, wantStatus: http.StatusUnprocessableEntity, wantCode: "invalid_repository"},
		{name: "duplicate", body: `{"repository":"acme/widget"}`, trackErr: service.ErrAlreadyTracked, wantStatus: http.StatusConflict, wantCode: "already_tracked"},
		{name: "github missing", body: `{"repository":"acme/missing"}`, trackErr: service.ErrRepoNotFound, wantStatus: http.StatusNotFound, wantCode: "repository_not_found"},
		{name: "private repo", body: `{"repository":"acme/secret"}`, trackErr: service.ErrPrivateRepo, wantStatus: http.StatusUnprocessableEntity, wantCode: "private_repository"},
		{name: "github unauthorized", body: `{"repository":"acme/private"}`, trackErr: github.ErrUnauthorized, wantStatus: http.StatusBadGateway, wantCode: "github_auth_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestRouter(&fakeTracker{
				trackFn: func(context.Context, string) (*model.Repository, error) {
					if tt.trackErr != nil {
						return nil, tt.trackErr
					}
					return &model.Repository{FullName: "acme/widget"}, nil
				},
			})
			rec := do(t, handler, http.MethodPost, "/api/v1/repos", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode != "" {
				var body errorEnvelope
				decode(t, rec, &body)
				if body.Error.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestGetRepoNotFound(t *testing.T) {
	handler := newTestRouter(&fakeTracker{
		getFn: func(context.Context, string, string) (*model.Repository, error) {
			return nil, service.ErrNotTracked
		},
	})
	rec := do(t, handler, http.MethodGet, "/api/v1/repos/acme/widget", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var body errorEnvelope
	decode(t, rec, &body)
	if body.Error.Code != "not_tracked" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestDeleteRepo(t *testing.T) {
	called := false
	handler := newTestRouter(&fakeTracker{
		untrackFn: func(_ context.Context, owner, name string) (bool, error) {
			called = true
			if owner != "acme" || name != "widget" {
				t.Errorf("path = %q/%q", owner, name)
			}
			return true, nil
		},
	})
	rec := do(t, handler, http.MethodDelete, "/api/v1/repos/acme/widget", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if !called {
		t.Error("untrack was not called")
	}
}

func TestRefreshErrorMapping(t *testing.T) {
	reset := time.Now().Add(90 * time.Second)

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		retryAfter bool
	}{
		{
			name:       "cooldown",
			err:        &service.CooldownError{RetryAfter: 12 * time.Second},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "cooldown",
			retryAfter: true,
		},
		{
			name:       "github rate limit",
			err:        &github.RateLimitError{ResetAt: reset},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "github_rate_limited",
			retryAfter: true,
		},
		{
			name:       "not tracked",
			err:        service.ErrNotTracked,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_tracked",
		},
		{
			name:       "github missing",
			err:        service.ErrRepoNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "repository_not_found",
		},
		{
			name:       "private repository",
			err:        service.ErrPrivateRepo,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "private_repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestRouter(&fakeTracker{
				refreshFn: func(context.Context, string, string) (*model.Repository, error) {
					return nil, tt.err
				},
			})
			rec := do(t, handler, http.MethodPost, "/api/v1/repos/acme/widget/refresh", "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			var body errorEnvelope
			decode(t, rec, &body)
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
			if tt.retryAfter && rec.Header().Get("Retry-After") == "" {
				t.Error("Retry-After header is missing")
			}
		})
	}
}

func TestSyncAll(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "scheduled", wantStatus: http.StatusAccepted},
		{name: "cooldown", err: &service.CooldownError{RetryAfter: time.Second}, wantStatus: http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestRouter(&fakeTracker{
				syncAllFn: func() error { return tt.err },
			})
			rec := do(t, handler, http.MethodPost, "/api/v1/sync", "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOwners(t *testing.T) {
	handler := newTestRouter(&fakeTracker{
		ownersFn: func(context.Context) ([]model.OwnerSummary, error) {
			return []model.OwnerSummary{{Login: "acme", RepoCount: 2}}, nil
		},
	})
	rec := do(t, handler, http.MethodGet, "/api/v1/owners", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Owners []model.OwnerSummary `json:"owners"`
	}
	decode(t, rec, &body)
	if len(body.Owners) != 1 || body.Owners[0].RepoCount != 2 {
		t.Errorf("owners = %+v", body.Owners)
	}
}

func TestUnknownRoute(t *testing.T) {
	handler := newTestRouter(&fakeTracker{})
	rec := do(t, handler, http.MethodGet, "/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var body errorEnvelope
	decode(t, rec, &body)
	if body.Error.Code != "not_found" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

// --- helpers ----------------------------------------------------------------

const testAdminToken = "test-admin-token"

func newTestRouter(tracker Tracker) http.Handler {
	return newTestRouterWithToken(tracker, testAdminToken)
}

func newTestRouterWithToken(tracker Tracker, token string) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(tracker, logger, token)
}

func do(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doWithToken(t, handler, method, path, body, testAdminToken)
}

func doWithToken(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
}

type fakeTracker struct {
	listFn    func(context.Context, string) ([]model.Repository, error)
	getFn     func(context.Context, string, string) (*model.Repository, error)
	ownersFn  func(context.Context) ([]model.OwnerSummary, error)
	trackFn   func(context.Context, string) (*model.Repository, error)
	untrackFn func(context.Context, string, string) (bool, error)
	refreshFn func(context.Context, string, string) (*model.Repository, error)
	syncAllFn func() error
	healthFn  func(context.Context) error
}

func (f *fakeTracker) List(ctx context.Context, owner string) ([]model.Repository, error) {
	if f.listFn != nil {
		return f.listFn(ctx, owner)
	}
	return []model.Repository{}, nil
}

func (f *fakeTracker) Get(ctx context.Context, owner, name string) (*model.Repository, error) {
	if f.getFn != nil {
		return f.getFn(ctx, owner, name)
	}
	return &model.Repository{FullName: owner + "/" + name}, nil
}

func (f *fakeTracker) Owners(ctx context.Context) ([]model.OwnerSummary, error) {
	if f.ownersFn != nil {
		return f.ownersFn(ctx)
	}
	return []model.OwnerSummary{}, nil
}

func (f *fakeTracker) Track(ctx context.Context, input string) (*model.Repository, error) {
	if f.trackFn != nil {
		return f.trackFn(ctx, input)
	}
	return &model.Repository{FullName: input}, nil
}

func (f *fakeTracker) Untrack(ctx context.Context, owner, name string) (bool, error) {
	if f.untrackFn != nil {
		return f.untrackFn(ctx, owner, name)
	}
	return true, nil
}

func (f *fakeTracker) Refresh(ctx context.Context, owner, name string) (*model.Repository, error) {
	if f.refreshFn != nil {
		return f.refreshFn(ctx, owner, name)
	}
	return &model.Repository{FullName: owner + "/" + name}, nil
}

func (f *fakeTracker) TriggerSyncAll() error {
	if f.syncAllFn != nil {
		return f.syncAllFn()
	}
	return nil
}

func (f *fakeTracker) Health(ctx context.Context) error {
	if f.healthFn != nil {
		return f.healthFn(ctx)
	}
	return nil
}
