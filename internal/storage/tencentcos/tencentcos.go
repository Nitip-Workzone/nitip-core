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
	cdnBaseURL    string // jika diisi, SignedURL pakai CDN domain (hemat traffic, cache 30d di edge)
}

func New(secretID, secretKey, region, bucket, baseURL string, assetBaseURL string, defaultExpire time.Duration) (*CosStorage, error) {
	var bucketURL *url.URL
	var err error

	// P0 FIX & ASSET_BASE_URL: baseURL = COS_BASE_URL untuk upload endpoint (myqcloud direct), assetBaseURL = ASSET_BASE_URL untuk read https://upload.nihtip.com/
	// Upload tetap langsung ke COS myqcloud via API key/secret existing, read via CDN custom domain
	// Bug fix local banner gagal load: sebelumnya ketika baseURL="" upload ke myqcloud tapi presigned URL juga untuk myqcloud host,
	// lalu rewrite host ke upload.nihtip.com tanpa re-sign → q-header-list=host mismatch → 403 SignatureDoesNotMatch
	// Sekarang: buat 2 client — uploadClient untuk PUT ke myqcloud, signClient untuk GetPresignedURL dengan host upload.nihtip.com agar signature valid untuk CDN
	effectiveUploadURL := baseURL
	if effectiveUploadURL != "" {
		bucketURL, err = url.Parse(effectiveUploadURL)
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid custom base url: %w", err)
		}
	} else {
		bucketURL, err = url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid bucket or region: %w", err)
		}
	}

	resolvedAssetBase := strings.TrimSpace(assetBaseURL)
	if resolvedAssetBase == "" {
		resolvedAssetBase = "https://upload.nihtip.com/"
	}
	if strings.Contains(resolvedAssetBase, "myqcloud.com") {
		resolvedAssetBase = "https://upload.nihtip.com/"
	}
	if !strings.HasPrefix(resolvedAssetBase, "http://") && !strings.HasPrefix(resolvedAssetBase, "https://") {
		resolvedAssetBase = "https://" + resolvedAssetBase
	}
	resolvedAssetBase = strings.TrimSuffix(resolvedAssetBase, "/")

	// Upload client: BucketURL = myqcloud endpoint (or custom baseURL if set) — untuk PUT Object
	uploadClient := cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Timeout: 10 * time.Second,
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})

	// Use uploadClient as primary for existence check, delete, upload
	client := uploadClient

	return &CosStorage{
		client:        client,
		secretID:      secretID,
		secretKey:     secretKey,
		defaultExpire: defaultExpire,
		cdnBaseURL:    resolvedAssetBase,
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

	// Fix banner gagal load di local: https://upload.nihtip.com/banners/...jpg?q-sign... 403 SignatureDoesNotMatch
	// Penyebab: sebelumnya GetPresignedURL pakai client BucketURL = myqcloud (https://bucket.cos.ap-jakarta.myqcloud.com)
	// lalu rewrite host ke upload.nihtip.com tanpa re-sign → q-header-list=host mismatch, signature invalid.
	// Solusi: generate presigned URL dengan BucketURL = https://upload.nihtip.com/ (cdnBaseURL) agar signature dibuat untuk host upload.nihtip.com
	// Cloudflare Orange Cloud CNAME upload.nihtip.com -> COS bucket, jadi origin tetap valid dengan signature untuk host tersebut
	if s.cdnBaseURL != "" {
		cdnBase := strings.TrimSuffix(s.cdnBaseURL, "/")
		if cdnURL, parseErr := url.Parse(cdnBase); parseErr == nil {
			// Buat client khusus untuk signing dengan BucketURL = cdnBase (upload.nihtip.com)
			// Ini memastikan q-header-list host = upload.nihtip.com, signature valid ketika fetch via CDN
			signClient := cos.NewClient(&cos.BaseURL{BucketURL: cdnURL}, &http.Client{
				Timeout: 10 * time.Second,
				Transport: &cos.AuthorizationTransport{
					SecretID:  s.secretID,
					SecretKey: s.secretKey,
				},
			})
			presignedURL, err := signClient.Object.GetPresignedURL(ctx, http.MethodGet, key, s.secretID, s.secretKey, dur, nil)
			if err != nil {
				return "", fmt.Errorf("generate signed URL on cos (cdn): %w", err)
			}
			return presignedURL.String(), nil
		}
	}

	// Fallback: no cdnBase, use default myqcloud client
	presignedURL, err := s.client.Object.GetPresignedURL(ctx, http.MethodGet, key, s.secretID, s.secretKey, dur, nil)
	if err != nil {
		return "", fmt.Errorf("generate signed URL on cos: %w", err)
	}
	return presignedURL.String(), nil
}
