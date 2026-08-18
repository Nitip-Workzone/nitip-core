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

	// P0 FIX & ASSET_BASE_URL: baseURL (COS_BASE_URL) dipakai untuk upload endpoint (boleh myqcloud atau custom),
	// assetBaseURL (ASSET_BASE_URL / COS_CDN_BASE_URL) dipakai untuk SignedURL read final https://upload.nihtip.com/
	// Upload tetap langsung ke COS pakai API key/secret existing, tidak diubah.
	// Jika baseURL kosong atau mengandung myqcloud, pakai default myqcloud untuk upload agar tidak PUT via Cloudflare
	// (Cloudflare CDN hanya untuk GET cache, bukan untuk PUT).
	effectiveUploadURL := baseURL
	if effectiveUploadURL != "" {
		// Jika baseURL adalah https://upload.nihtip.com/ (custom CDN), itu juga valid untuk upload jika CNAME proxy PUT diperbolehkan.
		// Tapi jika ingin aman, tetap parse custom URL. Core logic: upload tetap jalan via cos client dengan AuthorizationTransport.
		bucketURL, err = url.Parse(effectiveUploadURL)
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid custom base url: %w", err)
		}
	} else {
		// Default Tencent COS URL format untuk upload: https://<bucket>.cos.<region>.myqcloud.com
		bucketURL, err = url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
		if err != nil {
			return nil, fmt.Errorf("tencent cos init: invalid bucket or region: %w", err)
		}
	}

	// Resolve final asset base URL untuk read — single source of truth
	// Fallback chain sudah di config, tapi di sini ensure tidak kosong & bukan myqcloud untuk keamanan default
	resolvedAssetBase := strings.TrimSpace(assetBaseURL)
	if resolvedAssetBase == "" {
		resolvedAssetBase = "https://upload.nihtip.com/"
	}
	if strings.Contains(resolvedAssetBase, "myqcloud.com") {
		resolvedAssetBase = "https://upload.nihtip.com/"
	}
	// Ensure scheme https
	if !strings.HasPrefix(resolvedAssetBase, "http://") && !strings.HasPrefix(resolvedAssetBase, "https://") {
		resolvedAssetBase = "https://" + resolvedAssetBase
	}
	resolvedAssetBase = strings.TrimSuffix(resolvedAssetBase, "/")

	// P0 #10 FIX: http client timeout 10s to prevent hang holding Fiber worker
	client := cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Timeout: 10 * time.Second,
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

	presignedURL, err := s.client.Object.GetPresignedURL(ctx, http.MethodGet, key, s.secretID, s.secretKey, dur, nil)
	if err != nil {
		return "", fmt.Errorf("generate signed URL on cos: %w", err)
	}

	// CDN cache optimization: jika COS_CDN_BASE_URL diset, rewrite signed URL ke CDN domain
	// Flow: client -> CDN edge (cache 30d, X-Cache: HIT) -> origin COS hanya 1x per file
	// Contoh: https://nihtip-user-upload-xxx.cos.ap-singapore.myqcloud.com/merchants/a.jpg?sign=xxx
	//      => https://cdn.nihtip.com/merchants/a.jpg?sign=xxx  (CDN akan forward sign ke origin & cache hasil)
	// Hemat: 1000 user buka list 15 merchant = 1x MISS + 999x HIT = 99.9% hemat GET + traffic
	if s.cdnBaseURL != "" {
		cdnBase := strings.TrimSuffix(s.cdnBaseURL, "/")
		// Replace bucket host dengan CDN host, keep path + query (signature)
		// presignedURL.String() = https://bucket.cos.region.myqcloud.com/key?sign...
		// kita ganti host-nya saja
		u := presignedURL
		if cdnURL, parseErr := url.Parse(cdnBase); parseErr == nil {
			u.Scheme = cdnURL.Scheme
			u.Host = cdnURL.Host
			u.Path = cdnURL.Path + "/" + key
			// jika cdnURL.Path kosong (hanya domain), tetap /key
			if cdnURL.Path == "" || cdnURL.Path == "/" {
				u.Path = "/" + key
			}
			return u.String(), nil
		}
	}

	return presignedURL.String(), nil
}
