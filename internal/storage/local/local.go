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

const maxUploadBytes = 10 * 1024 * 1024 // 10MB cap for local storage to prevent disk fill DoS

func (s *LocalStorage) sanitizeAndJoin(objectKey string) (string, error) {
	// P0 FIX path traversal: clean & reject .., ensure within basePath
	cleaned := filepath.Clean(objectKey)
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("invalid object key contains double dot")
	}
	absBase, err := filepath.Abs(s.basePath)
	if err != nil {
		return "", err
	}
	absPath := filepath.Join(absBase, cleaned)
	absPathAbs, err := filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absPathAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("object key escapes base path")
	}
	return absPathAbs, nil
}

func (s *LocalStorage) Upload(ctx context.Context, objectKey string, file io.Reader, size int64, contentType string) (string, error) {
	absPath, err := s.sanitizeAndJoin(objectKey)
	if err != nil {
		return "", fmt.Errorf("local storage upload: invalid key: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", fmt.Errorf("local storage upload: failed to create directories: %w", err)
	}

	out, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("local storage upload: failed to create file: %w", err)
	}
	defer func() { _ = out.Close() }()

	// P0 FIX disk fill DoS: limit copy 10MB + size hint check
	if size > 0 && size > maxUploadBytes {
		return "", fmt.Errorf("file too large >10MB")
	}
	limited := io.LimitReader(file, maxUploadBytes+1)
	n, err := io.Copy(out, limited)
	if err != nil {
		_ = os.Remove(absPath)
		return "", fmt.Errorf("local storage upload: failed to write file content: %w", err)
	}
	if n > maxUploadBytes {
		_ = os.Remove(absPath)
		return "", fmt.Errorf("file too large >10MB")
	}

	return objectKey, nil
}

func (s *LocalStorage) Delete(ctx context.Context, objectKey string) error {
	absPath, err := s.sanitizeAndJoin(objectKey)
	if err != nil {
		return nil // treat invalid as not found, not error
	}
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local storage delete: %w", err)
	}
	return nil
}

func (s *LocalStorage) Exists(ctx context.Context, objectKey string) (bool, error) {
	absPath, err2 := s.sanitizeAndJoin(objectKey)
	if err2 != nil {
		return false, nil
	}
	stat, err2 := os.Stat(absPath)
	if err2 == nil {
		_ = stat
		return true, nil
	}
	if os.IsNotExist(err2) {
		return false, nil
	}
	return false, fmt.Errorf("local storage exists check: %w", err2)
}

func (s *LocalStorage) SignedURL(ctx context.Context, objectKey string, expire time.Duration) (string, error) {
	return fmt.Sprintf("%s/%s", s.baseURL, strings.TrimPrefix(objectKey, "/")), nil
}
