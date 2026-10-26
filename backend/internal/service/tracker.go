// Package service orchestrates tracking, caching and background syncing of
// GitHub repositories.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"gitsion/backend/internal/github"
	"gitsion/backend/internal/model"
	"gitsion/backend/internal/store"
)

var (
	// ErrInvalidRef is returned for unparseable owner/name input.
	ErrInvalidRef = errors.New("invalid repository reference")
	// ErrAlreadyTracked is returned when a repository is already on the list.
	ErrAlreadyTracked = errors.New("repository is already tracked")
	// ErrNotTracked is returned when a repository is not on the dashboard.
	ErrNotTracked = errors.New("repository is not tracked")
	// ErrRepoNotFound means GitHub could not see the repository.
	ErrRepoNotFound = errors.New("repository not found on GitHub")
)

const (
	manualCooldown  = 20 * time.Second
	syncAllCooldown = 60 * time.Second
	syncTimeout     = 5 * time.Minute
	maxSyncErrorLen = 400
)

// CooldownError indicates that a manual refresh happened too recently.
type CooldownError struct {
	RetryAfter time.Duration
}

func (e *CooldownError) Error() string {
	seconds := int(e.RetryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("refresh is cooling down, retry in %ds", seconds)
}

// GitHubClient is the part of the GitHub client the tracker needs.
type GitHubClient interface {
	FetchSnapshot(ctx context.Context, owner, name string) (*model.Repository, error)
}

// Tracker owns the lifecycle of tracked repositories.
type Tracker struct {
	store          store.RepositoryStore
	github         GitHubClient
	log            *slog.Logger
	syncInterval   time.Duration
	concurrency    int
	manualCooldown time.Duration
	group          singleflight.Group

	mu          sync.Mutex
	lastManual  map[string]time.Time
	lastSyncAll time.Time
}

// NewTracker wires a tracker. syncInterval controls the background loop.
func NewTracker(st store.RepositoryStore, gh GitHubClient, log *slog.Logger, syncInterval time.Duration, concurrency int) *Tracker {
	if log == nil {
		log = slog.Default()
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return &Tracker{
		store:          st,
		github:         gh,
		log:            log,
		syncInterval:   syncInterval,
		concurrency:    concurrency,
		manualCooldown: manualCooldown,
		lastManual:     make(map[string]time.Time),
	}
}

// Start launches the initial sync and the periodic refresh loop.
func (t *Tracker) Start(ctx context.Context) {
	go func() {
		// Give the HTTP server a moment to come up, then warm the cache.
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		t.syncAllLogged(ctx)

		ticker := time.NewTicker(t.syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				t.log.Info("background sync stopped")
				return
			case <-ticker.C:
				t.syncAllLogged(ctx)
			}
		}
	}()
}

func (t *Tracker) syncAllLogged(ctx context.Context) {
	n, err := t.SyncAll(ctx)
	if err != nil {
		t.log.Warn("background sync finished with errors", "synced", n, "error", err)
		return
	}
	t.log.Info("background sync finished", "synced", n)
}

// List returns tracked repositories, optionally filtered by owner.
func (t *Tracker) List(ctx context.Context, owner string) ([]model.Repository, error) {
	repos, err := t.store.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	if repos == nil {
		repos = []model.Repository{}
	}
	for i := range repos {
		normalize(&repos[i])
	}
	return repos, nil
}

// Get returns a single tracked repository.
func (t *Tracker) Get(ctx context.Context, owner, name string) (*model.Repository, error) {
	repo, err := t.store.Get(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotTracked
	}
	if err != nil {
		return nil, err
	}
	normalize(repo)
	return repo, nil
}

// Owners aggregates tracked repositories by owner.
func (t *Tracker) Owners(ctx context.Context) ([]model.OwnerSummary, error) {
	repos, err := t.store.List(ctx, "")
	if err != nil {
		return nil, err
	}
	byLogin := make(map[string]model.OwnerSummary)
	for _, repo := range repos {
		loginKey := repo.Owner.LoginKey
		if loginKey == "" {
			loginKey = strings.ToLower(repo.Owner.Login)
		}
		summary, ok := byLogin[loginKey]
		if !ok {
			summary = model.OwnerSummary{
				Login:     repo.Owner.Login,
				Type:      repo.Owner.Type,
				AvatarURL: repo.Owner.AvatarURL,
				HTMLURL:   repo.Owner.HTMLURL,
			}
		}
		summary.RepoCount++
		byLogin[loginKey] = summary
	}
	owners := make([]model.OwnerSummary, 0, len(byLogin))
	for _, owner := range byLogin {
		owners = append(owners, owner)
	}
	sort.Slice(owners, func(i, j int) bool {
		return strings.ToLower(owners[i].Login) < strings.ToLower(owners[j].Login)
	})
	return owners, nil
}

// Track validates, fetches and stores a new repository.
func (t *Tracker) Track(ctx context.Context, input string) (*model.Repository, error) {
	owner, name, err := ParseRepoRef(input)
	if err != nil {
		return nil, err
	}

	snapshot, err := t.github.FetchSnapshot(ctx, owner, name)
	if err != nil {
		return nil, mapGitHubError(err)
	}

	now := time.Now().UTC()
	snapshot.TrackedAt = now
	snapshot.LastSyncedAt = now
	snapshot.LastSyncAttempt = now
	snapshot.SyncError = ""

	if err := t.store.Create(ctx, snapshot); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, ErrAlreadyTracked
		}
		return nil, err
	}
	t.log.Info("repository tracked", "repo", snapshot.FullName)
	return snapshot, nil
}

// Untrack removes a repository. It reports whether something was deleted.
func (t *Tracker) Untrack(ctx context.Context, owner, name string) (bool, error) {
	deleted, err := t.store.Delete(ctx, owner, name)
	if err != nil {
		return false, err
	}
	if deleted {
		t.log.Info("repository untracked", "repo", owner+"/"+name)
	}
	return deleted, nil
}

// Refresh fetches a fresh snapshot for one repository, respecting a short
// per-repository cooldown.
func (t *Tracker) Refresh(ctx context.Context, owner, name string) (*model.Repository, error) {
	if _, err := t.store.Get(ctx, owner, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotTracked
		}
		return nil, err
	}

	key := repoKey(owner, name)
	now := time.Now()
	t.mu.Lock()
	if last, ok := t.lastManual[key]; ok {
		if remaining := t.manualCooldown - now.Sub(last); remaining > 0 {
			t.mu.Unlock()
			return nil, &CooldownError{RetryAfter: remaining}
		}
	}
	t.lastManual[key] = now
	t.mu.Unlock()

	repo, err := t.syncRepo(ctx, owner, name)
	if err != nil {
		return nil, mapGitHubError(err)
	}
	return repo, nil
}

// TriggerSyncAll checks the global cooldown and starts a full sync in the
// background. The API returns immediately.
func (t *Tracker) TriggerSyncAll() error {
	now := time.Now()
	t.mu.Lock()
	if !t.lastSyncAll.IsZero() {
		if remaining := syncAllCooldown - now.Sub(t.lastSyncAll); remaining > 0 {
			t.mu.Unlock()
			return &CooldownError{RetryAfter: remaining}
		}
	}
	t.lastSyncAll = now
	t.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		t.syncAllLogged(ctx)
	}()
	return nil
}

// SyncAll refreshes every tracked repository using a small worker pool.
func (t *Tracker) SyncAll(ctx context.Context) (int, error) {
	repos, err := t.store.List(ctx, "")
	if err != nil {
		return 0, err
	}

	sem := make(chan struct{}, t.concurrency)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		synced  int
		errList []error
	)

	for _, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if _, err := t.syncRepo(ctx, repo.Owner.Login, repo.Name); err != nil {
				t.log.Warn("sync failed", "repo", repo.FullName, "error", err)
				mu.Lock()
				errList = append(errList, fmt.Errorf("%s: %w", repo.FullName, err))
				mu.Unlock()
				return
			}
			mu.Lock()
			synced++
			mu.Unlock()
		}()
	}
	wg.Wait()

	return synced, errors.Join(errList...)
}

// syncRepo refreshes a single repository. singleflight collapses concurrent
// refreshes of the same repository into one GitHub request set.
func (t *Tracker) syncRepo(ctx context.Context, owner, name string) (*model.Repository, error) {
	value, err, _ := t.group.Do("sync:"+repoKey(owner, name), func() (any, error) {
		existing, err := t.store.Get(ctx, owner, name)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotTracked
		}
		if err != nil {
			return nil, err
		}

		snapshot, fetchErr := t.github.FetchSnapshot(ctx, owner, name)
		if fetchErr != nil {
			// Keep the last good snapshot, but surface the failure in the UI.
			existing.LastSyncAttempt = time.Now().UTC()
			existing.SyncError = truncate(fetchErr.Error(), maxSyncErrorLen)
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if saveErr := t.store.ReplaceSnapshot(persistCtx, existing); saveErr != nil {
				t.log.Warn("could not persist sync error", "repo", existing.FullName, "error", saveErr)
			}
			return nil, fetchErr
		}

		now := time.Now().UTC()
		snapshot.TrackedAt = existing.TrackedAt
		snapshot.LastSyncAttempt = now
		snapshot.LastSyncedAt = now
		snapshot.SyncError = ""
		if err := t.store.ReplaceSnapshot(ctx, snapshot); err != nil {
			return nil, err
		}
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	repo, ok := value.(*model.Repository)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected sync result type %T", value)
	}
	return repo, nil
}

// Health checks MongoDB connectivity.
func (t *Tracker) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return t.store.Ping(ctx)
}

// ParseRepoRef accepts owner/name, github.com/owner/name and full GitHub URLs.
func ParseRepoRef(raw string) (string, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return "", "", ErrInvalidRef
	}

	// git@github.com:owner/name.git
	if strings.HasPrefix(input, "git@") {
		if idx := strings.IndexAny(input, ":/"); idx >= 0 {
			input = input[idx+1:]
		}
	} else if strings.Contains(input, "://") {
		parsed, err := url.Parse(input)
		if err != nil {
			return "", "", ErrInvalidRef
		}
		input = parsed.Path
	} else {
		lower := strings.ToLower(input)
		if strings.HasPrefix(lower, "github.com/") {
			input = input[len("github.com/"):]
		}
	}

	input = strings.Trim(strings.TrimSpace(input), "/")
	input = strings.TrimSuffix(input, ".git")
	parts := strings.Split(input, "/")
	if len(parts) != 2 {
		return "", "", ErrInvalidRef
	}
	owner, name := parts[0], parts[1]
	if !validPathPart(owner) || !validPathPart(name) {
		return "", "", ErrInvalidRef
	}
	return owner, name, nil
}

var pathPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func validPathPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	return pathPartPattern.MatchString(part)
}

func mapGitHubError(err error) error {
	if errors.Is(err, github.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrRepoNotFound, err)
	}
	return err
}

func repoKey(owner, name string) string {
	return strings.ToLower(strings.TrimSpace(owner) + "/" + strings.TrimSpace(name))
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// normalize makes documents written by older versions safe for the JSON API
// and the templates (which expect arrays rather than null).
func normalize(repo *model.Repository) {
	if repo.Topics == nil {
		repo.Topics = []string{}
	}
	if repo.Tags == nil {
		repo.Tags = []model.Tag{}
	}
	if repo.Issues == nil {
		repo.Issues = []model.Issue{}
	}
}
