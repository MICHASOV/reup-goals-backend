package security

import "testing"

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
