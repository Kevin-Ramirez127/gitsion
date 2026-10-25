package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"gitsion/backend/internal/model"
)

// MongoStore persists repositories in a single MongoDB collection.
type MongoStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// NewMongo connects to MongoDB and verifies the connection.
func NewMongo(ctx context.Context, uri, database string) (*MongoStore, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo: ping: %w", err)
	}

	return &MongoStore{
		client: client,
		coll:   client.Database(database).Collection("repositories"),
	}, nil
}

// EnsureIndexes creates the indexes the API relies on. It is safe to run on
// every start.
func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "full_name_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_full_name_key"),
		},
		{
			Keys:    bson.D{{Key: "owner.login_key", Value: 1}},
			Options: options.Index().SetName("owner_login_key"),
		},
	}
	if _, err := s.coll.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("mongo: create indexes: %w", err)
	}
	return nil
}

// Close disconnects the client.
func (s *MongoStore) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

func (s *MongoStore) List(ctx context.Context, owner string) ([]model.Repository, error) {
	filter := bson.M{}
	if owner = strings.TrimSpace(owner); owner != "" {
		filter["owner.login_key"] = strings.ToLower(owner)
	}

	cursor, err := s.coll.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "full_name_key", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("mongo: list repositories: %w", err)
	}
	defer cursor.Close(ctx)

	repos := make([]model.Repository, 0)
	if err := cursor.All(ctx, &repos); err != nil {
		return nil, fmt.Errorf("mongo: decode repositories: %w", err)
	}
	return repos, nil
}

func (s *MongoStore) Get(ctx context.Context, owner, name string) (*model.Repository, error) {
	var repo model.Repository
	err := s.coll.FindOne(ctx, bson.M{"full_name_key": key(owner, name)}).Decode(&repo)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mongo: get repository: %w", err)
	}
	return &repo, nil
}

func (s *MongoStore) Create(ctx context.Context, repo *model.Repository) error {
	repo.FullNameKey = strings.ToLower(repo.FullName)
	repo.Owner.LoginKey = strings.ToLower(repo.Owner.Login)
	if _, err := s.coll.InsertOne(ctx, repo); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("mongo: insert repository: %w", err)
	}
	return nil
}

func (s *MongoStore) ReplaceSnapshot(ctx context.Context, repo *model.Repository) error {
	repo.FullNameKey = strings.ToLower(repo.FullName)
	repo.Owner.LoginKey = strings.ToLower(repo.Owner.Login)

	result, err := s.coll.ReplaceOne(ctx, bson.M{"full_name_key": repo.FullNameKey}, repo)
	if err != nil {
		return fmt.Errorf("mongo: replace repository: %w", err)
	}
	if result.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *MongoStore) Delete(ctx context.Context, owner, name string) (bool, error) {
	result, err := s.coll.DeleteOne(ctx, bson.M{"full_name_key": key(owner, name)})
	if err != nil {
		return false, fmt.Errorf("mongo: delete repository: %w", err)
	}
	return result.DeletedCount > 0, nil
}

func (s *MongoStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx, readpref.Primary())
}

func key(owner, name string) string {
	return strings.ToLower(strings.TrimSpace(owner) + "/" + strings.TrimSpace(name))
}
