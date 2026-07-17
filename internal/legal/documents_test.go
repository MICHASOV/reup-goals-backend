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
