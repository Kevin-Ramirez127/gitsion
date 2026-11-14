// Package github is a small REST client for the GitHub endpoints the
// dashboard needs. It is intentionally independent from the store and the API
// so it can be exercised against an httptest server.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"gitsion/backend/internal/model"
)

const (
	apiVersion  = "2022-11-28"
	userAgent   = "gitsion-dashboard/1.0"
	maxBodySize = 2 << 20
	maxTags     = 10
	// maxIssuesPerState and maxPullRequests cap the lists stored per snapshot.
	// GitHub returns up to 100 items per page, which is enough for the
	// "show more" sections on the dedicated issue and pull request pages.
	maxIssuesPerState = 100
	maxPullRequests   = 100
	// maxContributors caps the contributor list stored per snapshot. GitHub
	// returns them ordered by contribution count.
	maxContributors = 100
	// Keep recent push history bounded to the latest repository activity page.
	maxActivity = 100
)

var (
	// ErrNotFound means the repository does not exist or is not visible with
	// the configured credentials.
	ErrNotFound = errors.New("github: not found")
	// ErrUnauthorized means GitHub rejected the configured token.
	ErrUnauthorized = errors.New("github: unauthorized")
)

// RateLimitError is returned when GitHub's rate limit is exhausted.
type RateLimitError struct {
	ResetAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return "github: API rate limit exceeded"
	}
	return fmt.Sprintf("github: API rate limit exceeded until %s", e.ResetAt.Format(time.RFC3339))
}

// APIError is a non-2xx response that is not otherwise classified.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: API returned %d: %s", e.Status, e.Message)
}

// Client talks to the GitHub REST API.
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewClient creates a client. baseURL can point at a fake server in tests.
func NewClient(baseURL, token string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 25 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// FetchSnapshot retrieves repository metadata, pull requests, tags, issues and
// top contributors and attributed push activities in parallel.
func (c *Client) FetchSnapshot(ctx context.Context, owner, name string) (*model.Repository, error) {
	var (
		repo         *model.Repository
		prs          []model.PullRequest
		tags         []model.Tag
		issues       []model.Issue
		contributors []model.User
		activities   []ghActivity
	)

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (err error) {
		repo, err = c.fetchRepository(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		prs, err = c.fetchPullRequests(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		tags, err = c.fetchTags(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		issues, err = c.fetchIssues(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		contributors, err = c.fetchContributors(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		activities, err = c.fetchRepositoryActivity(ctx, owner, name)
		return err
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}

	attributeMerges(prs, activities)
	repo.PullRequests = prs
	if len(prs) > 0 {
		latest := prs[0]
		repo.LatestPR = &latest
	}
	repo.Tags = tags
	repo.Issues = issues
	repo.Contributors = contributors
	repo.Pushes = pushesFromActivities(activities)
	repo.SyncError = ""
	return repo, nil
}

func (c *Client) fetchRepository(ctx context.Context, owner, name string) (*model.Repository, error) {
	var raw ghRepository
	if err := c.get(ctx, repoPath(owner, name), &raw); err != nil {
		return nil, err
	}
	if raw.FullName == "" {
		return nil, fmt.Errorf("github: repository response is missing full_name")
	}
	return &model.Repository{
		FullName:      raw.FullName,
		FullNameKey:   strings.ToLower(raw.FullName),
		Name:          raw.Name,
		Owner:         raw.Owner.toOwner(),
		Description:   stringValue(raw.Description),
		HTMLURL:       raw.HTMLURL,
		Language:      stringValue(raw.Language),
		DefaultBranch: raw.DefaultBranch,
		Visibility:    raw.Visibility,
		Topics:        nonNilStrings(raw.Topics),
		Stars:         raw.Stargazers,
		Forks:         raw.Forks,
		OpenItems:     raw.OpenIssues,
		PushedAt:      raw.PushedAt,
		Tags:          []model.Tag{},
		Issues:        []model.Issue{},
		Contributors:  []model.User{},
	}, nil
}

func (c *Client) fetchPullRequests(ctx context.Context, owner, name string) ([]model.PullRequest, error) {
	var raws []ghPullRequest
	path := fmt.Sprintf(
		"%s/pulls?state=all&sort=updated&direction=desc&per_page=%d",
		repoPath(owner, name),
		maxPullRequests,
	)
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	prs := make([]model.PullRequest, 0, len(raws))
	for _, raw := range raws {
		state := raw.State
		if raw.MergedAt != nil {
			state = "merged"
		}
		prs = append(prs, model.PullRequest{
			Number:         raw.Number,
			Title:          raw.Title,
			State:          state,
			Draft:          raw.Draft,
			Author:         raw.User.toModel(),
			HTMLURL:        raw.HTMLURL,
			HeadRef:        raw.Head.Ref,
			BaseRef:        raw.Base.Ref,
			CreatedAt:      raw.CreatedAt,
			UpdatedAt:      raw.UpdatedAt,
			MergedAt:       raw.MergedAt,
			ClosedAt:       raw.ClosedAt,
			MergeCommitSHA: raw.MergeCommitSHA,
		})
	}
	return prs, nil
}

func (c *Client) fetchTags(ctx context.Context, owner, name string) ([]model.Tag, error) {
	var raws []ghTag
	path := fmt.Sprintf("%s/tags?per_page=%d", repoPath(owner, name), maxTags)
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	tags := make([]model.Tag, 0, len(raws))
	for _, raw := range raws {
		tags = append(tags, model.Tag{Name: raw.Name, CommitSHA: raw.Commit.SHA})
	}
	return tags, nil
}

func (c *Client) fetchIssues(ctx context.Context, owner, name string) ([]model.Issue, error) {
	states := []string{"open", "closed"}
	results := make([][]model.Issue, len(states))

	group, ctx := errgroup.WithContext(ctx)
	for i, state := range states {
		group.Go(func() error {
			issues, err := c.fetchIssueState(ctx, owner, name, state)
			if err != nil {
				return err
			}
			results[i] = issues
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	issues := make([]model.Issue, 0, maxIssuesPerState*len(states))
	for _, stateIssues := range results {
		issues = append(issues, stateIssues...)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})
	return issues, nil
}

func (c *Client) fetchIssueState(ctx context.Context, owner, name, state string) ([]model.Issue, error) {
	issues := make([]model.Issue, 0, maxIssuesPerState)
	seen := make(map[int]bool)
	for page := 1; len(issues) < maxIssuesPerState; page++ {
		var raws []ghIssue
		path := fmt.Sprintf(
			"%s/issues?state=%s&sort=updated&direction=desc&per_page=%d&page=%d",
			repoPath(owner, name), url.QueryEscape(state), maxIssuesPerState, page,
		)
		if err := c.get(ctx, path, &raws); err != nil {
			return nil, err
		}
		for _, raw := range raws {
			// Pull requests do not consume the issue budget. Deduplicate issues
			// that move between pages while GitHub's updated ordering changes.
			if raw.PullRequest != nil || seen[raw.Number] {
				continue
			}
			seen[raw.Number] = true
			labels := make([]string, 0, len(raw.Labels))
			for _, label := range raw.Labels {
				labels = append(labels, label.Name)
			}
			issueState := raw.State
			if issueState == "" {
				issueState = state
			}
			issues = append(issues, model.Issue{
				Number:    raw.Number,
				Title:     raw.Title,
				State:     issueState,
				Author:    raw.User.toModel(),
				Labels:    labels,
				Comments:  raw.Comments,
				HTMLURL:   raw.HTMLURL,
				CreatedAt: raw.CreatedAt,
				UpdatedAt: raw.UpdatedAt,
				ClosedAt:  raw.ClosedAt,
			})
			if len(issues) == maxIssuesPerState {
				break
			}
		}
		if len(raws) < maxIssuesPerState {
			break
		}
	}
	return issues, nil
}

func (c *Client) fetchContributors(ctx context.Context, owner, name string) ([]model.User, error) {
	var raws []ghUser
	path := fmt.Sprintf("%s/contributors?per_page=%d", repoPath(owner, name), maxContributors)
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	contributors := make([]model.User, 0, len(raws))
	for _, raw := range raws {
		if raw.Login == "" {
			continue // anonymized contributors cannot be searched by login
		}
		contributors = append(contributors, raw.toModel())
	}
	return contributors, nil
}

func (c *Client) fetchRepositoryActivity(ctx context.Context, owner, name string) ([]ghActivity, error) {
	var raws []ghActivity
	path := fmt.Sprintf("%s/activity?per_page=%d&direction=desc", repoPath(owner, name), maxActivity)
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	return raws, nil
}

func pushesFromActivities(raws []ghActivity) []model.Push {
	pushes := make([]model.Push, 0, len(raws))
	for _, raw := range raws {
		if raw.ActivityType != "push" && raw.ActivityType != "force_push" {
			continue
		}
		// GitHub can return a null actor. Never substitute the repository owner.
		if raw.Actor == nil || raw.Actor.Login == "" || raw.ID <= 0 ||
			raw.Ref == "" || raw.Timestamp.IsZero() || raw.After == "" ||
			strings.Trim(raw.After, "0") == "" {
			continue
		}
		pushes = append(pushes, model.Push{
			ID:        raw.ID,
			Actor:     raw.Actor.toModel(),
			Ref:       raw.Ref,
			Before:    raw.Before,
			After:     raw.After,
			Timestamp: raw.Timestamp,
			Forced:    raw.ActivityType == "force_push",
		})
	}
	return pushes
}

// Reuse repository activity to identify merge actors without a request per PR.
// If the merge is outside the activity window, leave the actor unknown.
func attributeMerges(prs []model.PullRequest, activities []ghActivity) {
	for i := range prs {
		pr := &prs[i]
		if pr.MergedAt == nil || pr.MergeCommitSHA == "" || pr.BaseRef == "" {
			continue
		}
		for _, activity := range activities {
			if activity.ActivityType == "pr_merge" && activity.After == pr.MergeCommitSHA &&
				activity.Ref == "refs/heads/"+pr.BaseRef && activity.Timestamp.Equal(*pr.MergedAt) &&
				activity.Actor != nil && activity.Actor.Login != "" {
				actor := activity.Actor.toModel()
				pr.MergedBy = &actor
				break
			}
		}
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if resp.StatusCode == http.StatusNoContent {
			return nil
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(out); err != nil {
			return fmt.Errorf("github: decode response: %w", err)
		}
		return nil
	}

	message := strings.TrimSpace(readErrorBody(resp.Body))
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
			return &RateLimitError{ResetAt: rateLimitReset(resp.Header)}
		}
	}
	return &APIError{Status: resp.StatusCode, Message: message}
}

func readErrorBody(r io.Reader) string {
	body, _ := io.ReadAll(io.LimitReader(r, 64<<10))
	var parsed struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Message != "" {
		return parsed.Message
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300]
	}
	return text
}

func rateLimitReset(header http.Header) time.Time {
	var unix int64
	if _, err := fmt.Sscanf(header.Get("X-RateLimit-Reset"), "%d", &unix); err != nil || unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func stringValue(in *string) string {
	if in == nil {
		return ""
	}
	return *in
}

// --- GitHub wire types -------------------------------------------------------

type ghUser struct {
	Login     string `json:"login"`
	Type      string `json:"type"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
}

func (u ghUser) toModel() model.User {
	return model.User{Login: u.Login, AvatarURL: u.AvatarURL, HTMLURL: u.HTMLURL}
}

func (u ghUser) toOwner() model.Owner {
	return model.Owner{
		Login:     u.Login,
		LoginKey:  strings.ToLower(u.Login),
		Type:      u.Type,
		AvatarURL: u.AvatarURL,
		HTMLURL:   u.HTMLURL,
	}
}

type ghRepository struct {
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Owner         ghUser    `json:"owner"`
	Description   *string   `json:"description"`
	HTMLURL       string    `json:"html_url"`
	Language      *string   `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	Visibility    string    `json:"visibility"`
	Topics        []string  `json:"topics"`
	Stargazers    int       `json:"stargazers_count"`
	Forks         int       `json:"forks_count"`
	OpenIssues    int       `json:"open_issues_count"`
	PushedAt      time.Time `json:"pushed_at"`
}

type ghPullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	User    ghUser `json:"user"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	MergedAt       *time.Time `json:"merged_at"`
	ClosedAt       *time.Time `json:"closed_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
}

type ghTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type ghActivity struct {
	ID           int64     `json:"id"`
	Actor        *ghUser   `json:"actor"`
	Ref          string    `json:"ref"`
	Before       string    `json:"before"`
	After        string    `json:"after"`
	Timestamp    time.Time `json:"timestamp"`
	ActivityType string    `json:"activity_type"`
}

type ghIssue struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	User        ghUser     `json:"user"`
	HTMLURL     string     `json:"html_url"`
	Comments    int        `json:"comments"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}
