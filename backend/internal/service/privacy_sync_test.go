package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gitsion/backend/internal/github"
	"gitsion/backend/internal/model"
)

func TestRepository404QuarantinesAndRecoversAcrossTrackerRestart(t *testing.T) {
	for _, method := range []string{"refresh", "sync_all", "background"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			st, gh := newFakeStore(), newFakeGitHub()
			tr := newTestTracker(st, gh)
			tr.manualCooldown = 0
			before, err := tr.Track(ctx, "acme/widget")
			if err != nil {
				t.Fatal(err)
			}
			gh.setError(fmt.Errorf("%w: %w", github.ErrRepositoryUnavailable, github.ErrNotFound))
			switch method {
			case "refresh":
				_, err = tr.Refresh(ctx, "acme", "widget")
			case "sync_all":
				_, err = tr.SyncAll(ctx)
			case "background":
				_, err = tr.syncAll(ctx, false)
			}
			if !errors.Is(err, github.ErrRepositoryUnavailable) {
				t.Fatalf("sync error = %v", err)
			}
			cached, _ := st.Get(ctx, "acme", "widget")
			if !cached.Hidden || cached.Visibility != "public" || cached.Description != before.Description ||
				!cached.LastSyncedAt.Equal(before.LastSyncedAt) || cached.SyncError == "" {
				t.Fatalf("quarantine must preserve the last good data without claiming private visibility: %+v", cached)
			}

			// The quarantine lives in storage, not just in the tracker process.
			tr = newTestTracker(st, gh)
			assertHidden := func() {
				t.Helper()
				for _, owner := range []string{"", "acme"} {
					repos, err := tr.List(ctx, owner)
					if err != nil || len(repos) != 0 {
						t.Fatalf("List(%q) exposed quarantined data: %+v, %v", owner, repos, err)
					}
				}
				if _, err := tr.Get(ctx, "acme", "widget"); !errors.Is(err, ErrNotTracked) {
					t.Fatalf("Get = %v, want ErrNotTracked", err)
				}
				owners, err := tr.Owners(ctx)
				if err != nil || len(owners) != 0 {
					t.Fatalf("Owners exposed quarantined data: %+v, %v", owners, err)
				}
			}
			assertHidden()
			gh.setError(errors.New("temporary network failure"))
			_, _ = tr.syncAll(ctx, false)
			assertHidden()

			gh.setError(nil)
			gh.snapshot.Description = "public again"
			if _, err := tr.syncAll(ctx, false); err != nil {
				t.Fatalf("automatic recovery: %v", err)
			}
			recovered, err := tr.Get(ctx, "acme", "widget")
			if err != nil || recovered.Hidden || recovered.SyncError != "" ||
				recovered.Description != "public again" || !recovered.TrackedAt.Equal(before.TrackedAt) {
				t.Fatalf("recovered = %+v, err = %v", recovered, err)
			}
		})
	}
}

func TestExplicitNonPublicVisibilityCanRecoverAutomatically(t *testing.T) {
	for _, visibility := range []string{"private", "internal", "", "unknown"} {
		t.Run(visibility, func(t *testing.T) {
			ctx := context.Background()
			st, gh := newFakeStore(), newFakeGitHub()
			tr := newTestTracker(st, gh)
			if _, err := tr.Track(ctx, "acme/widget"); err != nil {
				t.Fatal(err)
			}
			gh.snapshot.Visibility = visibility
			if _, err := tr.syncAll(ctx, false); !errors.Is(err, ErrPrivateRepo) {
				t.Fatalf("non-public sync = %v", err)
			}
			gh.snapshot.Visibility = "public"
			if _, err := newTestTracker(st, gh).syncAll(ctx, false); err != nil {
				t.Fatalf("recovery = %v", err)
			}
			if _, err := tr.Get(ctx, "acme", "widget"); err != nil {
				t.Fatalf("public again = %v", err)
			}
		})
	}
}

func TestCollection404DoesNotQuarantinePublicRepository(t *testing.T) {
	ctx := context.Background()
	st, gh := newFakeStore(), newFakeGitHub()
	tr := newTestTracker(st, gh)
	if _, err := tr.Track(ctx, "acme/widget"); err != nil {
		t.Fatal(err)
	}
	gh.setError(fmt.Errorf("activity endpoint: %w", github.ErrNotFound))
	if _, err := tr.Refresh(ctx, "acme", "widget"); err == nil {
		t.Fatal("expected collection error")
	}
	if repo, err := tr.Get(ctx, "acme", "widget"); err != nil || repo.Hidden {
		t.Fatalf("a collection failure should preserve public data: %+v, %v", repo, err)
	}
}

func TestInactiveRepositoryUsesMetadataChecksAndManualSyncBypassesSchedule(t *testing.T) {
	ctx := context.Background()
	st, gh := newFakeStore(), newFakeGitHub()
	gh.snapshot.PushedAt = time.Now().Add(-40 * 24 * time.Hour)
	tr := newTestTracker(st, gh)
	before, err := tr.Track(ctx, "acme/widget")
	if err != nil {
		t.Fatal(err)
	}
	if before.NextSyncAt.Sub(before.LastSyncedAt.Truncate(tr.syncInterval)) != 8*tr.syncInterval {
		t.Fatalf("inactive schedule = %s", before.NextSyncAt.Sub(before.LastSyncedAt))
	}
	// A restarted process must honor the persisted full-snapshot schedule.
	tr = newTestTracker(st, gh)
	if _, err := tr.syncAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if gh.callCount() != 1 || gh.metadataCalls != 1 {
		t.Fatalf("idle sweep: full = %d, metadata = %d", gh.callCount(), gh.metadataCalls)
	}
	if _, err := tr.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if gh.callCount() != 2 {
		t.Fatalf("manual sync did not bypass the schedule: %d", gh.callCount())
	}
	gh.snapshot.PushedAt = time.Now()
	if _, err := tr.syncAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Get(ctx, "acme", "widget")
	if gh.callCount() != 3 || after.NextSyncAt.Sub(after.LastSyncedAt.Truncate(tr.syncInterval)) != tr.syncInterval {
		t.Fatalf("a new push should trigger a full refresh immediately: %+v, calls = %d", after, gh.callCount())
	}
}

func TestSnapshotIntervalUsesAllCapturedActivity(t *testing.T) {
	now := time.Now().UTC()
	tr := newTestTracker(newFakeStore(), newFakeGitHub())
	for _, tc := range []struct {
		name   string
		repo   model.Repository
		factor int
	}{
		{"recent push", model.Repository{PushedAt: now.Add(-time.Hour)}, 1},
		{"two days idle", model.Repository{PushedAt: now.Add(-48 * time.Hour)}, 2},
		{"eight days idle", model.Repository{PushedAt: now.Add(-8 * 24 * time.Hour)}, 4},
		{"forty days idle", model.Repository{PushedAt: now.Add(-40 * 24 * time.Hour)}, 8},
		{"no activity", model.Repository{}, 8},
		{"recent PR", model.Repository{PullRequests: []model.PullRequest{{UpdatedAt: now}}}, 1},
		{"recent issue", model.Repository{Issues: []model.Issue{{UpdatedAt: now}}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tr.snapshotInterval(&tc.repo, now); got != time.Duration(tc.factor)*tr.syncInterval {
				t.Fatalf("interval = %s", got)
			}
		})
	}
}

func TestSyncAllPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st, gh := newFakeStore(), newFakeGitHub()
	tr := newTestTracker(st, gh)
	if _, err := tr.Track(ctx, "acme/widget"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if n, err := tr.SyncAll(ctx); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync = %d, %v", n, err)
	}
}
