package contract

import (
	"context"
	"time"
)

type RedisClient interface {
	Persist(ctx context.Context, key string, value []byte, ttl time.Duration) error
	PersistMultiple(ctx context.Context, values map[string][]byte, ttl time.Duration) error
	Fetch(ctx context.Context, key string) ([]byte, error)
	FetchMultiple(ctx context.Context, keys []string) (map[string][]byte, error)
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, keys ...string) (int64, error)
	Exists(ctx context.Context, keys ...string) (int64, error)
	AddSetMembers(ctx context.Context, key string, members ...string) error
	GetSetMembers(ctx context.Context, key string) ([]string, error)
	HashSet(ctx context.Context, key string, values map[string][]byte) error
	HashGet(ctx context.Context, key, field string) ([]byte, error)
	HashGetMany(ctx context.Context, key string, fields ...string) (map[string][]byte, error)
	HashGetAll(ctx context.Context, key string) (map[string][]byte, error)
	Close() error
}
