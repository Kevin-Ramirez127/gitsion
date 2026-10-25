package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestFetchSnapshot(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widget", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "widget",
			"full_name": "acme/widget",
			"owner": {"login": "acme", "type": "Organization", "avatar_url": "https://avatars.example/acme", "html_url": "https://github.com/acme"},
			"description": "A widget",
			"html_url": "https://github.com/acme/widget",
			"language": "Go",
			"default_branch": "main",
			"visibility": "public",
			"topics": ["tools"],
			"stargazers_count": 42,
			"forks_count": 7,
			"open_issues_count": 9,
			"pushed_at": "2026-02-01T10:00:00Z"
		}`))
	})
	mux.HandleFunc("/repos/acme/widget/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "all" {
			t.Errorf("pulls state query = %q", r.URL.Query().Get("state"))
		}
		_, _ = w.Write([]byte(`[{
			"number": 12,
			"title": "Add the thing",
			"state": "closed",
			"draft": false,
			"user": {"login": "octocat", "avatar_url": "https://avatars.example/octocat", "html_url": "https://github.com/octocat"},
			"html_url": "https://github.com/acme/widget/pull/12",
			"head": {"ref": "feature"},
			"base": {"ref": "main"},
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-03T00:00:00Z",
			"merged_at": "2026-01-03T00:00:00Z"
		}]`))
	})
	mux.HandleFunc("/repos/acme/widget/tags", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"name": "v1.2.0", "commit": {"sha": "abcdef1234567890"}},
			{"name": "v1.1.0", "commit": {"sha": "0123456789abcdef"}}
		]`))
	})
	mux.HandleFunc("/repos/acme/widget/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("issues per_page = %q, want 100", got)
		}
		_, _ = w.Write([]byte(`[
			{
				"number": 3,
				"title": "Something broke",
				"user": {"login": "bob", "avatar_url": "https://avatars.example/bob", "html_url": "https://github.com/bob"},
				"html_url": "https://github.com/acme/widget/issues/3",
				"comments": 4,
				"created_at": "2026-01-05T00:00:00Z",
				"updated_at": "2026-01-06T00:00:00Z",
				"labels": [{"name": "bug"}, {"name": "priority"}]
			},
			{
				"number": 4,
				"title": "I am a pull request",
				"pull_request": {"url": "https://api.github.com/repos/acme/widget/pulls/4"},
				"user": {"login": "bob"},
				"labels": []
			}
		]`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	repo, err := client.FetchSnapshot(context.Background(), "acme", "widget")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	if repo.FullName != "acme/widget" || repo.FullNameKey != "acme/widget" {
		t.Errorf("full name = %q / %q", repo.FullName, repo.FullNameKey)
	}
	if repo.Owner.LoginKey != "acme" || repo.Owner.Type != "Organization" {
		t.Errorf("owner = %+v", repo.Owner)
	}
	if repo.Description != "A widget" || repo.Language != "Go" {
		t.Errorf("description/language = %q/%q", repo.Description, repo.Language)
	}
	if repo.Stars != 42 || repo.Forks != 7 || repo.OpenItems != 9 {
		t.Errorf("counts = %d/%d/%d", repo.Stars, repo.Forks, repo.OpenItems)
	}
	if repo.LatestPR == nil {
		t.Fatal("LatestPR is nil")
	}
	if repo.LatestPR.State != "merged" || repo.LatestPR.Number != 12 {
		t.Errorf("latest PR = %+v", repo.LatestPR)
	}
	if repo.LatestPR.MergedAt == nil {
		t.Error("MergedAt is nil")
	}
	if len(repo.Tags) != 2 || repo.Tags[0].Name != "v1.2.0" || repo.Tags[0].CommitSHA == "" {
		t.Errorf("tags = %+v", repo.Tags)
	}
	if len(repo.Issues) != 1 {
		t.Fatalf("issues = %+v, want PRs filtered out", repo.Issues)
	}
	if repo.Issues[0].Number != 3 || len(repo.Issues[0].Labels) != 2 {
		t.Errorf("issue = %+v", repo.Issues[0])
	}
}

func TestFetchSnapshotEmptyCollections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/empty", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"empty","full_name":"acme/empty","owner":{"login":"acme"},"default_branch":"main"}`))
	})
	mux.HandleFunc("/repos/acme/empty/pulls", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/repos/acme/empty/tags", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/repos/acme/empty/issues", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })

	server := httptest.NewServer(mux)
	defer server.Close()

	repo, err := NewClient(server.URL, "").FetchSnapshot(context.Background(), "acme", "empty")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if repo.LatestPR != nil {
		t.Error("LatestPR should be nil")
	}
	if repo.Tags == nil || repo.Issues == nil || repo.Topics == nil {
		t.Error("collections should be empty slices, not nil")
	}
}

func TestGetErrors(t *testing.T) {
	reset := time.Now().Add(10 * time.Minute).Unix()

	tests := []struct {
		name      string
		status    int
		wantErr   error
		rateLimit bool
		apiStatus int
	}{
		{name: "not found", status: http.StatusNotFound, wantErr: ErrNotFound},
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: ErrUnauthorized},
		{name: "rate limited", status: http.StatusForbidden, rateLimit: true},
		{name: "server error", status: http.StatusBadGateway, apiStatus: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.rateLimit {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			}))
			defer server.Close()

			var out map[string]any
			err := NewClient(server.URL, "").get(context.Background(), "/repos/a/b", &out)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.rateLimit {
				var rle *RateLimitError
				if !errors.As(err, &rle) {
					t.Fatalf("error = %T, want *RateLimitError", err)
				}
				if rle.ResetAt.Unix() != reset {
					t.Errorf("reset = %v, want %v", rle.ResetAt.Unix(), reset)
				}
			}
			if tt.apiStatus != 0 {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != tt.apiStatus {
					t.Fatalf("error = %v, want APIError %d", err, tt.apiStatus)
				}
			}
		})
	}
}
