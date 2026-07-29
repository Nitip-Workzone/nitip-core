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
	// P1: tune pool size for 512M prod — 50 pool vs 10 default bottleneck under 50 DB conns + 10 workers + HTTP handlers
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		PoolSize:     50,
		MinIdleConns: 10,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		MaxRetries:   1,
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

func (r *Redis) AcquireLock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	//nolint:staticcheck // SA1019: SetNX is deprecated but still works fine in v9
	ok, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return token, nil
}

func (r *Redis) ReleaseLock(ctx context.Context, key string, token string) error {
	if token == "" {
		return nil
	}
	script := redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`)
	return script.Run(ctx, r.client, []string{key}, token).Err()
}

func (r *Redis) GeoAddOrder(ctx context.Context, orderID string, lat, lng float64) error {
	return r.client.GeoAdd(ctx, "orders:live", &redis.GeoLocation{
		Name:      orderID,
		Longitude: lng,
		Latitude:  lat,
	}).Err()
}

func (r *Redis) GeoRemoveOrder(ctx context.Context, orderID string) error {
	return r.client.ZRem(ctx, "orders:live", orderID).Err()
}

func (r *Redis) GeoSearchOrders(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return r.client.GeoSearch(ctx, "orders:live", &redis.GeoSearchQuery{
		Longitude:  lng,
		Latitude:   lat,
		Radius:     radiusKm,
		RadiusUnit: "km",
	}).Result()
}

func (r *Redis) Close() error {
	return r.client.Close()
}
