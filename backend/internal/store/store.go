// Package store defines persistence for tracked repositories.
package store

import (
	"context"
	"errors"

	"gitsion/backend/internal/model"
)

var (
	// ErrNotFound means the repository is not tracked.
	ErrNotFound = errors.New("store: repository not found")
	// ErrDuplicate means the repository is already tracked.
	ErrDuplicate = errors.New("store: repository already tracked")
)

// RepositoryStore is the persistence boundary used by the tracker service.
// Implementations must treat owner and repository names case-insensitively.
type RepositoryStore interface {
	List(ctx context.Context, owner string) ([]model.Repository, error)
	Get(ctx context.Context, owner, name string) (*model.Repository, error)
	Create(ctx context.Context, repo *model.Repository) error
	ReplaceSnapshot(ctx context.Context, repo *model.Repository) error
	Delete(ctx context.Context, owner, name string) (bool, error)
	Ping(ctx context.Context) error
}
