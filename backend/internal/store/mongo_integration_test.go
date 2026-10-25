//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gitsion/backend/internal/model"
	"gitsion/backend/internal/store"
)

func TestMongoStoreLifecycle(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("gitsion_test_%d", time.Now().UnixNano())
	st, err := store.NewMongo(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("NewMongo: %v", err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = st.Close(cleanup)
	}()

	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

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
	if err := st.ReplaceSnapshot(ctx, got); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}
	updated, err := st.Get(ctx, "acme", "widget")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if updated.Stars != 99 || updated.TrackedAt.IsZero() {
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
