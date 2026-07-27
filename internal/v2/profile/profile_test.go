package profile

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildInvoicePDF(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	document := BuildInvoicePDF(Invoice{
		Number: "REUP-2026-000001", Amount: 2990, Currency: "RUB",
		Description: "Подписка REUP.goals, тариф Founder", TaxLabel: "Без НДС",
		IssuedAt: now, DueAt: now.Add(5 * 24 * time.Hour),
	}, SellerProfile{
		FullName: "ООО РЕАП", INN: "5262392668", KPP: "526201001",
		RegistrationNumber: "1235200026995", LegalAddress: "Нижний Новгород",
		BankName: "АО Т-Банк", SettlementAccount: "40702810110001489655",
		CorrespondentAccount: "30101810145250000974", BIC: "044525974",
		TaxLabel: "Без НДС",
	}, BillingOrganization{
		FullName: "ООО Покупатель", INN: "7701234567", RegistrationNumber: "1027700123456",
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

func TestInvoiceRequestJSONContract(t *testing.T) {
	var request InvoiceRequest
	decoder := json.NewDecoder(strings.NewReader(
		`{"plan_code":"team","billing_period":"annual","order_kind":"subscription"}`,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode invoice request: %v", err)
	}
	if request.PlanCode != "team" || request.BillingPeriod != "annual" || request.OrderKind != "subscription" {
		t.Fatalf("unexpected invoice request: %+v", request)
	}
}

func TestAIUsageResponseJSONContract(t *testing.T) {
	payload, err := json.Marshal(AIUsageResponse{
		PlanCode: "team", PlanName: "Team", ResetAmount: 2990, Currency: "RUB",
		CanManageSubscription: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, field := range []string{
		`"plan_code":"team"`,
		`"reset_amount":2990`,
		`"can_manage_subscription":true`,
		`"ai_usage"`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("expected %s in %s", field, body)
		}
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

func TestDedupePositiveIDs(t *testing.T) {
	got := dedupePositiveIDs([]int{4, 0, 2, 4, -1, 2, 9})
	want := []int{4, 2, 9}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestInvitationCooldownRemaining(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

	if got := invitationCooldownRemaining(now.Add(-30*time.Second), now); got != 90*time.Second {
		t.Fatalf("expected 90 seconds, got %s", got)
	}
	if got := invitationCooldownRemaining(now.Add(-invitationResendCooldown), now); got != 0 {
		t.Fatalf("expected expired cooldown, got %s", got)
	}
	if got := invitationCooldownRemaining(now.Add(time.Second), now); got != invitationResendCooldown {
		t.Fatalf("expected clock skew to be capped at %s, got %s", invitationResendCooldown, got)
	}
}

func TestInvitationResendTooSoonRetryAfterRoundsUp(t *testing.T) {
	value := &InvitationResendTooSoonError{RetryAfter: 1500 * time.Millisecond}
	if got := value.RetryAfterSeconds(); got != 2 {
		t.Fatalf("expected two seconds, got %d", got)
	}
}
