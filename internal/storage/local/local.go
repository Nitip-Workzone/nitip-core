package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LocalStorage struct {
	basePath string
	baseURL  string
}

func New(basePath, baseURL string) (*LocalStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("local storage init: failed to create base path: %w", err)
	}
	return &LocalStorage{
		basePath: basePath,
		baseURL:  strings.TrimSuffix(baseURL, "/"),
	}, nil
}

func (s *LocalStorage) Upload(ctx context.Context, objectKey string, file io.Reader, size int64, contentType string) (string, error) {
	absPath := filepath.Join(s.basePath, objectKey)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", fmt.Errorf("local storage upload: failed to create directories: %w", err)
	}

	out, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("local storage upload: failed to create file: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, file); err != nil {
		return "", fmt.Errorf("local storage upload: failed to write file content: %w", err)
	}

	return objectKey, nil
}

func (s *LocalStorage) Delete(ctx context.Context, objectKey string) error {
	absPath := filepath.Join(s.basePath, objectKey)
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local storage delete: %w", err)
	}
	return nil
}

func (s *LocalStorage) Exists(ctx context.Context, objectKey string) (bool, error) {
	absPath := filepath.Join(s.basePath, objectKey)
	_, err := os.Stat(absPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("local storage exists check: %w", err)
}

func (s *LocalStorage) SignedURL(ctx context.Context, objectKey string, expire time.Duration) (string, error) {
	return fmt.Sprintf("%s/%s", s.baseURL, strings.TrimPrefix(objectKey, "/")), nil
}
