package fileutil

import (
	"mime/multipart"
	"net/http"
)

// IsImage detects if the uploaded file is a valid image (JPEG, PNG, GIF)
// by reading the first 512 bytes (magic bytes).
func IsImage(file *multipart.FileHeader) bool {
	f, err := file.Open()
	if err != nil {
		return false
	}
	defer f.Close()

	// Only read first 512 bytes to detect content type
	buffer := make([]byte, 512)
	_, err = f.Read(buffer)
	if err != nil {
		return false
	}

	contentType := http.DetectContentType(buffer)
	return contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/gif"
}
