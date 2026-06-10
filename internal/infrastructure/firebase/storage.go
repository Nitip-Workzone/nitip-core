package firebase

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"

	"cloud.google.com/go/storage"
	firebase "firebase.google.com/go/v4"
	"github.com/codecoffy/nitip-core/config"
	"github.com/google/uuid"
)

type StorageService struct {
	bucket *storage.BucketHandle
	name   string
}

func NewStorage(app *firebase.App, cfg *config.Config) (*StorageService, error) {
	if app == nil {
		return &StorageService{
			bucket: nil,
			name:   "dummy-bucket",
		}, nil
	}

	ctx := context.Background()
	client, err := app.Storage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage client: %w", err)
	}

	bucket, err := client.Bucket(cfg.FirebaseBucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket: %w", err)
	}

	return &StorageService{
		bucket: bucket,
		name:   cfg.FirebaseBucketName,
	}, nil
}

// UploadFile uploads a reader to Firebase Storage and returns the public URL
func (s *StorageService) UploadFile(ctx context.Context, folder string, filename string, content io.Reader) (string, error) {
	// If in dummy mode, return a placeholder URL
	if s.bucket == nil {
		return fmt.Sprintf("https://storage.dummy.id/%s/%s", folder, filename), nil
	}

	// Generate unique filename
	ext := filepath.Ext(filename)
	objName := fmt.Sprintf("%s/%s%s", folder, uuid.New().String(), ext)

	obj := s.bucket.Object(objName)
	wc := obj.NewWriter(ctx)
	if _, err := io.Copy(wc, content); err != nil {
		return "", fmt.Errorf("failed to copy file to storage: %w", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed to close storage writer: %w", err)
	}

	// Make the object publicly accessible (optional, depends on Firebase Rule)
	// For production, we usually use signed URLs or just rely on Firebase's public URL format
	// Firebase standard public URL: https://firebasestorage.googleapis.com/v0/b/<bucket>/o/<encoded_path>?alt=media
	encodedPath := url.PathEscape(objName)
	publicURL := fmt.Sprintf("https://firebasestorage.googleapis.com/v0/b/%s/o/%s?alt=media", s.name, encodedPath)

	return publicURL, nil
}

