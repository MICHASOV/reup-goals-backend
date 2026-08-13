package legal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

const (
	DocumentPublicOffer        = "public_offer"
	DocumentPrivacyNotice      = "privacy_notice"
	DocumentPersonalData       = "personal_data_consent"
	DocumentMarketing          = "marketing_communications"
	CurrentDocumentVersion     = "2026-07-18"
	registrationEvidenceSource = "web_registration"
)

type Document struct {
	Type     string `json:"type"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	Required bool   `json:"required"`
}

type AcceptanceInput struct {
	DocumentType string `json:"document_type"`
	Version      string `json:"version"`
	Accepted     bool   `json:"accepted"`
}

type AcceptanceRecord struct {
	DocumentType string
	Version      string
	Accepted     bool
	LegalBasis   string
}

var ErrAcceptanceRequired = errors.New("legal_acceptance_required")
var ErrVersionOutdated = errors.New("legal_document_version_outdated")
var ErrInvalidAcceptance = errors.New("invalid_legal_acceptance")

func CurrentDocuments() []Document {
	documents := []Document{
		{Type: DocumentPublicOffer, Version: CurrentDocumentVersion, URL: "/public-offer", Required: true},
		{Type: DocumentPrivacyNotice, Version: CurrentDocumentVersion, URL: "/privacy-policy", Required: true},
		{Type: DocumentPersonalData, Version: CurrentDocumentVersion, URL: "/personal-data-consent", Required: true},
		{Type: DocumentMarketing, Version: CurrentDocumentVersion, URL: "/marketing-consent", Required: false},
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Type < documents[j].Type })
	return documents
}

func ValidateRegistrationAcceptances(inputs []AcceptanceInput) ([]AcceptanceRecord, error) {
	provided := make(map[string]AcceptanceInput, len(inputs))
	for _, input := range inputs {
		typeName := strings.TrimSpace(input.DocumentType)
		if !knownDocument(typeName) {
			return nil, ErrInvalidAcceptance
		}
		if _, exists := provided[typeName]; exists {
			return nil, ErrInvalidAcceptance
		}
		if strings.TrimSpace(input.Version) != CurrentDocumentVersion {
			return nil, ErrVersionOutdated
		}
		input.DocumentType = typeName
		input.Version = CurrentDocumentVersion
		provided[typeName] = input
	}

	for _, required := range []string{DocumentPublicOffer, DocumentPrivacyNotice, DocumentPersonalData} {
		input, ok := provided[required]
		if !ok || !input.Accepted {
			return nil, ErrAcceptanceRequired
		}
	}
	marketing, ok := provided[DocumentMarketing]
	if !ok {
		marketing = AcceptanceInput{DocumentType: DocumentMarketing, Version: CurrentDocumentVersion, Accepted: false}
	}
	provided[DocumentMarketing] = marketing

	legalBasis := map[string]string{
		DocumentPublicOffer:   "contract",
		DocumentPrivacyNotice: "transparency_acknowledgement",
		DocumentPersonalData:  "consent",
		DocumentMarketing:     "consent",
	}
	result := make([]AcceptanceRecord, 0, len(provided))
	for _, document := range CurrentDocuments() {
		input := provided[document.Type]
		result = append(result, AcceptanceRecord{
			DocumentType: document.Type,
			Version:      input.Version,
			Accepted:     input.Accepted,
			LegalBasis:   legalBasis[document.Type],
		})
	}
	return result, nil
}

func StoreAcceptances(ctx context.Context, tx *sql.Tx, userID int, subjectKey string, records []AcceptanceRecord, requestID string) error {
	for _, record := range records {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO legal_acceptances (
				user_id, subject_key, document_type, document_version, accepted,
				legal_basis, source, request_id, withdrawn_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
				CASE WHEN $3=$9 AND $5=FALSE THEN NOW() ELSE NULL END)
		`, userID, subjectKey, record.DocumentType, record.Version, record.Accepted,
			record.LegalBasis, registrationEvidenceSource, SanitizeRequestID(requestID), DocumentMarketing)
		if err != nil {
			return err
		}
	}
	return nil
}

func NewSubjectKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func knownDocument(value string) bool {
	switch value {
	case DocumentPublicOffer, DocumentPrivacyNotice, DocumentPersonalData, DocumentMarketing:
		return true
	default:
		return false
	}
}

func SanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return ""
	}
	for _, symbol := range value {
		if (symbol >= 'a' && symbol <= 'z') || (symbol >= 'A' && symbol <= 'Z') ||
			(symbol >= '0' && symbol <= '9') || symbol == '-' || symbol == '_' {
			continue
		}
		return ""
	}
	return value
}
