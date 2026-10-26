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
	maxIssues   = 25
	// maxContributors caps the contributor list stored per snapshot. GitHub
	// returns them ordered by contribution count.
	maxContributors = 100
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

// FetchSnapshot retrieves repository metadata, the latest updated pull
// request, tags, open issues and top contributors in parallel.
func (c *Client) FetchSnapshot(ctx context.Context, owner, name string) (*model.Repository, error) {
	var (
		repo         *model.Repository
		pr           *model.PullRequest
		tags         []model.Tag
		issues       []model.Issue
		contributors []model.User
	)

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (err error) {
		repo, err = c.fetchRepository(ctx, owner, name)
		return err
	})
	group.Go(func() (err error) {
		pr, err = c.fetchLatestPullRequest(ctx, owner, name)
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
	if err := group.Wait(); err != nil {
		return nil, err
	}

	repo.LatestPR = pr
	repo.Tags = tags
	repo.Issues = issues
	repo.Contributors = contributors
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

func (c *Client) fetchLatestPullRequest(ctx context.Context, owner, name string) (*model.PullRequest, error) {
	var raws []ghPullRequest
	path := repoPath(owner, name) + "/pulls?state=all&sort=updated&direction=desc&per_page=1"
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, nil
	}
	raw := raws[0]
	state := raw.State
	if raw.MergedAt != nil {
		state = "merged"
	}
	return &model.PullRequest{
		Number:    raw.Number,
		Title:     raw.Title,
		State:     state,
		Draft:     raw.Draft,
		Author:    raw.User.toModel(),
		HTMLURL:   raw.HTMLURL,
		HeadRef:   raw.Head.Ref,
		BaseRef:   raw.Base.Ref,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
		MergedAt:  raw.MergedAt,
	}, nil
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
	var raws []ghIssue
	path := fmt.Sprintf("%s/issues?state=open&per_page=100", repoPath(owner, name))
	if err := c.get(ctx, path, &raws); err != nil {
		return nil, err
	}
	issues := make([]model.Issue, 0, maxIssues)
	for _, raw := range raws {
		if raw.PullRequest != nil {
			continue // the issues endpoint also returns pull requests
		}
		if len(issues) == maxIssues {
			break
		}
		labels := make([]string, 0, len(raw.Labels))
		for _, label := range raw.Labels {
			labels = append(labels, label.Name)
		}
		issues = append(issues, model.Issue{
			Number:    raw.Number,
			Title:     raw.Title,
			Author:    raw.User.toModel(),
			Labels:    labels,
			Comments:  raw.Comments,
			HTMLURL:   raw.HTMLURL,
			CreatedAt: raw.CreatedAt,
			UpdatedAt: raw.UpdatedAt,
		})
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
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
}

type ghTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type ghIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	User        ghUser    `json:"user"`
	HTMLURL     string    `json:"html_url"`
	Comments    int       `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}
