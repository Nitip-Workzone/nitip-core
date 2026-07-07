package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type LocalStorage struct {
	basePath string
	baseURL  string
}

func NewLocalStorage(basePath, baseURL string) (*LocalStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, err
	}
	return &LocalStorage{basePath: basePath, baseURL: baseURL}, nil
}

func (s *LocalStorage) Upload(ctx context.Context, folder string, filename string, content io.Reader) (string, error) {
	ext := filepath.Ext(filename)
	relPath := filepath.Join(folder, uuid.New().String()+ext)
	absPath := filepath.Join(s.basePath, relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", err
	}

	out, err := os.Create(absPath)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, content); err != nil {
		return "", err
	}

	return relPath, nil
}

func (s *LocalStorage) GetURL(ctx context.Context, objectKey string) (string, error) {
	// For local development, we just return a static file path URL
	return fmt.Sprintf("%s/uploads/%s", s.baseURL, objectKey), nil
}

func (s *LocalStorage) GetSignedURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	// Local doesn't support signing easily, just return same URL
	return s.GetURL(ctx, objectKey)
}
