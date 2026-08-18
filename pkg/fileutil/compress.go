package fileutil

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register png decoder
	"io"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"
)

const (
	MaxImageDimension = 8000             // reject images larger than 8000x8000 to prevent decompression bomb OOM
	MaxImageBytes     = 10 * 1024 * 1024 // 10MB limit via LimitReader
	DefaultMaxUpload  = 1 * 1024 * 1024  // 1MB final target enforced for all uploads to save COS egress + CDN cache
)

// Bounded concurrency guard to prevent VPS OOM on Lighthouse 2C4G (app limit 512MB)
// Each compress RGBA 4000x3000 ~48MB + rescale buffer ~ similar peak ~100MB, 3 concurrent = ~300MB safe
// Max 3 concurrent compress, queue timeout 30s to prevent rush condition
var (
	compressSem        = make(chan struct{}, 3) // 3 concurrent max
	compressActive     int32
	compressQueueDepth int32
)

// AcquireCompressSlot tries to acquire compress slot with timeout to prevent rush OOM
func AcquireCompressSlot(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case compressSem <- struct{}{}:
			atomic.AddInt32(&compressActive, 1)
			return nil
		case <-ctx.Done():
			return fmt.Errorf("terlalu banyak proses kompresi bersamaan, coba lagi")
		default:
			// Channel full, need to wait
			atomic.AddInt32(&compressQueueDepth, 1)
			select {
			case compressSem <- struct{}{}:
				atomic.AddInt32(&compressQueueDepth, -1)
				atomic.AddInt32(&compressActive, 1)
				return nil
			case <-ctx.Done():
				atomic.AddInt32(&compressQueueDepth, -1)
				return fmt.Errorf("terlalu banyak proses kompresi bersamaan, coba lagi")
			case <-time.After(time.Until(deadline)):
				atomic.AddInt32(&compressQueueDepth, -1)
				return fmt.Errorf("antrian kompresi penuh, coba lagi dalam beberapa detik")
			}
		}
	}
}

func ReleaseCompressSlot() {
	select {
	case <-compressSem:
		atomic.AddInt32(&compressActive, -1)
	default:
	}
}

// CompressAndResizeImage decodes an image from reader, resizes it if it exceeds maxDimension (width or height),
// and encodes it back to JPEG format with the specified quality.
// P0 #4 fix: prevents decompression bomb OOM 512M — checks Config before Decode + LimitReader.
func CompressAndResizeImage(r io.Reader, maxDimension int, quality int) (io.Reader, error) {
	// 1. LimitReader to 10MB to prevent unbounded read
	limited := io.LimitReader(r, MaxImageBytes+1)

	// 2. Peek config first without full decode to check dimensions
	// Need to buffer because Config consumes reader — use Tee + bytes copy
	// For simplicity: read into memory limited buffer then Config
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, limited); err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}
	if buf.Len() > MaxImageBytes {
		return nil, fmt.Errorf("image too large (>10MB)")
	}

	// Config check before full decode
	cfg, _, err := image.DecodeConfig(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image config: %w", err)
	}
	if cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension {
		return nil, fmt.Errorf("image dimensions too large (%dx%d > %d)", cfg.Width, cfg.Height, MaxImageDimension)
	}
	// Absolute max 8000 check regardless of requested maxDimension
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}

	// 3. Full decode now safe
	img, _, err2 := image.Decode(bytes.NewReader(buf.Bytes()))
	if err2 != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err2)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	newImg := img

	// Resize if necessary
	if width > maxDimension || height > maxDimension {
		var newW, newH int
		if width > height {
			newW = maxDimension
			newH = (height * maxDimension) / width
		} else {
			newH = maxDimension
			newW = (width * maxDimension) / height
		}

		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		// Use BiLinear scaling for good balance of quality and speed (ideal for text readability on receipts)
		draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		newImg = dst
	}

	// Encode to JPEG
	outBuf := new(bytes.Buffer)
	if err := jpeg.Encode(outBuf, newImg, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("failed to encode image as jpeg: %w", err)
	}

	return outBuf, nil
}

// CompressToLimit compresses image to targetMax (default 1MB) with concurrent bounded queue
// Loop quality/dimension steps: [maxDimStart/75, 1000/70, 1000/60, 800/60, 800/50, 600/50, 600/40, 400/40]
// Ensures final size <= targetMax or error, prevents VPS OOM via semaphore
func CompressToLimit(r io.Reader, maxDimStart int, targetMax int64) (io.Reader, int64, error) {
	return CompressToLimitWithContext(context.Background(), r, maxDimStart, targetMax)
}

func CompressToLimitWithContext(ctx context.Context, r io.Reader, maxDimStart int, targetMax int64) (io.Reader, int64, error) {
	if targetMax <= 0 {
		targetMax = DefaultMaxUpload
	}
	if maxDimStart <= 0 {
		maxDimStart = 1200
	}

	// Acquire bounded slot to prevent concurrent OOM
	if err := AcquireCompressSlot(ctx); err != nil {
		return nil, 0, err
	}
	defer ReleaseCompressSlot()

	// Buffer original first (with limit) to allow re-reads across loop steps
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, io.LimitReader(r, MaxImageBytes+1)); err != nil {
		return nil, 0, fmt.Errorf("failed to read image for compress limit: %w", err)
	}
	if int64(buf.Len()) > MaxImageBytes {
		return nil, 0, fmt.Errorf("image too large pre-compress (>10MB)")
	}
	originalBytes := buf.Bytes()

	steps := []struct {
		dim     int
		quality int
	}{
		{maxDimStart, 75},
		{1000, 70},
		{1000, 60},
		{800, 60},
		{800, 50},
		{600, 50},
		{600, 40},
		{400, 40},
	}

	var lastReader io.Reader
	var lastSize int64
	var lastErr error

	for _, step := range steps {
		// Compress
		comp, err := CompressAndResizeImage(bytes.NewReader(originalBytes), step.dim, step.quality)
		if err != nil {
			lastErr = err
			continue
		}
		// Measure size
		if b, ok := comp.(*bytes.Buffer); ok {
			size := int64(b.Len())
			lastReader = b
			lastSize = size
			if size <= targetMax {
				return b, size, nil
			}
			// Continue loop to smaller dimension/quality
		} else {
			// Convert to buffer to measure
			tmpBuf := new(bytes.Buffer)
			n, err := io.Copy(tmpBuf, comp)
			if err != nil {
				lastErr = err
				continue
			}
			lastReader = tmpBuf
			lastSize = n
			if n <= targetMax {
				return tmpBuf, n, nil
			}
		}
	}

	// After all steps still > target
	if lastReader != nil {
		return nil, lastSize, fmt.Errorf("compressed image still >%dKB even after max compression (size %dKB), coba foto lebih kecil", targetMax/1024, lastSize/1024)
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("failed to compress image under %dKB", targetMax/1024)
}

// EnsureUnderLimit ensures reader under target, otherwise compress
func EnsureUnderLimit(r io.Reader, targetMax int64) (io.Reader, int64, error) {
	if targetMax <= 0 {
		targetMax = DefaultMaxUpload
	}
	// Quick check if already small? Need buffer to check
	buf := new(bytes.Buffer)
	n, err := io.Copy(buf, io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if n <= targetMax {
		// Already under limit, but still ensure JPEG? Return as-is
		return bytes.NewReader(buf.Bytes()), n, nil
	}
	// Need compress
	return CompressToLimit(bytes.NewReader(buf.Bytes()), 1200, targetMax)
}

// GetCompressStats returns active and queued counts for monitoring
func GetCompressStats() (active int32, queued int32) {
	return atomic.LoadInt32(&compressActive), atomic.LoadInt32(&compressQueueDepth)
}
