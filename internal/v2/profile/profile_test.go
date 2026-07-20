package profile

import (
	"bytes"
	"testing"
	"time"
)

func TestBuildInvoicePDF(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	document := BuildInvoicePDF(Invoice{
		Number: "REUP-2026-000001", Amount: 2990, Currency: "RUB",
		IssuedAt: now, DueAt: now.Add(5 * 24 * time.Hour),
	}, BillingOrganization{
		FullName: "ООО РЕАП", INN: "7701234567", RegistrationNumber: "1027700123456",
		LegalAddress: "Москва", AccountingEmail: "billing@example.com", ContactPerson: "Иван",
	})
	if !bytes.HasPrefix(document, []byte("%PDF-1.4")) {
		t.Fatalf("expected a PDF header, got %q", document[:min(12, len(document))])
	}
	if !bytes.Contains(document, []byte("startxref")) || !bytes.HasSuffix(document, []byte("%%EOF\n")) {
		t.Fatal("expected a complete PDF file")
	}
}

func TestValidOrganization(t *testing.T) {
	valid := BillingOrganization{
		FullName: "ООО РЕАП", INN: "7701234567", KPP: "770101001",
		RegistrationNumber: "1027700123456", LegalAddress: "г. Москва, ул. Тестовая, 1",
		AccountingEmail: "billing@example.com", ContactPerson: "Иван Иванов",
	}
	if !validOrganization(valid) {
		t.Fatal("expected organization to be valid")
	}
	valid.INN = "123"
	if validOrganization(valid) {
		t.Fatal("expected short INN to be rejected")
	}
}

func TestSubscriptionDisplayStatus(t *testing.T) {
	soon := time.Now().UTC().Add(48 * time.Hour)
	if got := subscriptionDisplayStatus("active", &soon, nil); got != "expires_soon" {
		t.Fatalf("expected expires_soon, got %q", got)
	}
	grace := time.Now().UTC().Add(48 * time.Hour)
	if got := subscriptionDisplayStatus("past_due", nil, &grace); got != "grace_period" {
		t.Fatalf("expected grace_period, got %q", got)
	}
}

func TestValidSettings(t *testing.T) {
	value := Settings{
		InterfaceLanguage: "ru", Theme: "dark", DateFormat: "DD.MM.YYYY", AILanguage: "ru",
	}
	if !validSettings(value) {
		t.Fatal("expected settings to be valid")
	}
	value.Theme = "purple"
	if validSettings(value) {
		t.Fatal("expected unsupported theme to be rejected")
	}
}
