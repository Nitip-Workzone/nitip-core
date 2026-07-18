package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/storage/local"
	"github.com/codecoffy/nitip-core/internal/storage/tencentcos"
)

type Storage interface {
	Upload(ctx context.Context, objectKey string, file io.Reader, size int64, contentType string) (string, error)
	Delete(ctx context.Context, objectKey string) error
	Exists(ctx context.Context, objectKey string) (bool, error)
	SignedURL(ctx context.Context, objectKey string, expire time.Duration) (string, error)
}

func NewFromEnv(cfg *config.Config) (Storage, error) {
	switch cfg.StorageDriver {
	case "local":
		return local.New(cfg.LocalStoragePath, cfg.LocalStorageBaseURL)
	case "tencent_cos":
		expire, err := time.ParseDuration(cfg.CosSignExpire)
		if err != nil {
			expire = 5 * time.Minute
		}
		return tencentcos.New(
			cfg.CosSecretID,
			cfg.CosSecretKey,
			cfg.CosRegion,
			cfg.CosBucket,
			cfg.CosBaseURL,
			expire,
		)
	default:
		return nil, fmt.Errorf("unsupported storage driver: %s", cfg.StorageDriver)
	}
}
