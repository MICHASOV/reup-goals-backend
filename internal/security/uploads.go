package security

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
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

func InspectBusinessDocument(filename string, sizeBytes int64, maxBytes int64, source io.Reader) (io.Reader, string, error) {
	if err := ValidateBusinessDocument(filename, sizeBytes, maxBytes); err != nil {
		return nil, "", err
	}
	content, err := readUpload(source, maxBytes)
	if err != nil {
		return nil, "", err
	}
	extension := strings.ToLower(filepath.Ext(SafeFilename(filename)))
	if !validBusinessDocumentSignature(extension, content) {
		return nil, "", fmt.Errorf("file_content_mismatch")
	}
	return bytes.NewReader(content), businessDocumentContentType(extension, content), nil
}

func InspectAudio(filename string, sizeBytes int64, maxBytes int64, source io.Reader) (io.Reader, string, error) {
	if err := ValidateAudio(filename, sizeBytes, maxBytes); err != nil {
		return nil, "", err
	}
	content, err := readUpload(source, maxBytes)
	if err != nil {
		return nil, "", err
	}
	extension := strings.ToLower(filepath.Ext(SafeFilename(filename)))
	if !validAudioSignature(extension, content) {
		return nil, "", fmt.Errorf("file_content_mismatch")
	}
	return bytes.NewReader(content), audioContentType(extension, content), nil
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

func readUpload(source io.Reader, maxBytes int64) ([]byte, error) {
	if source == nil {
		return nil, fmt.Errorf("empty_file")
	}
	limit := maxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	content, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return nil, fmt.Errorf("file_read_failed")
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("empty_file")
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("file_too_large")
	}
	return content, nil
}

func validBusinessDocumentSignature(extension string, content []byte) bool {
	switch extension {
	case ".pdf":
		tail := content
		if len(tail) > 2048 {
			tail = tail[len(tail)-2048:]
		}
		return bytes.HasPrefix(content, []byte("%PDF-")) &&
			bytes.Contains(content, []byte("startxref")) &&
			bytes.Contains(tail, []byte("%%EOF"))
	case ".docx", ".xlsx", ".pptx":
		return validOfficePackage(extension, content)
	case ".doc", ".xls", ".ppt":
		return bytes.HasPrefix(content, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	case ".rtf":
		return bytes.HasPrefix(bytes.TrimSpace(content), []byte(`{\rtf`))
	case ".json":
		return looksLikeText(content) && json.Valid(content)
	case ".csv", ".html", ".md", ".txt":
		return looksLikeText(content)
	default:
		return false
	}
}

func validAudioSignature(extension string, sample []byte) bool {
	switch extension {
	case ".wav":
		return len(sample) >= 12 && bytes.Equal(sample[:4], []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WAVE"))
	case ".ogg":
		return bytes.HasPrefix(sample, []byte("OggS"))
	case ".webm":
		return bytes.HasPrefix(sample, []byte{0x1a, 0x45, 0xdf, 0xa3})
	case ".m4a", ".mp4":
		return len(sample) >= 12 && bytes.Equal(sample[4:8], []byte("ftyp"))
	case ".mp3", ".mpeg", ".mpga":
		return bytes.HasPrefix(sample, []byte("ID3")) ||
			(len(sample) >= 2 && sample[0] == 0xff && sample[1]&0xe0 == 0xe0)
	default:
		return false
	}
}

func validOfficePackage(extension string, content []byte) bool {
	if !hasZIPSignature(content) {
		return false
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > 2048 {
		return false
	}
	requiredPrefix := map[string]string{".docx": "word/", ".xlsx": "xl/", ".pptx": "ppt/"}[extension]
	hasContentTypes := false
	hasRequiredPart := false
	var expandedBytes uint64
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		clean := filepath.ToSlash(filepath.Clean(name))
		if name == "" || strings.HasPrefix(name, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return false
		}
		if name == "[Content_Types].xml" {
			hasContentTypes = true
		}
		if strings.HasPrefix(name, requiredPrefix) {
			hasRequiredPart = true
		}
		expandedBytes += file.UncompressedSize64
		if expandedBytes > 256<<20 {
			return false
		}
		if file.CompressedSize64 > 0 && file.UncompressedSize64/file.CompressedSize64 > 200 {
			return false
		}
	}
	return hasContentTypes && hasRequiredPart
}

func hasZIPSignature(content []byte) bool {
	return bytes.HasPrefix(content, []byte{'P', 'K', 0x03, 0x04}) ||
		bytes.HasPrefix(content, []byte{'P', 'K', 0x05, 0x06}) ||
		bytes.HasPrefix(content, []byte{'P', 'K', 0x07, 0x08})
}

func looksLikeText(content []byte) bool {
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return false
	}
	sample := content
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := http.DetectContentType(sample)
	return strings.HasPrefix(detected, "text/") ||
		detected == "application/json" ||
		detected == "application/xml"
}

func businessDocumentContentType(extension string, content []byte) string {
	contentTypes := map[string]string{
		".csv":  "text/csv",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".html": "text/html",
		".json": "application/json",
		".md":   "text/markdown",
		".pdf":  "application/pdf",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".rtf":  "application/rtf",
		".txt":  "text/plain",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	if value := contentTypes[extension]; value != "" {
		return value
	}
	return http.DetectContentType(content[:min(len(content), 512)])
}

func audioContentType(extension string, content []byte) string {
	contentTypes := map[string]string{
		".m4a":  "audio/mp4",
		".mp3":  "audio/mpeg",
		".mp4":  "audio/mp4",
		".mpeg": "audio/mpeg",
		".mpga": "audio/mpeg",
		".ogg":  "audio/ogg",
		".wav":  "audio/wav",
		".webm": "audio/webm",
	}
	if value := contentTypes[extension]; value != "" {
		return value
	}
	return http.DetectContentType(content[:min(len(content), 512)])
}
