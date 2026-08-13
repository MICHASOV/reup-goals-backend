package legal

import (
	"errors"
	"testing"
)

func TestValidateRegistrationAcceptances(t *testing.T) {
	records, err := ValidateRegistrationAcceptances([]AcceptanceInput{
		{DocumentType: DocumentPublicOffer, Version: CurrentDocumentVersion, Accepted: true},
		{DocumentType: DocumentPrivacyNotice, Version: CurrentDocumentVersion, Accepted: true},
		{DocumentType: DocumentPersonalData, Version: CurrentDocumentVersion, Accepted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[0].DocumentType != DocumentMarketing || records[0].Accepted {
		t.Fatalf("expected explicit marketing refusal, got %+v", records)
	}
}

func TestValidateRegistrationAcceptancesRejectsMissingRequired(t *testing.T) {
	_, err := ValidateRegistrationAcceptances([]AcceptanceInput{
		{DocumentType: DocumentPublicOffer, Version: CurrentDocumentVersion, Accepted: true},
	})
	if !errors.Is(err, ErrAcceptanceRequired) {
		t.Fatalf("expected required acceptance error, got %v", err)
	}
}

func TestValidateRegistrationAcceptancesRejectsStaleVersion(t *testing.T) {
	_, err := ValidateRegistrationAcceptances([]AcceptanceInput{
		{DocumentType: DocumentPublicOffer, Version: "old", Accepted: true},
	})
	if !errors.Is(err, ErrVersionOutdated) {
		t.Fatalf("expected stale version error, got %v", err)
	}
}

func TestSanitizeRequestID(t *testing.T) {
	if got := SanitizeRequestID(" web-request_123 "); got != "web-request_123" {
		t.Fatalf("expected sanitized request ID, got %q", got)
	}
	if got := SanitizeRequestID("invalid/request"); got != "" {
		t.Fatalf("expected invalid request ID to be rejected, got %q", got)
	}
}
