package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitsion/backend/internal/model"
)

func TestAttributeMergesRequiresMatchingMergeActivity(t *testing.T) {
	merged := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	activity := ghActivity{
		ActivityType: "pr_merge", After: "merge-sha", Ref: "refs/heads/main",
		Timestamp: merged, Actor: &ghUser{Login: "maintainer"},
	}
	tests := []struct {
		name      string
		change    func(*ghActivity)
		wantActor bool
	}{
		{"matching merge", func(*ghActivity) {}, true},
		{"different commit", func(a *ghActivity) { a.After = "other" }, false},
		{"different branch", func(a *ghActivity) { a.Ref = "refs/heads/release" }, false},
		{"different time", func(a *ghActivity) { a.Timestamp = merged.Add(time.Second) }, false},
		{"ordinary push", func(a *ghActivity) { a.ActivityType = "push" }, false},
		{"missing actor", func(a *ghActivity) { a.Actor = nil }, false},
		{"anonymous actor", func(a *ghActivity) { a.Actor = &ghUser{} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := activity
			tt.change(&candidate)
			prs := []model.PullRequest{{
				Author: model.User{Login: "author"}, MergedAt: &merged,
				MergeCommitSHA: "merge-sha", BaseRef: "main",
			}}
			attributeMerges(prs, []ghActivity{candidate})
			if tt.wantActor {
				if prs[0].MergedBy == nil || prs[0].MergedBy.Login != "maintainer" {
					t.Fatalf("merge actor = %+v", prs[0].MergedBy)
				}
			} else if prs[0].MergedBy != nil {
				t.Fatalf("must leave unmatched actor unknown: %+v", prs[0].MergedBy)
			}
		})
	}
}

func TestFetchIssueStateContinuesPastPullRequestPages(t *testing.T) {
	for _, state := range []string{"open", "closed"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/repos/acme/widget/issues" || r.URL.Query().Get("state") != state {
					t.Errorf("unexpected request: %s", r.URL)
				}
				var entries []map[string]any
				switch r.URL.Query().Get("page") {
				case "1":
					for n := 1; n <= 100; n++ {
						entries = append(entries, map[string]any{"number": n, "pull_request": map[string]any{}})
					}
				case "2":
					entries = []map[string]any{{"number": 101, "title": "Real issue", "labels": []any{}}}
				default:
					t.Errorf("unexpected extra page: %s", r.URL)
				}
				_ = json.NewEncoder(w).Encode(entries)
			}))
			defer server.Close()
			issues, err := NewClient(server.URL, "").fetchIssueState(context.Background(), "acme", "widget", state)
			if err != nil || len(issues) != 1 || issues[0].Number != 101 || issues[0].State != state || calls != 2 {
				t.Fatalf("issues = %+v, calls = %d, err = %v", issues, calls, err)
			}
		})
	}
}

func TestFetchIssueStateCapsActualIssuesAndDeduplicates(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var entries []map[string]any
		if r.URL.Query().Get("page") == "1" {
			for n := 1; n <= 100; n++ {
				entry := map[string]any{"number": n}
				if n > 50 {
					entry["pull_request"] = map[string]any{}
				}
				entries = append(entries, entry)
			}
		} else {
			// Issue 50 moved across the page boundary during pagination.
			entries = append(entries, map[string]any{"number": 50})
			for n := 101; n < 200; n++ {
				entries = append(entries, map[string]any{"number": n})
			}
		}
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()
	issues, err := NewClient(server.URL, "").fetchIssueState(context.Background(), "acme", "widget", "open")
	if err != nil || len(issues) != 100 || issues[99].Number != 150 || calls != 2 {
		t.Fatalf("len = %d, calls = %d, err = %v", len(issues), calls, err)
	}
}

func TestFetchIssueStateDoesNotPublishPartialResultsOnPageFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		entries := make([]map[string]any, 100)
		for i := range entries {
			entries[i] = map[string]any{"number": i + 1, "pull_request": map[string]any{}}
		}
		entries[0] = map[string]any{"number": 1}
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()
	issues, err := NewClient(server.URL, "").fetchIssueState(context.Background(), "acme", "widget", "open")
	var apiErr *APIError
	if issues != nil || !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("issues = %+v, err = %v", issues, err)
	}
}
