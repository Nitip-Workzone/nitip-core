package fileutil

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register png decoder
	"io"

	"golang.org/x/image/draw"
)

const (
	MaxImageDimension = 8000             // reject images larger than 8000x8000 to prevent decompression bomb OOM
	MaxImageBytes     = 10 * 1024 * 1024 // 10MB limit via LimitReader
)

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
