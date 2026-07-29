package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

func RateLimit(redis *cache.Redis, max int, duration time.Duration) fiber.Handler {
	// If redis is nil, fallback to in-memory storage via fallback
	var storage fiber.Storage
	if redis != nil {
		storage = &redisStorage{redis: redis}
	} else {
		storage = &fallbackMemoryStorage{
			data: make(map[string]item),
		}
	}
	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: duration,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":" + c.Path()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return response.Custom(c, 429, "Terlalu banyak permintaan. Silakan coba lagi nanti.", nil)
		},
		Storage: storage,
	})
}

// ── Redis Storage with nil guard + timeout ─────

type redisStorage struct {
	redis *cache.Redis
}

func (s *redisStorage) Get(key string) ([]byte, error) {
	if s.redis == nil || s.redis.Client() == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, err := s.redis.Get(ctx, key)
	return []byte(val), err
}

func (s *redisStorage) Set(key string, val []byte, exp time.Duration) error {
	if s.redis == nil || s.redis.Client() == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.redis.Set(ctx, key, string(val), exp)
}

func (s *redisStorage) Delete(key string) error {
	if s.redis == nil || s.redis.Client() == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.redis.Del(ctx, key)
}

func (s *redisStorage) Reset() error {
	return nil // Not supported for now
}

func (s *redisStorage) Close() error {
	return nil
}

// ── Fallback in-memory storage when redis nil ──

type item struct {
	val []byte
	exp time.Time
}

type fallbackMemoryStorage struct {
	mu   sync.Mutex
	data map[string]item
}

func (m *fallbackMemoryStorage) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	if !it.exp.IsZero() && time.Now().After(it.exp) {
		delete(m.data, key)
		return nil, nil
	}
	return it.val, nil
}

func (m *fallbackMemoryStorage) Set(key string, val []byte, exp time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var e time.Time
	if exp > 0 {
		e = time.Now().Add(exp)
	}
	m.data[key] = item{val: val, exp: e}
	return nil
}

func (m *fallbackMemoryStorage) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *fallbackMemoryStorage) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string]item)
	return nil
}

func (m *fallbackMemoryStorage) Close() error {
	return nil
}
