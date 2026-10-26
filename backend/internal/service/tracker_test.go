package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gitsion/backend/internal/github"
	"gitsion/backend/internal/model"
	"gitsion/backend/internal/store"
)

func TestParseRepoRef(t *testing.T) {
	tests := []struct {
		input string
		owner string
		name  string
		ok    bool
	}{
		{"owner/repo", "owner", "repo", true},
		{"  Owner/Repo  ", "Owner", "Repo", true},
		{"github.com/owner/repo", "owner", "repo", true},
		{"https://github.com/owner/repo", "owner", "repo", true},
		{"https://github.com/owner/repo/", "owner", "repo", true},
		{"https://github.com/owner/repo.git", "owner", "repo", true},
		{"git@github.com:owner/repo.git", "owner", "repo", true},
		{"", "", "", false},
		{"owner", "", "", false},
		{"owner/repo/extra", "", "", false},
		{"owner//repo", "", "", false},
		{"owner/repo?tab=readme", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			owner, name, err := ParseRepoRef(tt.input)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatalf("expected error, got %q/%q", owner, name)
				}
				return
			}
			if owner != tt.owner || name != tt.name {
				t.Errorf("got %q/%q, want %q/%q", owner, name, tt.owner, tt.name)
			}
		})
	}
}

func TestTrack(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)

	repo, err := tr.Track(context.Background(), "https://github.com/Acme/Widget.git")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if repo.FullName != "Acme/Widget" {
		t.Errorf("full name = %q", repo.FullName)
	}
	if repo.Owner.LoginKey != "acme" {
		t.Errorf("owner key = %q", repo.Owner.LoginKey)
	}
	if repo.TrackedAt.IsZero() || repo.LastSyncedAt.IsZero() {
		t.Error("timestamps should be set")
	}

	stored, err := st.Get(context.Background(), "ACME", "WIDGET")
	if err != nil {
		t.Fatalf("stored repo: %v", err)
	}
	if stored.FullName != "Acme/Widget" {
		t.Errorf("stored full name = %q", stored.FullName)
	}

	if _, err := tr.Track(context.Background(), "acme/widget"); !errors.Is(err, ErrAlreadyTracked) {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestTrackErrors(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)

	if _, err := tr.Track(context.Background(), "nope"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("invalid ref error = %v", err)
	}

	gh.setError(fmt.Errorf("%w: gone", github.ErrNotFound))
	if _, err := tr.Track(context.Background(), "acme/missing"); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("github not found error = %v", err)
	}
}

func TestRefreshCooldown(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)
	if _, err := tr.Track(context.Background(), "acme/widget"); err != nil {
		t.Fatalf("Track: %v", err)
	}

	if _, err := tr.Refresh(context.Background(), "acme", "widget"); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	_, err := tr.Refresh(context.Background(), "acme", "widget")
	var cooldown *CooldownError
	if !errors.As(err, &cooldown) {
		t.Fatalf("second Refresh error = %v, want cooldown", err)
	}
	if cooldown.RetryAfter <= 0 || cooldown.RetryAfter > manualCooldown {
		t.Errorf("retry after = %v", cooldown.RetryAfter)
	}
}

func TestRefreshKeepsSnapshotOnError(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)
	tr.manualCooldown = 0

	if _, err := tr.Track(context.Background(), "acme/widget"); err != nil {
		t.Fatalf("Track: %v", err)
	}
	before, _ := st.Get(context.Background(), "acme", "widget")
	beforeTags := len(before.Tags)

	gh.setError(errors.New("network exploded"))
	if _, err := tr.Refresh(context.Background(), "acme", "widget"); err == nil {
		t.Fatal("expected refresh error")
	}

	after, err := st.Get(context.Background(), "acme", "widget")
	if err != nil {
		t.Fatalf("Get after failed refresh: %v", err)
	}
	if after.SyncError == "" {
		t.Error("SyncError should be recorded")
	}
	if len(after.Tags) != beforeTags {
		t.Errorf("tags changed from %d to %d", beforeTags, len(after.Tags))
	}
	if !after.LastSyncedAt.Equal(before.LastSyncedAt) {
		t.Error("last successful sync should not move on failure")
	}
}

func TestSyncAll(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)
	tr.concurrency = 2

	for _, name := range []string{"one", "two"} {
		if _, err := tr.Track(context.Background(), "acme/"+name); err != nil {
			t.Fatalf("Track %s: %v", name, err)
		}
	}

	synced, err := tr.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if synced != 2 {
		t.Errorf("synced = %d, want 2", synced)
	}
	if gh.callCount() != 4 {
		t.Errorf("GitHub calls = %d, want 4", gh.callCount())
	}

	repos, _ := st.List(context.Background(), "")
	for _, repo := range repos {
		if repo.LastSyncedAt.IsZero() {
			t.Errorf("%s was not synced", repo.FullName)
		}
	}
}

func TestOwners(t *testing.T) {
	st := newFakeStore()
	gh := newFakeGitHub()
	tr := newTestTracker(st, gh)

	for _, ref := range []string{"acme/one", "acme/two", "other/three"} {
		if _, err := tr.Track(context.Background(), ref); err != nil {
			t.Fatalf("Track %s: %v", ref, err)
		}
	}

	owners, err := tr.Owners(context.Background())
	if err != nil {
		t.Fatalf("Owners: %v", err)
	}
	if len(owners) != 2 {
		t.Fatalf("owners = %+v", owners)
	}
	if owners[0].Login != "acme" || owners[0].RepoCount != 2 {
		t.Errorf("first owner = %+v", owners[0])
	}
	if owners[1].Login != "other" || owners[1].RepoCount != 1 {
		t.Errorf("second owner = %+v", owners[1])
	}
}

func TestTriggerSyncAllCooldown(t *testing.T) {
	tr := newTestTracker(newFakeStore(), newFakeGitHub())
	tr.lastSyncAll = time.Now()
	err := tr.TriggerSyncAll()
	var cooldown *CooldownError
	if !errors.As(err, &cooldown) {
		t.Fatalf("error = %v, want cooldown", err)
	}
}

// --- fakes ------------------------------------------------------------------

func newTestTracker(st *fakeStore, gh *fakeGitHub) *Tracker {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := NewTracker(st, gh, logger, time.Hour, 2)
	tr.manualCooldown = manualCooldown
	return tr
}

type fakeStore struct {
	mu      sync.Mutex
	repos   map[string]*model.Repository
	pingErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{repos: make(map[string]*model.Repository)}
}

func (s *fakeStore) List(ctx context.Context, owner string) ([]model.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.repos))
	for key := range s.repos {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	repos := make([]model.Repository, 0, len(keys))
	for _, key := range keys {
		repo := s.repos[key]
		if owner != "" && repo.Owner.LoginKey != strings.ToLower(owner) {
			continue
		}
		repos = append(repos, *cloneRepo(repo))
	}
	return repos, nil
}

func (s *fakeStore) Get(ctx context.Context, owner, name string) (*model.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.repos[repoKey(owner, name)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneRepo(repo), nil
}

func (s *fakeStore) Create(ctx context.Context, repo *model.Repository) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := repoKey(repo.Owner.Login, repo.Name)
	if _, exists := s.repos[key]; exists {
		return store.ErrDuplicate
	}
	repo.Owner.LoginKey = strings.ToLower(repo.Owner.Login)
	s.repos[key] = cloneRepo(repo)
	return nil
}

func (s *fakeStore) ReplaceSnapshot(ctx context.Context, repo *model.Repository) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := repoKey(repo.Owner.Login, repo.Name)
	if _, exists := s.repos[key]; !exists {
		return store.ErrNotFound
	}
	s.repos[key] = cloneRepo(repo)
	return nil
}

func (s *fakeStore) Delete(ctx context.Context, owner, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := repoKey(owner, name)
	if _, exists := s.repos[key]; !exists {
		return false, nil
	}
	delete(s.repos, key)
	return true, nil
}

func (s *fakeStore) Ping(ctx context.Context) error { return s.pingErr }

type fakeGitHub struct {
	mu       sync.Mutex
	snapshot *model.Repository
	err      error
	calls    int
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{snapshot: &model.Repository{
		Description: "fake repo",
		Stars:       10,
		Tags:        []model.Tag{{Name: "v1.0.0", CommitSHA: "abc123"}},
		Issues:      []model.Issue{{Number: 1, Title: "An issue"}},
	}}
}

func (f *fakeGitHub) FetchSnapshot(ctx context.Context, owner, name string) (*model.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	repo := cloneRepo(f.snapshot)
	repo.FullName = owner + "/" + name
	repo.FullNameKey = strings.ToLower(repo.FullName)
	repo.Name = name
	repo.Owner = model.Owner{Login: owner, LoginKey: strings.ToLower(owner), Type: "User"}
	return repo, nil
}

func (f *fakeGitHub) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeGitHub) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Local helpers keep the tests independent from the Mongo implementation.
func cloneRepo(repo *model.Repository) *model.Repository {
	if repo == nil {
		return nil
	}
	clone := *repo
	clone.Topics = append([]string(nil), repo.Topics...)
	clone.Tags = append([]model.Tag(nil), repo.Tags...)
	clone.Issues = append([]model.Issue(nil), repo.Issues...)
	return &clone
}
