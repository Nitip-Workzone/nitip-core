package middleware

import (
	"context"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

func RateLimit(redis *cache.Redis, max int, duration time.Duration) fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: duration,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":" + c.Path()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return response.Custom(c, 429, "Terlalu banyak permintaan. Silakan coba lagi nanti.", nil)
		},
		Storage: &redisStorage{redis: redis},
	})
}

// redisStorage implements limiter.Storage for Fiber Limiter
type redisStorage struct {
	redis *cache.Redis
}

func (s *redisStorage) Get(key string) ([]byte, error) {
	val, err := s.redis.Get(context.Background(), key)
	return []byte(val), err
}

func (s *redisStorage) Set(key string, val []byte, exp time.Duration) error {
	return s.redis.Set(context.Background(), key, string(val), exp)
}

func (s *redisStorage) Delete(key string) error {
	return s.redis.Del(context.Background(), key)
}

func (s *redisStorage) Reset() error {
	return nil // Not supported for now
}

func (s *redisStorage) Close() error {
	return nil
}
