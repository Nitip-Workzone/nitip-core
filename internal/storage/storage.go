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
		// Prod guard: jika ASSET_BASE_URL set, local driver tetap pakai ASSET_BASE_URL untuk SignedURL agar tidak leak localhost
		asset := cfg.AssetBaseURL
		if asset == "" {
			asset = cfg.CosCDNBaseURL
		}
		if asset == "" {
			asset = cfg.CosBaseURL
		}
		if asset == "" || cfg.AppEnv == "development" {
			// dev: keep local base for debugging, but if asset is upload.nihtip.com prod default, it will be used in prod
			return local.New(cfg.LocalStoragePath, cfg.LocalStorageBaseURL, cfg.AssetBaseURL)
		}
		// prod: force asset base for read, even when driver=local accidentally
		return local.New(cfg.LocalStoragePath, asset, cfg.AssetBaseURL)
	case "tencent_cos":
		expire, err := time.ParseDuration(cfg.CosSignExpire)
		if err != nil {
			expire = 5 * time.Minute
		}
		// P0 FIX + ASSET_BASE_URL: upload endpoint = CosBaseURL (optional myqcloud or custom), read final = AssetBaseURL default https://upload.nihtip.com/
		return tencentcos.New(
			cfg.CosSecretID,
			cfg.CosSecretKey,
			cfg.CosRegion,
			cfg.CosBucket,
			cfg.CosBaseURL,
			cfg.AssetBaseURL,
			expire,
		)
	default:
		return nil, fmt.Errorf("unsupported storage driver: %s", cfg.StorageDriver)
	}
}
