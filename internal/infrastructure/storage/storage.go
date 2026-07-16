package storage

import (
	"context"
	"io"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/codecoffy/nitip-core/config"
)

// Storage defines the interface for all storage backends (Firebase, Minio, Local)
type Storage interface {
	// Upload uploads a file and returns the relative path/key
	Upload(ctx context.Context, folder string, filename string, content io.Reader) (string, error)

	// GetURL returns a viewable URL (Public or Signed depending on driver)
	GetURL(ctx context.Context, objectKey string) (string, error)

	// GetSignedURL returns a temporary URL for private objects
	GetSignedURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error)
}

func NewStorage(cfg *config.Config, firebaseApp *firebase.App) (Storage, error) {
	switch cfg.StorageDriver {
	case "minio":
		return NewMinioStorage(cfg)
	case "firebase":
		return NewFirebaseStorage(firebaseApp, cfg)
	case "local":
		return NewLocalStorage("./uploads", cfg.StorageBaseURL)
	default:
		return NewLocalStorage("./uploads", cfg.StorageBaseURL)
	}
}
