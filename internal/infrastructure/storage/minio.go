package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinioStorage struct {
	client *minio.Client
	bucket string
}

func NewMinioStorage(cfg *config.Config) (*MinioStorage, error) {
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize minio client: %w", err)
	}

	return &MinioStorage{
		client: client,
		bucket: cfg.MinioBucketName,
	}, nil
}

func (s *MinioStorage) Upload(ctx context.Context, folder string, filename string, content io.Reader) (string, error) {
	ext := filepath.Ext(filename)
	objectKey := fmt.Sprintf("%s/%s%s", folder, uuid.New().String(), ext)

	_, err := s.client.PutObject(ctx, s.bucket, objectKey, content, -1, minio.PutObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to upload to minio: %w", err)
	}

	return objectKey, nil
}

func (s *MinioStorage) GetURL(ctx context.Context, objectKey string) (string, error) {
	// For Minio, usually we use signed URLs even for "viewable" images if they are sensitive.
	// But if the bucket is public, we could return a direct URL.
	// In Stage 6, we prefer Signed URLs for security.
	return s.GetSignedURL(ctx, objectKey, 1*time.Hour)
}

func (s *MinioStorage) GetSignedURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	reqParams := make(url.Values)
	presignedURL, err := s.client.PresignedGetObject(ctx, s.bucket, objectKey, expiry, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed url: %w", err)
	}
	return presignedURL.String(), nil
}
