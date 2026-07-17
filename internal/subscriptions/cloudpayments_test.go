package subscriptions

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyWebhookFailsClosedWithoutSecret(t *testing.T) {
	client := &CloudPaymentsClient{}
	if client.VerifyWebhook([]byte("payload"), "anything") {
		t.Fatal("webhook verification must fail when the secret is missing")
	}
}

func TestVerifyWebhookValidatesHMAC(t *testing.T) {
	const secret = "test-secret"
	payload := []byte("payload")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	client := &CloudPaymentsClient{secret: secret}

	if !client.VerifyWebhook(payload, signature) {
		t.Fatal("expected valid webhook signature")
	}
	if client.VerifyWebhook([]byte("changed"), signature) {
		t.Fatal("expected changed payload to fail verification")
	}
}

func TestPaymentEventPayloadAllowlist(t *testing.T) {
	for _, key := range []string{"Status", "ReasonCode", "DateTime"} {
		if !safePaymentEventField(key) {
			t.Fatalf("expected %s to be retained", key)
		}
	}
	for _, key := range []string{"Token", "Email", "CardFirstSix", "IpAddress", "Data"} {
		if safePaymentEventField(key) {
			t.Fatalf("expected sensitive field %s to be removed", key)
		}
	}
}
