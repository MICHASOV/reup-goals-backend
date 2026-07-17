package security

import (
	"fmt"
	"path/filepath"
	"strings"
)

var businessDocumentExtensions = map[string]struct{}{
	".csv": {}, ".doc": {}, ".docx": {}, ".html": {}, ".json": {}, ".md": {},
	".pdf": {}, ".ppt": {}, ".pptx": {}, ".rtf": {}, ".txt": {}, ".xls": {}, ".xlsx": {},
}

var audioExtensions = map[string]struct{}{
	".m4a": {}, ".mp3": {}, ".mp4": {}, ".mpeg": {}, ".mpga": {}, ".ogg": {}, ".wav": {}, ".webm": {},
}

func SafeFilename(value string) string {
	name := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		return "upload"
	}
	return name
}

func ValidateBusinessDocument(filename string, sizeBytes int64, maxBytes int64) error {
	return validateUpload(filename, sizeBytes, maxBytes, businessDocumentExtensions)
}

func ValidateAudio(filename string, sizeBytes int64, maxBytes int64) error {
	return validateUpload(filename, sizeBytes, maxBytes, audioExtensions)
}

func validateUpload(filename string, sizeBytes int64, maxBytes int64, allowed map[string]struct{}) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("empty_file")
	}
	if maxBytes > 0 && sizeBytes > maxBytes {
		return fmt.Errorf("file_too_large")
	}
	extension := strings.ToLower(filepath.Ext(SafeFilename(filename)))
	if _, ok := allowed[extension]; !ok {
		return fmt.Errorf("unsupported_file_type")
	}
	return nil
}
