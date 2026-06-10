package subscriptions

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"reup-goals-backend/internal/config"
)

type CloudPaymentsClient struct {
	publicID    string
	secret      string
	baseURL     string
	planName    string
	amount      float64
	firstAmount float64
	currency    string
	trialDays   int
	client      *http.Client
}

func NewCloudPaymentsClient(cfg *config.Config) *CloudPaymentsClient {
	return &CloudPaymentsClient{
		publicID:    cfg.CloudPaymentsPublicID,
		secret:      cfg.CloudPaymentsAPISecret,
		baseURL:     strings.TrimRight(cfg.CloudPaymentsBaseURL, "/"),
		planName:    cfg.CloudPaymentsPlanName,
		amount:      cfg.CloudPaymentsAmount,
		firstAmount: cfg.CloudPaymentsFirstPaymentAmount,
		currency:    cfg.CloudPaymentsCurrency,
		trialDays:   cfg.CloudPaymentsTrialDays,
		client:      http.DefaultClient,
	}
}

func (c *CloudPaymentsClient) PublicID() string {
	return c.publicID
}

func (c *CloudPaymentsClient) PlanName() string {
	return c.planName
}

func (c *CloudPaymentsClient) Amount() float64 {
	return c.amount
}

func (c *CloudPaymentsClient) FirstPaymentAmount() float64 {
	return c.firstAmount
}

func (c *CloudPaymentsClient) Currency() string {
	return c.currency
}

func (c *CloudPaymentsClient) TrialDays() int {
	return c.trialDays
}

func (c *CloudPaymentsClient) VerifyWebhook(rawBody []byte, hmacHeader string) bool {
	if c.secret == "" {
		return true
	}
	if hmacHeader == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(c.secret))
	_, _ = mac.Write(rawBody)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(hmacHeader))
}

func (c *CloudPaymentsClient) CancelSubscription(subscriptionID string) error {
	if c.publicID == "" || c.secret == "" {
		return errors.New("cloudpayments_not_configured")
	}

	body, err := json.Marshal(map[string]string{"Id": subscriptionID})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/subscriptions/cancel", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.publicID, c.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudpayments_cancel_http_%d", resp.StatusCode)
	}

	var parsed struct {
		Success bool   `json:"Success"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	if !parsed.Success {
		if parsed.Message == "" {
			parsed.Message = "cloudpayments_cancel_failed"
		}
		return errors.New(parsed.Message)
	}

	return nil
}
