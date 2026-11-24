package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gitsion/backend/internal/model"
	"gitsion/backend/internal/store"
)

// movedGitHub answers one tracked name with the repository it became, as
// GitHub does through the redirect it keeps after a rename or a transfer.
type movedGitHub struct {
	*fakeGitHub
	from    string // lower-case owner/name that was renamed
	toOwner string
	toName  string
}

func (g movedGitHub) move(repo *model.Repository, owner, name string) {
	if repoKey(owner, name) != g.from {
		return
	}
	repo.FullName = g.toOwner + "/" + g.toName
	repo.FullNameKey = strings.ToLower(repo.FullName)
	repo.Name = g.toName
	repo.Owner.Login = g.toOwner
	repo.Owner.LoginKey = strings.ToLower(g.toOwner)
}

func (g movedGitHub) FetchSnapshot(ctx context.Context, owner, name string) (*model.Repository, error) {
	repo, err := g.fakeGitHub.FetchSnapshot(ctx, owner, name)
	if err == nil {
		g.move(repo, owner, name)
	}
	return repo, err
}

func (g movedGitHub) FetchRepository(ctx context.Context, owner, name string) (*model.Repository, error) {
	repo, err := g.fakeGitHub.FetchRepository(ctx, owner, name)
	if err == nil {
		g.move(repo, owner, name)
	}
	return repo, err
}

func syncBy(t *testing.T, tr *Tracker, method string) error {
	t.Helper()
	ctx := context.Background()
	var err error
	switch method {
	case "refresh":
		_, err = tr.Refresh(ctx, "acme", "widget")
	case "sync_all":
		_, err = tr.SyncAll(ctx)
	case "background":
		_, err = tr.syncAll(ctx, false)
	default:
		t.Fatalf("unknown sync method %q", method)
	}
	return err
}

func TestRenamedRepositoryKeepsItsRecord(t *testing.T) {
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

			gh.snapshot.Stars = 99
			tr.github = movedGitHub{fakeGitHub: gh, from: "acme/widget", toOwner: "Acme-Labs", toName: "Gadget"}
			if err := syncBy(t, tr, method); err != nil {
				t.Fatalf("sync after the rename: %v", err)
			}

			if _, err := st.Get(ctx, "acme", "widget"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("old name is still stored: %v", err)
			}
			moved, err := st.Get(ctx, "acme-labs", "gadget")
			if err != nil {
				t.Fatalf("record under the new name: %v", err)
			}
			if moved.FullName != "Acme-Labs/Gadget" || moved.Stars != 99 || !moved.TrackedAt.Equal(before.TrackedAt) {
				t.Fatalf("the record must follow the rename with fresh data and its tracking date: %+v", moved)
			}
			repos, err := tr.List(ctx, "")
			if err != nil || len(repos) != 1 || repos[0].FullName != "Acme-Labs/Gadget" {
				t.Fatalf("List = %+v, %v", repos, err)
			}

			// The next pass syncs it under its new name like any other repository.
			if _, err := tr.SyncAll(ctx); err != nil {
				t.Fatalf("sync under the new name: %v", err)
			}
		})
	}
}

func TestRenamedRepositoryAlreadyTrackedDropsTheOldRecord(t *testing.T) {
	for _, method := range []string{"refresh", "sync_all", "background"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			st, gh := newFakeStore(), newFakeGitHub()
			tr := newTestTracker(st, gh)
			tr.manualCooldown = 0
			if _, err := tr.Track(ctx, "acme/widget"); err != nil {
				t.Fatal(err)
			}
			kept, err := tr.Track(ctx, "acme/gadget")
			if err != nil {
				t.Fatal(err)
			}
			st.repos["acme/widget"].TrackedAt = kept.TrackedAt.Add(-24 * time.Hour)

			tr.github = movedGitHub{fakeGitHub: gh, from: "acme/widget", toOwner: "acme", toName: "gadget"}
			if err := syncBy(t, tr, method); err != nil {
				t.Fatalf("sync after the rename: %v", err)
			}

			repos, err := tr.List(ctx, "")
			if err != nil || len(repos) != 1 || repos[0].FullName != "acme/gadget" {
				t.Fatalf("only the record under the current name must remain: %+v, %v", repos, err)
			}
			if !repos[0].TrackedAt.Equal(kept.TrackedAt) {
				t.Fatalf("the surviving record must keep its own tracking date: %v", repos[0].TrackedAt)
			}
		})
	}
}

func TestRenameWaitsForAPublicSnapshot(t *testing.T) {
	ctx := context.Background()
	st, gh := newFakeStore(), newFakeGitHub()
	tr := newTestTracker(st, gh)
	if _, err := tr.Track(ctx, "acme/widget"); err != nil {
		t.Fatal(err)
	}

	gh.snapshot.Visibility = "private"
	tr.github = movedGitHub{fakeGitHub: gh, from: "acme/widget", toOwner: "acme", toName: "gadget"}
	if _, err := tr.SyncAll(ctx); !errors.Is(err, ErrPrivateRepo) {
		t.Fatalf("sync error = %v, want ErrPrivateRepo", err)
	}

	hidden, err := st.Get(ctx, "acme", "widget")
	if err != nil || !hidden.Hidden {
		t.Fatalf("a renamed repository that is no longer public must stay quarantined in place: %+v, %v", hidden, err)
	}
	if _, err := st.Get(ctx, "acme", "gadget"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("nothing may be stored under the new name: %v", err)
	}
}
