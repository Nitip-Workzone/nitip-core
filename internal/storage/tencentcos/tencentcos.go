package tencentcos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

type CosStorage struct {
	client        *cos.Client
	secretID      string
	secretKey     string
	defaultExpire time.Duration
}

func New(secretID, secretKey, region, bucket, baseURL string, defaultExpire time.Duration) (*CosStorage, error) {
	var bucketURL *url.URL
	var err error

	if baseURL != "" {
		bucketURL, err = url.Parse(baseURL)
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid custom base url: %w", err)
		}
	} else {
		// Default Tencent COS URL format: https://<bucket>.cos.<region>.myqcloud.com
		bucketURL, err = url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid bucket or region: %w", err)
		}
	}

	client := cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})

	return &CosStorage{
		client:        client,
		secretID:      secretID,
		secretKey:     secretKey,
		defaultExpire: defaultExpire,
	}, nil
}

func (s *CosStorage) Upload(ctx context.Context, objectKey string, file io.Reader, size int64, contentType string) (string, error) {
	// Clean object key
	key := strings.TrimPrefix(objectKey, "/")

	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: contentType,
		},
	}
	if size > 0 {
		opt.ContentLength = size
	}

	_, err := s.client.Object.Put(ctx, key, file, opt)
	if err != nil {
		return "", fmt.Errorf("upload to cos: %w", err)
	}

	return key, nil
}

func (s *CosStorage) Delete(ctx context.Context, objectKey string) error {
	key := strings.TrimPrefix(objectKey, "/")
	_, err := s.client.Object.Delete(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("delete from cos: %w", err)
	}
	return nil
}

func (s *CosStorage) Exists(ctx context.Context, objectKey string) (bool, error) {
	key := strings.TrimPrefix(objectKey, "/")
	_, err := s.client.Object.Head(ctx, key, nil)
	if err != nil {
		if cosErr, ok := err.(*cos.ErrorResponse); ok && cosErr.Response != nil && cosErr.Response.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("exists check on cos: %w", err)
	}
	return true, nil
}

func (s *CosStorage) SignedURL(ctx context.Context, objectKey string, expire time.Duration) (string, error) {
	key := strings.TrimPrefix(objectKey, "/")
	dur := expire
	if dur <= 0 {
		dur = s.defaultExpire
	}

	presignedURL, err := s.client.Object.GetPresignedURL(ctx, http.MethodGet, key, s.secretID, s.secretKey, dur, nil)
	if err != nil {
		return "", fmt.Errorf("generate signed URL on cos: %w", err)
	}

	return presignedURL.String(), nil
}
