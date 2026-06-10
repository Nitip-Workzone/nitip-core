package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	firebase "firebase.google.com/go/v4"
	"github.com/codecoffy/nitip-core/config"
	"github.com/google/uuid"
)

type FirebaseStorage struct {
	bucket *storage.BucketHandle
	name   string
}

func NewFirebaseStorage(app *firebase.App, cfg *config.Config) (*FirebaseStorage, error) {
	if app == nil {
		return &FirebaseStorage{bucket: nil, name: "dummy"}, nil
	}

	client, err := app.Storage(context.Background())
	if err != nil {
		return nil, err
	}

	bucket, err := client.Bucket(cfg.FirebaseBucketName)
	if err != nil {
		return nil, err
	}

	return &FirebaseStorage{bucket: bucket, name: cfg.FirebaseBucketName}, nil
}

func (s *FirebaseStorage) Upload(ctx context.Context, folder string, filename string, content io.Reader) (string, error) {
	if s.bucket == nil {
		return fmt.Sprintf("%s/%s", folder, filename), nil
	}

	ext := filepath.Ext(filename)
	objectKey := fmt.Sprintf("%s/%s%s", folder, uuid.New().String(), ext)

	obj := s.bucket.Object(objectKey)
	wc := obj.NewWriter(ctx)
	if _, err := io.Copy(wc, content); err != nil {
		return "", err
	}
	if err := wc.Close(); err != nil {
		return "", err
	}

	return objectKey, nil
}

func (s *FirebaseStorage) GetURL(ctx context.Context, objectKey string) (string, error) {
	if s.bucket == nil {
		return "https://storage.dummy.id/" + objectKey, nil
	}
	
	encodedPath := url.PathEscape(objectKey)
	return fmt.Sprintf("https://firebasestorage.googleapis.com/v0/b/%s/o/%s?alt=media", s.name, encodedPath), nil
}

func (s *FirebaseStorage) GetSignedURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	if s.bucket == nil {
		return s.GetURL(ctx, objectKey)
	}

	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(expiry),
	}

	signedURL, err := s.bucket.SignedURL(objectKey, opts)
	if err != nil {
		return "", err
	}
	return signedURL, nil
}
