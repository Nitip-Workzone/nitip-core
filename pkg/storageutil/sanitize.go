package storageutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DefaultAssetBaseURL is single source of truth default — https://upload.nihtip.com/
const DefaultAssetBaseURL = "https://upload.nihtip.com/"

// SanitizeStorageKey extracts storage key from various URL formats
// Supports:
// - relative key: banners/foo.jpg -> banners/foo.jpg
// - /uploads/banners/foo.jpg -> banners/foo.jpg
// - https://bucket.cos.ap-singapore.myqcloud.com/banners/foo.jpg?sign -> banners/foo.jpg
// - http://localhost:8000/uploads/banners/foo.jpg -> banners/foo.jpg
// - https://upload.nihtip.com/banners/foo.jpg?sign -> banners/foo.jpg
func SanitizeStorageKey(raw string) string {
	if raw == "" {
		return ""
	}
	u := strings.TrimSpace(raw)

	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		// strip scheme
		if strings.HasPrefix(u, "https://") {
			u = strings.TrimPrefix(u, "https://")
		} else {
			u = strings.TrimPrefix(u, "http://")
		}
		// find first slash after host
		if idx := strings.Index(u, "/"); idx != -1 {
			u = u[idx+1:]
		} else {
			// host only without path -> empty
			return ""
		}
	}

	// strip leading uploads/
	u = strings.TrimPrefix(u, "/")
	u = strings.TrimPrefix(u, "uploads/")
	// strip query params ?q-sign...
	if qIdx := strings.Index(u, "?"); qIdx != -1 {
		u = u[:qIdx]
	}
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(u, "/")
	return u
}

// IsAlreadyAssetURL checks if url already starts with final asset base e.g. https://upload.nihtip.com/
func IsAlreadyAssetURL(urlStr, assetBase string) bool {
	if urlStr == "" {
		return false
	}
	base := strings.TrimSuffix(assetBase, "/")
	if base == "" {
		base = DefaultAssetBaseURL
		base = strings.TrimSuffix(base, "/")
	}
	return strings.HasPrefix(urlStr, base+"/")
}

// ResolveAssetBase ensures non-empty asset base with default https://upload.nihtip.com/
func ResolveAssetBase(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return DefaultAssetBaseURL
	}
	if strings.Contains(s, "myqcloud.com") {
		// never use myqcloud as final read base — default to upload.nihtip.com
		return DefaultAssetBaseURL
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return strings.TrimSuffix(s, "/") + "/"
}

// UniqueObjectKey generates cache-busting unique key for CDN https://upload.nihtip.com/
// Format: prefix/uuid8_nanotime_original (or .jpg)
// Ensures tiap re-upload nama beda agar CDN tidak serve file lama (stale cache)
// Example: UniqueObjectKey("avatars", "foo.jpg") -> avatars/a1b2c3d4_171334..._foo.jpg
// Standard: prefix/<uuid8>_<nanotime>_<filename> — upload ke COS, read via ASSET_BASE_URL
func UniqueObjectKey(prefix, originalName string) string {
	ext := filepath.Ext(originalName)
	name := strings.TrimSuffix(originalName, ext)
	if name == "" {
		name = "image"
	}
	// sanitize filename: replace spaces etc
	name = strings.ReplaceAll(name, " ", "_")
	if ext == "" {
		ext = ".jpg"
	}
	uid := uuid.New().String()[:8]
	ts := time.Now().UnixNano()
	if prefix == "" {
		return fmt.Sprintf("%s_%d_%s%s", uid, ts, name, ext)
	}
	prefix = strings.Trim(prefix, "/")
	return fmt.Sprintf("%s/%s_%d_%s%s", prefix, uid, ts, name, ext)
}

// UniqueKeySimple generates short unique key: prefix/uuid_nano.ext
func UniqueKeySimple(prefix, ext string) string {
	if ext == "" {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	prefix = strings.Trim(prefix, "/")
	uid := uuid.New().String()
	ts := time.Now().UnixNano()
	if prefix == "" {
		return fmt.Sprintf("%s_%d%s", uid, ts, ext)
	}
	return fmt.Sprintf("%s/%s_%d%s", prefix, uid, ts, ext)
}
