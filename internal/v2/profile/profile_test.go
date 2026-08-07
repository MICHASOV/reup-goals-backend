package profile

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/subscriptions"
	"reup-goals-backend/internal/v2/billing"
)

func TestBuildInvoicePDF(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	document, err := BuildInvoicePDF(Invoice{
		Number: "REUP-2026-000001", Amount: 2990, Currency: "RUB",
		Description: "Подписка REUP.goals, тариф Founder, доступ к системе стратегического управления компанией и AI-советнику", TaxLabel: "Без НДС",
		IssuedAt: now, DueAt: now.Add(5 * 24 * time.Hour),
		IssuedDate: "20.07.2026", DueDate: "25.07.2026", Timezone: "Europe/Moscow",
	}, SellerProfile{
		FullName: "ООО РЕАП", INN: "5262392668", KPP: "526201001",
		RegistrationNumber: "1235200026995", LegalAddress: "603000, Нижегородская область, город Нижний Новгород, улица Большая Покровская, дом 15, помещение 24",
		BankName: "АО Т-Банк", SettlementAccount: "40702810110001489655",
		CorrespondentAccount: "30101810145250000974", BIC: "044525974",
		TaxLabel: "Без НДС",
	}, BillingOrganization{
		FullName: "ООО Покупатель", INN: "7701234567", RegistrationNumber: "1027700123456",
		LegalAddress: "125009, город Москва, Тверская улица, дом 10, строение 3, этаж 5, помещение 18", AccountingEmail: "billing@example.com", ContactPerson: "Иван",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(document, []byte("%PDF-")) {
		t.Fatalf("expected a PDF header, got %q", document[:min(12, len(document))])
	}
	if !bytes.Contains(document, []byte("startxref")) || !bytes.HasSuffix(document, []byte("%%EOF\n")) {
		t.Fatal("expected a complete PDF file")
	}
	if !bytes.Contains(document, []byte("/Subtype /Image")) {
		t.Fatal("expected the REUP.goals logo to be embedded")
	}
	if outputPath := strings.TrimSpace(os.Getenv("INVOICE_TEST_OUTPUT")); outputPath != "" {
		if err := os.WriteFile(outputPath, document, 0o600); err != nil {
			t.Fatalf("write invoice fixture: %v", err)
		}
	}
}

func TestInvoicePresentationHelpers(t *testing.T) {
	invoice := Invoice{
		IssuedAt:   time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		IssuedDate: "28.07.2026",
	}
	if got := invoiceLongDate(invoice); got != "28 июля 2026 г." {
		t.Fatalf("unexpected long date: %q", got)
	}
	if got := formatMoney(29990); got != "29 990,00" {
		t.Fatalf("unexpected formatted amount: %q", got)
	}
	if got := russianMoneyWords(3490, "RUB"); got != "Три тысячи четыреста девяносто рублей 00 копеек." {
		t.Fatalf("unexpected amount in words: %q", got)
	}
	if got := russianMoneyWords(1201.02, "RUB"); got != "Одна тысяча двести один рубль 02 копейки." {
		t.Fatalf("unexpected amount in words with inflection: %q", got)
	}
}

func TestInvoiceFontPathRejectsPathsOutsideSystemFontDirectories(t *testing.T) {
	t.Setenv("INVOICE_FONT_PATH", "/etc/passwd")
	path, err := invoiceFontPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "/etc/passwd" {
		t.Fatal("expected configured path outside system font directories to be rejected")
	}
}

func TestAllowedInvoiceFontPathRejectsTraversal(t *testing.T) {
	if path, allowed := allowedInvoiceFontPath("/usr/share/fonts/../../../etc/passwd"); allowed {
		t.Fatalf("expected traversal path to be rejected, got %q", path)
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
		`{"plan_code":"team","billing_period":"annual","order_kind":"subscription","idempotency_key":"invoice-test-1"}`,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode invoice request: %v", err)
	}
	if request.PlanCode != "team" || request.BillingPeriod != "annual" || request.OrderKind != "subscription" || request.IdempotencyKey != "invoice-test-1" {
		t.Fatalf("unexpected invoice request: %+v", request)
	}
}

func TestCheckoutRequestJSONContract(t *testing.T) {
	var request CheckoutRequest
	decoder := json.NewDecoder(strings.NewReader(`{"plan_code":"team","billing_period":"quarterly"}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode checkout request: %v", err)
	}
	if request.PlanCode != "team" || request.BillingPeriod != "quarterly" {
		t.Fatalf("unexpected checkout request: %+v", request)
	}
}

func TestCheckoutProviderPrefersCloudPayments(t *testing.T) {
	cfg := &config.Config{
		BillingPaymentsEnabled: true,
		CloudPaymentsPublicID:  "pk_test_reup",
		TopPaymentsCheckoutURL: "https://legacy-payments.example/checkout",
	}
	handler := NewHandler(nil, cfg, nil, subscriptions.NewCloudPaymentsClient(cfg))
	if got := handler.checkoutProvider(); got != "cloudpayments" {
		t.Fatalf("expected CloudPayments, got %q", got)
	}
}

func TestCheckoutProviderFallsBackToRedirect(t *testing.T) {
	cfg := &config.Config{
		BillingPaymentsEnabled: true,
		TopPaymentsCheckoutURL: "https://legacy-payments.example/checkout",
	}
	handler := NewHandler(nil, cfg, nil, subscriptions.NewCloudPaymentsClient(cfg))
	if got := handler.checkoutProvider(); got != "toppayments" {
		t.Fatalf("expected redirect fallback, got %q", got)
	}
}

func TestInvoiceCreationIsDisabledInProduction(t *testing.T) {
	handler := NewHandler(nil, &config.Config{Environment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/profile/billing/invoices", nil)
	response := httptest.NewRecorder()

	handler.invoices(response, request, 1, Overview{}, nil)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, response.Code)
	}
	if !strings.Contains(response.Body.String(), "invoice_payments_disabled") {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestAIUsageResponseJSONContract(t *testing.T) {
	payload, err := json.Marshal(AIUsageResponse{
		PlanCode: "team", PlanName: "Team", ResetAmount: 2990, Currency: "RUB",
		CanManageSubscription: true,
		AIUsage: billing.QuotaSummary{
			WeeklyTokenLimit: 3_000_000, WeeklyTokensUsed: 1_260_000,
			PurchasedTokenBalance: 25_000, RemainingTokens: 1_765_000,
		},
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
		`"weekly_token_limit":3000000`,
		`"weekly_tokens_used":1260000`,
		`"purchased_token_balance":25000`,
		`"remaining_tokens":1765000`,
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
		InterfaceLanguage: "ru", Theme: "light", DateFormat: "DD.MM.YYYY", AILanguage: "ru",
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
