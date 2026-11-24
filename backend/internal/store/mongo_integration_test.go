//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gitsion/backend/internal/model"
	"gitsion/backend/internal/store"
)

// openStore connects to a fresh database with the production indexes.
func openStore(t *testing.T) (*store.MongoStore, context.Context) {
	t.Helper()
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	dbName := fmt.Sprintf("gitsion_test_%d", time.Now().UnixNano())
	st, err := store.NewMongo(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("NewMongo: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = st.Close(cleanup)
	})

	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return st, ctx
}

func TestMongoStoreLifecycle(t *testing.T) {
	st, ctx := openStore(t)

	repo := &model.Repository{
		FullName:  "Acme/Widget",
		Name:      "Widget",
		Owner:     model.Owner{Login: "Acme", Type: "Organization"},
		Stars:     10,
		Tags:      []model.Tag{{Name: "v1.0.0", CommitSHA: "abc"}},
		Issues:    []model.Issue{},
		TrackedAt: time.Now().UTC(),
	}

	if err := st.Create(ctx, repo); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.Create(ctx, repo); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("duplicate Create = %v, want ErrDuplicate", err)
	}

	got, err := st.Get(ctx, "ACME", "WIDGET")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FullName != "Acme/Widget" || got.Owner.LoginKey != "acme" {
		t.Fatalf("Get returned %+v", got)
	}

	got.Stars = 99
	got.LastSyncedAt = time.Now().UTC()
	got.Hidden = true
	got.NextSyncAt = time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	if err := st.ReplaceSnapshot(ctx, got); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}
	updated, err := st.Get(ctx, "acme", "widget")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if updated.Stars != 99 || updated.TrackedAt.IsZero() || !updated.Hidden || !updated.NextSyncAt.Equal(got.NextSyncAt) {
		t.Fatalf("updated repo = %+v", updated)
	}

	repos, err := st.List(ctx, "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("List returned %d repositories", len(repos))
	}
	other, err := st.List(ctx, "nobody")
	if err != nil {
		t.Fatalf("List other owner: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("List other owner returned %d repositories", len(other))
	}

	deleted, err := st.Delete(ctx, "acme", "widget")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}
	if _, err := st.Get(ctx, "acme", "widget"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestMongoStoreRename(t *testing.T) {
	st, ctx := openStore(t)

	tracked := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Millisecond)
	for _, repo := range []*model.Repository{
		{FullName: "Acme/Widget", Name: "Widget", Owner: model.Owner{Login: "Acme"}, Stars: 1, TrackedAt: tracked},
		{FullName: "acme/taken", Name: "taken", Owner: model.Owner{Login: "acme"}, Stars: 2},
	} {
		if err := st.Create(ctx, repo); err != nil {
			t.Fatalf("Create %s: %v", repo.FullName, err)
		}
	}
	renamed := func(fullName string) *model.Repository {
		owner, name, _ := strings.Cut(fullName, "/")
		return &model.Repository{FullName: fullName, Name: name, Owner: model.Owner{Login: owner},
			Stars: 99, TrackedAt: tracked}
	}

	// A name that is already tracked is refused and both records stay as they were.
	if err := st.Rename(ctx, "acme", "widget", renamed("Acme/Taken")); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("Rename onto a tracked name = %v, want ErrDuplicate", err)
	}
	for name, stars := range map[string]int{"widget": 1, "taken": 2} {
		got, err := st.Get(ctx, "acme", name)
		if err != nil || got.Stars != stars {
			t.Fatalf("%s after the refused rename = %+v, %v", name, got, err)
		}
	}

	if err := st.Rename(ctx, "ACME", "WIDGET", renamed("Acme-Labs/Gadget")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := st.Get(ctx, "acme", "widget"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get old name = %v, want ErrNotFound", err)
	}
	moved, err := st.Get(ctx, "acme-labs", "gadget")
	if err != nil {
		t.Fatalf("Get new name: %v", err)
	}
	if moved.FullName != "Acme-Labs/Gadget" || moved.Owner.LoginKey != "acme-labs" || moved.Stars != 99 ||
		!moved.TrackedAt.Equal(tracked) {
		t.Fatalf("renamed repo = %+v", moved)
	}
	byOwner, err := st.List(ctx, "Acme-Labs")
	if err != nil || len(byOwner) != 1 {
		t.Fatalf("List by the new owner = %d repositories, %v", len(byOwner), err)
	}

	// A record deleted in the meantime must not come back under the new name.
	if err := st.Rename(ctx, "acme", "widget", renamed("acme/revived")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Rename of a missing record = %v, want ErrNotFound", err)
	}
	if _, err := st.Get(ctx, "acme", "revived"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get revived = %v, want ErrNotFound", err)
	}
}
