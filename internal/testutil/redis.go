package testutil

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"go.uber.org/zap"
)

// NewMockRedis starts an in-memory miniredis server and returns the redis cache wrapper.
func NewMockRedis(t *testing.T) (*cache.Redis, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	cfg := &config.Config{
		RedisAddr: mr.Addr(),
	}

	redisCache, err := cache.NewRedis(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to initialize redis wrapper: %v", err)
	}

	t.Cleanup(func() {
		_ = redisCache.Close()
		mr.Close()
	})

	return redisCache, mr
}
