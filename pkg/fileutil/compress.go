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

// CompressAndResizeImage decodes an image from reader, resizes it if it exceeds maxDimension (width or height),
// and encodes it back to JPEG format with the specified quality.
func CompressAndResizeImage(r io.Reader, maxDimension int, quality int) (io.Reader, error) {
	// Decode image
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
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
	buf := new(bytes.Buffer)
	err = jpeg.Encode(buf, newImg, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, fmt.Errorf("failed to encode image as jpeg: %w", err)
	}

	return buf, nil
}
