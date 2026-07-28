package security

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestSafeFilenameRemovesPath(t *testing.T) {
	if got := SafeFilename(`..\private\report.pdf`); got != "report.pdf" {
		t.Fatalf("expected report.pdf, got %q", got)
	}
}

func TestValidateBusinessDocument(t *testing.T) {
	if err := ValidateBusinessDocument("report.pdf", 100, 200); err != nil {
		t.Fatalf("expected valid document, got %v", err)
	}
	if err := ValidateBusinessDocument("payload.exe", 100, 200); err == nil {
		t.Fatal("expected executable upload to be rejected")
	}
	if err := ValidateBusinessDocument("report.pdf", 300, 200); err == nil {
		t.Fatal("expected oversized upload to be rejected")
	}
}

func TestInspectBusinessDocumentRejectsSpoofedExtension(t *testing.T) {
	_, _, err := InspectBusinessDocument("payload.pdf", 12, 100, strings.NewReader("not a pdf"))
	if err == nil || err.Error() != "file_content_mismatch" {
		t.Fatalf("expected content mismatch, got %v", err)
	}
}

func TestInspectBusinessDocumentReplaysSample(t *testing.T) {
	source := "%PDF-1.7\n1 0 obj\n<<>>\nendobj\nxref\n0 1\ntrailer\n<<>>\nstartxref\n0\n%%EOF\n"
	reader, contentType, err := InspectBusinessDocument("report.pdf", int64(len(source)), 100, strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != source || contentType != "application/pdf" {
		t.Fatalf("unexpected replay %q with %q", content, contentType)
	}
}

func TestInspectBusinessDocumentRejectsIncompletePDF(t *testing.T) {
	source := "%PDF-1.7\nnot a complete document"
	_, _, err := InspectBusinessDocument("report.pdf", int64(len(source)), 100, strings.NewReader(source))
	if err == nil || err.Error() != "file_content_mismatch" {
		t.Fatalf("expected incomplete PDF to be rejected, got %v", err)
	}
}

func TestInspectBusinessDocumentValidatesOfficeContainer(t *testing.T) {
	var document bytes.Buffer
	writer := zip.NewWriter(&document)
	contentTypes, err := writer.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contentTypes.Write([]byte("<Types/>")); err != nil {
		t.Fatal(err)
	}
	mainDocument, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mainDocument.Write([]byte("<document/>")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reader, contentType, err := InspectBusinessDocument(
		"report.docx",
		int64(document.Len()),
		int64(document.Len()+1),
		bytes.NewReader(document.Bytes()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("unexpected content type %q", contentType)
	}

	_, _, err = InspectBusinessDocument(
		"report.xlsx",
		int64(document.Len()),
		int64(document.Len()+1),
		bytes.NewReader(document.Bytes()),
	)
	if err == nil || err.Error() != "file_content_mismatch" {
		t.Fatalf("expected mismatched Office package to be rejected, got %v", err)
	}
}

func TestInspectBusinessDocumentRejectsBinaryText(t *testing.T) {
	_, _, err := InspectBusinessDocument("notes.txt", 4, 100, strings.NewReader("a\x00bc"))
	if err == nil {
		t.Fatal("expected binary text to be rejected")
	}
}

func TestInspectAudioValidatesMagic(t *testing.T) {
	wav := "RIFF\x04\x00\x00\x00WAVEdata"
	if _, _, err := InspectAudio("voice.wav", int64(len(wav)), 100, strings.NewReader(wav)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InspectAudio("voice.wav", 8, 100, strings.NewReader("not wav!")); err == nil {
		t.Fatal("expected spoofed audio to be rejected")
	}
}
