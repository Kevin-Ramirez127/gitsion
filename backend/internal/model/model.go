// Package model contains the persisted representation of a tracked repository.
package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// User is the subset of a GitHub account we display.
type User struct {
	Login     string `bson:"login" json:"login"`
	AvatarURL string `bson:"avatar_url" json:"avatarUrl"`
	HTMLURL   string `bson:"html_url" json:"htmlUrl"`
}

// Owner describes the account or organisation that owns a repository.
type Owner struct {
	Login     string `bson:"login" json:"login"`
	LoginKey  string `bson:"login_key" json:"-"`
	Type      string `bson:"type" json:"type"`
	AvatarURL string `bson:"avatar_url" json:"avatarUrl"`
	HTMLURL   string `bson:"html_url" json:"htmlUrl"`
}

// PullRequest is the most recently updated pull request of a repository.
type PullRequest struct {
	Number    int        `bson:"number" json:"number"`
	Title     string     `bson:"title" json:"title"`
	State     string     `bson:"state" json:"state"` // open, merged or closed
	Draft     bool       `bson:"draft" json:"draft"`
	Author    User       `bson:"author" json:"author"`
	HTMLURL   string     `bson:"html_url" json:"htmlUrl"`
	HeadRef   string     `bson:"head_ref,omitempty" json:"headRef,omitempty"`
	BaseRef   string     `bson:"base_ref,omitempty" json:"baseRef,omitempty"`
	CreatedAt time.Time  `bson:"created_at" json:"createdAt"`
	UpdatedAt time.Time  `bson:"updated_at" json:"updatedAt"`
	MergedAt  *time.Time `bson:"merged_at,omitempty" json:"mergedAt,omitempty"`
}

// Tag is a git tag. GitHub's tags endpoint does not expose tag dates, so we
// store the name and the commit it points at.
type Tag struct {
	Name      string `bson:"name" json:"name"`
	CommitSHA string `bson:"commit_sha" json:"commitSha"`
}

// Issue is an open GitHub issue (pull requests are filtered out).
type Issue struct {
	Number    int       `bson:"number" json:"number"`
	Title     string    `bson:"title" json:"title"`
	Author    User      `bson:"author" json:"author"`
	Labels    []string  `bson:"labels" json:"labels"`
	Comments  int       `bson:"comments" json:"comments"`
	HTMLURL   string    `bson:"html_url" json:"htmlUrl"`
	CreatedAt time.Time `bson:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bson:"updated_at" json:"updatedAt"`
}

// Repository is both the tracked-repository record and the cached GitHub
// snapshot. Snapshots are embedded and capped so a dashboard read is a single
// Mongo lookup.
type Repository struct {
	ID            bson.ObjectID `bson:"_id,omitempty" json:"-"`
	FullName      string        `bson:"full_name" json:"fullName"`
	FullNameKey   string        `bson:"full_name_key" json:"-"`
	Name          string        `bson:"name" json:"name"`
	Owner         Owner         `bson:"owner" json:"owner"`
	Description   string        `bson:"description" json:"description"`
	HTMLURL       string        `bson:"html_url" json:"htmlUrl"`
	Language      string        `bson:"language" json:"language"`
	DefaultBranch string        `bson:"default_branch" json:"defaultBranch"`
	Visibility    string        `bson:"visibility" json:"visibility"`
	Topics        []string      `bson:"topics" json:"topics"`
	Stars         int           `bson:"stars" json:"stars"`
	Forks         int           `bson:"forks" json:"forks"`
	OpenItems     int           `bson:"open_items" json:"openItems"` // GitHub open issues + PRs
	PushedAt      time.Time     `bson:"pushed_at" json:"pushedAt"`

	LatestPR *PullRequest `bson:"latest_pr,omitempty" json:"latestPr"`
	Tags     []Tag        `bson:"tags" json:"tags"`
	Issues   []Issue      `bson:"issues" json:"issues"`

	TrackedAt       time.Time `bson:"tracked_at" json:"trackedAt"`
	LastSyncedAt    time.Time `bson:"last_synced_at" json:"lastSyncedAt"`
	LastSyncAttempt time.Time `bson:"last_sync_attempt" json:"lastSyncAttempt"`
	SyncError       string    `bson:"sync_error,omitempty" json:"syncError,omitempty"`
}

// OwnerSummary aggregates tracked repositories per owner for the dashboard.
type OwnerSummary struct {
	Login     string `json:"login"`
	Type      string `json:"type"`
	AvatarURL string `json:"avatarUrl"`
	HTMLURL   string `json:"htmlUrl"`
	RepoCount int    `json:"repoCount"`
}
