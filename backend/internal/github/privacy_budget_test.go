package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotChecksVisibilityBeforeFetchingCollections(t *testing.T) {
	for _, visibility := range []string{"private", "internal", "", "unknown"} {
		t.Run(visibility, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/repos/acme/widget" {
					t.Errorf("fetched non-public collection: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name": "widget", "full_name": "acme/widget", "visibility": visibility,
				})
			}))
			defer server.Close()
			repo, err := NewClient(server.URL, "").FetchSnapshot(context.Background(), "acme", "widget")
			if err != nil || repo.Visibility != visibility || calls.Load() != 1 {
				t.Fatalf("visibility = %+v, calls = %d, err = %v", repo, calls.Load(), err)
			}
		})
	}
}

func TestOnlyMetadata404MeansRepositoryUnavailable(t *testing.T) {
	for _, unavailablePath := range []string{"/repos/acme/widget", "/repos/acme/widget/activity"} {
		t.Run(unavailablePath, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == unavailablePath {
					http.NotFound(w, r)
				} else if r.URL.Path == "/repos/acme/widget" {
					_, _ = w.Write([]byte(`{"name":"widget","full_name":"acme/widget","visibility":"public"}`))
				} else {
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "").FetchSnapshot(context.Background(), "acme", "widget")
			wantUnavailable := unavailablePath == "/repos/acme/widget"
			if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrRepositoryUnavailable) != wantUnavailable {
				t.Fatalf("error = %v, want unavailable = %t", err, wantUnavailable)
			}
		})
	}
}

func TestRequestUsageAndQuotaReset(t *testing.T) {
	var calls atomic.Int64
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	var out map[string]any
	if err := client.get(context.Background(), "/repos/a/b", &out); err != nil {
		t.Fatal(err)
	}
	usage := client.Usage()
	if usage.Requests != 1 || !usage.RateLimitKnown || usage.Limit != 5000 ||
		usage.Remaining != 0 || !usage.ResetAt.Equal(reset) {
		t.Fatalf("usage = %+v", usage)
	}
	var limited *RateLimitError
	if err := client.get(context.Background(), "/repos/a/b", &out); !errors.As(err, &limited) {
		t.Fatalf("exhausted quota = %v", err)
	}
	if calls.Load() != 1 || client.Usage().Requests != 1 {
		t.Fatal("quota exhaustion should avoid further HTTP requests")
	}
	client.quota.mu.Lock()
	client.quota.usage.ResetAt = time.Now().Add(-time.Second)
	client.quota.mu.Unlock()
	if err := client.get(context.Background(), "/repos/a/b", &out); err != nil || calls.Load() != 2 {
		t.Fatalf("did not resume after reset: %v, calls = %d", err, calls.Load())
	}
}

func TestSecondaryRateLimitPausesRequests(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"secondary rate limit"}`))
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			for i := 0; i < 2; i++ {
				var out map[string]any
				var limited *RateLimitError
				if err := client.get(context.Background(), "/repos/a/b", &out); !errors.As(err, &limited) ||
					time.Until(limited.ResetAt) < 119*time.Second {
					t.Fatalf("secondary rate limit = %v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("backoff made %d requests", calls.Load())
			}
		})
	}
}

func TestQuotaReservationsAreConcurrentAndResponsesCannotRaiseBudget(t *testing.T) {
	var budget requestBudget
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "5000")
	header.Set("X-RateLimit-Remaining", "25")
	header.Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
	budget.observe(header)
	var started atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 100; i++ {
		workers.Go(func() {
			if budget.begin() == nil {
				started.Add(1)
			}
		})
	}
	workers.Wait()
	// A delayed response from an earlier request must not reopen exhausted quota.
	budget.observe(header)
	if started.Load() != 25 || budget.usage.Remaining != 0 || budget.usage.Requests != 25 {
		t.Fatalf("started = %d, usage = %+v", started.Load(), budget.usage)
	}
	if err := budget.begin(); err == nil {
		t.Fatal("out-of-order response incorrectly restored quota")
	}
}
