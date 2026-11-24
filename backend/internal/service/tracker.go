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
	// ErrPrivateRepo is returned when a repository is not public. The dashboard
	// is public, so only public repository data may be stored.
	ErrPrivateRepo = errors.New("only public repositories can be tracked")
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

		t.syncAllLogged(ctx, false)

		ticker := time.NewTicker(t.syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				t.log.Info("background sync stopped")
				return
			case <-ticker.C:
				t.syncAllLogged(ctx, false)
			}
		}
	}()
}

func (t *Tracker) syncAllLogged(ctx context.Context, force bool) {
	started := time.Now()
	n, err := t.syncAll(ctx, force)
	attrs := []any{"synced", n, "duration", time.Since(started), "forced", force}
	if client, ok := t.github.(interface{ Usage() github.Usage }); ok {
		usage := client.Usage()
		attrs = append(attrs, "github_requests_total", usage.Requests)
		if usage.RateLimitKnown {
			attrs = append(attrs, "github_rate_limit", usage.Limit,
				"github_rate_remaining", usage.Remaining, "github_rate_reset", usage.ResetAt)
		}
	}
	if err != nil {
		t.log.Warn("repository sync finished with errors", append(attrs, "error", err)...)
		return
	}
	t.log.Info("repository sync finished", attrs...)
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
	public := make([]model.Repository, 0, len(repos))
	for _, repo := range repos {
		if !isPublicRepository(repo) {
			continue
		}
		public = append(public, repo)
	}
	return public, nil
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
	if !isPublicRepository(*repo) {
		return nil, ErrNotTracked
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
		if !isPublicRepository(repo) {
			continue
		}
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
	if !isPublicRepository(*snapshot) {
		return nil, ErrPrivateRepo
	}

	now := time.Now().UTC()
	snapshot.TrackedAt = now
	snapshot.LastSyncedAt = now
	snapshot.LastSyncAttempt = now
	snapshot.SyncError = ""
	snapshot.NextSyncAt = t.nextSnapshotAt(snapshot, now)

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

	repo, err := t.syncRepo(ctx, owner, name, true)
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
		t.syncAllLogged(ctx, true)
	}()
	return nil
}

// SyncAll refreshes every tracked repository using a small worker pool.
func (t *Tracker) SyncAll(ctx context.Context) (int, error) {
	return t.syncAll(ctx, true)
}

// Background sweeps check visibility on every pass, while inactive repositories
// fetch their full collections less often. Manual sync always fetches everything.
func (t *Tracker) syncAll(ctx context.Context, force bool) (int, error) {
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
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return synced, errors.Join(append(errList, ctx.Err())...)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if _, err := t.syncRepo(ctx, repo.Owner.Login, repo.Name, force); err != nil {
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

	return synced, errors.Join(append(errList, ctx.Err())...)
}

type syncResult struct {
	repo         *model.Repository
	metadataOnly bool
}

// syncRepo refreshes a single repository. singleflight collapses concurrent
// refreshes of the same repository into one GitHub request set.
func (t *Tracker) syncRepo(ctx context.Context, owner, name string, force bool) (*model.Repository, error) {
	// Serialize metadata and full refreshes together so an older public result
	// cannot overwrite a concurrent decision to quarantine the repository.
	value, err, _ := t.group.Do("sync:"+repoKey(owner, name), func() (any, error) {
		existing, err := t.store.Get(ctx, owner, name)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotTracked
		}
		if err != nil {
			return nil, err
		}

		var snapshot *model.Repository
		var fetchErr error
		metadataOnly := false
		if !force && time.Now().Before(existing.NextSyncAt) {
			if client, ok := t.github.(interface {
				FetchRepository(context.Context, string, string) (*model.Repository, error)
			}); ok {
				snapshot, fetchErr = client.FetchRepository(ctx, owner, name)
				metadataOnly = true
			}
		}
		if !metadataOnly || (fetchErr == nil && isPublicRepository(*snapshot) &&
			(!isPublicRepository(*existing) || !snapshot.PushedAt.Equal(existing.PushedAt) ||
				renamed(owner, name, snapshot))) {
			snapshot, fetchErr = t.github.FetchSnapshot(ctx, owner, name)
			metadataOnly = false
		}
		if fetchErr == nil && !isPublicRepository(*snapshot) {
			// Hide the previous public snapshot using the newly observed visibility.
			existing.Visibility = snapshot.Visibility
			existing.Hidden = true
			fetchErr = ErrPrivateRepo
		}
		if fetchErr != nil {
			if errors.Is(fetchErr, github.ErrRepositoryUnavailable) {
				existing.Hidden = true
			}
			// Keep the last good snapshot, but surface the failure in the UI.
			existing.LastSyncAttempt = time.Now().UTC()
			existing.NextSyncAt = existing.LastSyncAttempt.Add(t.syncInterval)
			var limited *github.RateLimitError
			if errors.As(fetchErr, &limited) && limited.ResetAt.After(existing.NextSyncAt) {
				existing.NextSyncAt = limited.ResetAt
			}
			existing.SyncError = truncate(fetchErr.Error(), maxSyncErrorLen)
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if saveErr := t.store.ReplaceSnapshot(persistCtx, existing); saveErr != nil {
				t.log.Warn("could not persist sync error", "repo", existing.FullName, "error", saveErr)
				return nil, errors.Join(fetchErr, saveErr)
			}
			return nil, fetchErr
		}
		if metadataOnly {
			return syncResult{repo: existing, metadataOnly: true}, nil
		}

		now := time.Now().UTC()
		snapshot.TrackedAt = existing.TrackedAt
		snapshot.LastSyncAttempt = now
		snapshot.LastSyncedAt = now
		snapshot.SyncError = ""
		snapshot.Hidden = false
		snapshot.NextSyncAt = t.nextSnapshotAt(snapshot, now)
		if err := t.saveSnapshot(ctx, owner, name, snapshot); err != nil {
			return nil, err
		}
		return syncResult{repo: snapshot}, nil
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.(syncResult)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected sync result type %T", value)
	}
	if force && result.metadataOnly {
		// A manual request may have joined a cheap background check. Fetch the
		// full snapshot now that that check has completed.
		return t.syncRepo(ctx, owner, name, true)
	}
	return result.repo, nil
}

// renamed reports whether GitHub answered the repository tracked as owner/name
// under another name, which it does after a rename or a transfer.
func renamed(owner, name string, snapshot *model.Repository) bool {
	return strings.ToLower(strings.TrimSpace(snapshot.FullName)) != repoKey(owner, name)
}

// saveSnapshot stores a fresh snapshot over the record tracked as owner/name.
// A renamed repository takes its record along, tracking date included. When
// its new name is already tracked, the record under the old one is a duplicate.
func (t *Tracker) saveSnapshot(ctx context.Context, owner, name string, snapshot *model.Repository) error {
	if !renamed(owner, name, snapshot) {
		return t.store.ReplaceSnapshot(ctx, snapshot)
	}

	err := t.store.Rename(ctx, owner, name, snapshot)
	if errors.Is(err, store.ErrDuplicate) {
		if _, err := t.store.Delete(ctx, owner, name); err != nil {
			return err
		}
		t.log.Info("duplicate of a renamed repository removed",
			"from", owner+"/"+name, "to", snapshot.FullName)
		return nil
	}
	if err != nil {
		return err
	}
	t.log.Info("repository renamed", "from", owner+"/"+name, "to", snapshot.FullName)
	return nil
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
		return fmt.Errorf("%w: %w", ErrRepoNotFound, err)
	}
	return err
}

func repoKey(owner, name string) string {
	return strings.ToLower(strings.TrimSpace(owner) + "/" + strings.TrimSpace(name))
}

// isPublicRepository treats missing or unknown visibility as non-public. The
// dashboard exposes repository metadata to everyone, so only data GitHub
// explicitly marks as public may be stored or shown.
func isPublicRepository(repo model.Repository) bool {
	return !repo.Hidden && strings.EqualFold(strings.TrimSpace(repo.Visibility), "public")
}

func (t *Tracker) nextSnapshotAt(repo *model.Repository, now time.Time) time.Time {
	// Align deadlines to the base interval so time spent fetching a snapshot
	// does not make the next tick miss its deadline and double the cadence.
	return now.Truncate(t.syncInterval).Add(t.snapshotInterval(repo, now))
}

// Full snapshots back off by 2x, 4x and 8x after one, seven and thirty days
// without captured activity. Metadata checks keep the normal privacy cadence.
func (t *Tracker) snapshotInterval(repo *model.Repository, now time.Time) time.Duration {
	latest := repo.PushedAt
	observe := func(at time.Time) {
		if at.After(latest) {
			latest = at
		}
	}
	for _, push := range repo.Pushes {
		observe(push.Timestamp)
	}
	for _, pr := range repo.PullRequests {
		observe(pr.CreatedAt)
		observe(pr.UpdatedAt)
		if pr.MergedAt != nil {
			observe(*pr.MergedAt)
		}
		if pr.ClosedAt != nil {
			observe(*pr.ClosedAt)
		}
	}
	for _, issue := range repo.Issues {
		observe(issue.CreatedAt)
		observe(issue.UpdatedAt)
		if issue.ClosedAt != nil {
			observe(*issue.ClosedAt)
		}
	}
	age := now.Sub(latest)
	switch {
	case latest.IsZero() || age >= 30*24*time.Hour:
		return 8 * t.syncInterval
	case age >= 7*24*time.Hour:
		return 4 * t.syncInterval
	case age >= 24*time.Hour:
		return 2 * t.syncInterval
	default:
		return t.syncInterval
	}
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
	if repo.Pushes == nil {
		repo.Pushes = []model.Push{}
	}
	if repo.Issues == nil {
		repo.Issues = []model.Issue{}
	}
	for i := range repo.Issues {
		// Snapshots written before issue states were stored only contain open
		// issues, so an empty state means open.
		if repo.Issues[i].State == "" {
			repo.Issues[i].State = "open"
		}
	}
	if repo.PullRequests == nil {
		repo.PullRequests = []model.PullRequest{}
	}
	// Older snapshots only kept the latest pull request. Surface it on the
	// dedicated pull request page until the next sync fills the full list.
	if len(repo.PullRequests) == 0 && repo.LatestPR != nil {
		repo.PullRequests = []model.PullRequest{*repo.LatestPR}
	}
}
