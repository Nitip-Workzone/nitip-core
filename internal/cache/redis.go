package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Redis struct {
	client *redis.Client
	logger *zap.Logger
}

func NewRedis(cfg *config.Config, logger *zap.Logger) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	logger.Info("redis connected", zap.String("addr", cfg.RedisAddr))
	return &Redis{client: client, logger: logger}, nil
}

func (r *Redis) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (r *Redis) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

func (r *Redis) Del(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

func (r *Redis) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.client.Exists(ctx, key).Result()
	return n > 0, err
}

func (r *Redis) Client() *redis.Client {
	return r.client
}

func (r *Redis) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	//nolint:staticcheck // SA1019: SetNX is deprecated but still works fine in v9
	return r.client.SetNX(ctx, key, "locked", ttl).Result()
}

func (r *Redis) ReleaseLock(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

func (r *Redis) Close() error {
	return r.client.Close()
}
