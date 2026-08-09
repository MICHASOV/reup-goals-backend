package subscriptions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"reup-goals-backend/internal/auth"
	v2billing "reup-goals-backend/internal/v2/billing"
)

const (
	statusInactive = "inactive"
	statusTrial    = "trial_active"
	statusActive   = "active"
	statusPastDue  = "past_due"
	statusCanceled = "cancelled"
	statusExpired  = "expired"
)

type Handler struct {
	dbx     *sql.DB
	cp      *CloudPaymentsClient
	billing *v2billing.Service
}

func NewHandler(dbx *sql.DB, cp *CloudPaymentsClient, billing ...*v2billing.Service) *Handler {
	result := &Handler{dbx: dbx, cp: cp}
	if len(billing) > 0 {
		result.billing = billing[0]
	}
	return result
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	status, err := h.statusForUser(uid)
	if err != nil {
		http.Error(w, "subscription_status_failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, status)
}

func (h *Handler) CheckoutConfig(w http.ResponseWriter, r *http.Request) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var email string
	if err := h.dbx.QueryRow(`SELECT email FROM users WHERE id=$1`, uid).Scan(&email); err != nil {
		http.Error(w, "user_not_found", http.StatusNotFound)
		return
	}

	if h.cp.PublicID() == "" {
		http.Error(w, "cloudpayments_not_configured", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, h.cp.TrialDays())
	accountID := accountIDForUser(uid)

	_, err := h.dbx.Exec(`
		INSERT INTO subscriptions (
			user_id, status, plan_name, amount, currency, trial_started_at, trial_ends_at,
			next_payment_at, current_period_start, current_period_end
		)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6, $6, NOW(), $6)
		ON CONFLICT (user_id) DO UPDATE SET
			plan_name=EXCLUDED.plan_name,
			amount=EXCLUDED.amount,
			currency=EXCLUDED.currency,
			updated_at=NOW()
	`, uid, statusInactive, h.cp.PlanName(), h.cp.Amount(), h.cp.Currency(), startDate)
	if err != nil {
		http.Error(w, "subscription_prepare_failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"public_id":            h.cp.PublicID(),
		"description":          h.cp.PlanName(),
		"first_payment_amount": h.cp.FirstPaymentAmount(),
		"amount":               h.cp.Amount(),
		"currency":             h.cp.Currency(),
		"account_id":           accountID,
		"email":                email,
		"trial_days":           h.cp.TrialDays(),
		"start_date":           startDate.Format(time.RFC3339),
		"recurrent": map[string]any{
			"period":     1,
			"interval":   "Month",
			"amount":     h.cp.Amount(),
			"start_date": startDate.Format(time.RFC3339),
		},
	})
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var cpSubscriptionID sql.NullString
	var currentPeriodEnd sql.NullTime
	err := h.dbx.QueryRow(`
		SELECT cloudpayments_subscription_id, current_period_end
		FROM subscriptions
		WHERE user_id=$1
	`, uid).Scan(&cpSubscriptionID, &currentPeriodEnd)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "subscription_not_found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "subscription_lookup_failed", http.StatusInternalServerError)
		return
	}

	if cpSubscriptionID.Valid && cpSubscriptionID.String != "" {
		if err := h.cp.CancelSubscription(cpSubscriptionID.String); err != nil {
			http.Error(w, "cloudpayments_cancel_failed", http.StatusBadGateway)
			return
		}
	}

	_, err = h.dbx.Exec(`
		UPDATE subscriptions
		SET status=$1, cancelled_at=NOW(), updated_at=NOW()
		WHERE user_id=$2
	`, statusCanceled, uid)
	if err != nil {
		http.Error(w, "subscription_cancel_failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"ok":                 true,
		"current_period_end": nullableTime(currentPeriodEnd),
	})
}

func (h *Handler) CloudPaymentsWebhook(eventType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawBody := readRawBody(r)
		hmacHeader := r.Header.Get("Content-HMAC")
		if hmacHeader == "" {
			hmacHeader = r.Header.Get("X-Content-HMAC")
		}
		if !h.cp.VerifyWebhook(rawBody, hmacHeader) {
			writeWebhookCode(w, 13)
			return
		}

		form, err := url.ParseQuery(string(rawBody))
		if err != nil {
			writeWebhookCode(w, 13)
			return
		}

		if eventType == "check" {
			if !h.validateCheck(form) {
				writeWebhookCode(w, 13)
				return
			}
			writeWebhookCode(w, 0)
			return
		}

		if err := h.applyWebhook(eventType, form); err != nil {
			writeWebhookCode(w, 13)
			return
		}

		writeWebhookCode(w, 0)
	}
}

func (h *Handler) validateCheck(form url.Values) bool {
	accountID := formValue(form, "AccountId")
	uid, ok := userIDFromAccountID(accountID)
	if !ok {
		return false
	}

	var exists bool
	if err := h.dbx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, uid).Scan(&exists); err != nil {
		return false
	}
	if !exists {
		return false
	}
	if orderID, ok := cloudPaymentsOrderID(form); ok {
		if h.billing == nil {
			return false
		}
		valid, err := h.billing.ValidateCloudPaymentOrder(
			context.Background(), orderID, uid,
			parseAmount(formValue(form, "Amount")), formValue(form, "Currency"),
		)
		return err == nil && valid
	}

	selection, err := h.checkoutSelection(uid)
	if err != nil {
		return false
	}
	currency := formValue(form, "Currency")
	if currency != "" && currency != selection.currency {
		return false
	}
	amount := parseAmount(formValue(form, "Amount"))
	if amount != 0 && amount != selection.amount {
		return false
	}

	return true
}

func (h *Handler) applyWebhook(eventType string, form url.Values) error {
	accountID := formValue(form, "AccountId")
	uid, ok := userIDFromAccountID(accountID)
	if !ok {
		return errors.New("invalid_account_id")
	}
	if orderID, hasOrder := cloudPaymentsOrderID(form); hasOrder {
		if h.billing == nil {
			return errors.New("billing_service_unavailable")
		}
		amount := parseAmount(formValue(form, "Amount"))
		currency := formValue(form, "Currency")
		transactionID := formValue(form, "TransactionId")
		// Ordered widget payments expose the recurrent subscription explicitly.
		// `Id` belongs to other CloudPayments notifications and must not be used
		// when deciding which existing subscription to replace.
		cpSubscriptionID := formValue(form, "SubscriptionId")
		if eventType == "pay" {
			confirmation, err := h.billing.ConfirmCloudPaymentOrder(
				context.Background(), orderID, uid, transactionID, cpSubscriptionID,
				formValue(form, "Token"), amount, currency,
			)
			if err != nil {
				return err
			}
			if confirmation.SubscriptionIDToCancel != "" {
				if err := h.cp.CancelSubscription(confirmation.SubscriptionIDToCancel); err != nil {
					return err
				}
				if err := h.billing.MarkCloudPaymentSubscriptionReplaced(
					context.Background(), orderID, confirmation.SubscriptionIDToCancel,
				); err != nil {
					return err
				}
			}
		}
		return h.storeEvent(eventType, uid, 0, transactionID, cpSubscriptionID, accountID, amount, currency, form)
	}

	selection, err := h.checkoutSelection(uid)
	if err != nil {
		return err
	}
	amount := parseAmount(formValue(form, "Amount"))
	currency := formValue(form, "Currency")
	if amount != 0 && amount != selection.amount {
		return errors.New("payment_amount_mismatch")
	}
	if currency != "" && currency != selection.currency {
		return errors.New("payment_currency_mismatch")
	}
	transactionID := formValue(form, "TransactionId")
	cpSubscriptionID := firstNonEmpty(
		formValue(form, "SubscriptionId"),
		formValue(form, "Id"),
	)
	if eventType != "pay" && cpSubscriptionID != "" {
		var activeSubscriptionID sql.NullString
		queryErr := h.dbx.QueryRow(`
			SELECT cloudpayments_subscription_id FROM subscriptions WHERE user_id=$1
		`, uid).Scan(&activeSubscriptionID)
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
		if activeSubscriptionID.Valid && activeSubscriptionID.String != "" && activeSubscriptionID.String != cpSubscriptionID {
			return h.storeEvent(
				eventType, uid, 0, transactionID, cpSubscriptionID, accountID, amount, currency, form,
			)
		}
	}
	token := formValue(form, "Token")
	now := time.Now().UTC()
	trialEndsAt := now.AddDate(0, 0, h.cp.TrialDays())
	nextPaymentAt := parseCloudPaymentsTime(firstNonEmpty(
		formValue(form, "NextTransactionDateIso"),
		formValue(form, "NextTransactionDate"),
	))
	if nextPaymentAt == nil && eventType == "pay" {
		if h.cp.TrialDays() > 0 {
			nextPaymentAt = &trialEndsAt
		} else {
			months := 1
			if selection.billingPeriod == v2billing.PeriodQuarterly {
				months = 3
			} else if selection.billingPeriod == v2billing.PeriodAnnual {
				months = 12
			}
			fallback := now.AddDate(0, months, 0)
			nextPaymentAt = &fallback
		}
	}

	status := statusActive
	switch eventType {
	case "pay":
		if h.cp.TrialDays() > 0 && nextPaymentAt != nil && nextPaymentAt.After(now.Add(24*time.Hour)) {
			status = statusTrial
		}
	case "fail":
		status = statusPastDue
	case "cancel":
		status = statusCanceled
	case "recurrent":
		cpStatus := strings.ToLower(formValue(form, "Status"))
		switch cpStatus {
		case "cancelled", "canceled", "rejected", "expired":
			status = statusCanceled
		case "pastdue":
			status = statusPastDue
		default:
			status = statusActive
		}
	}

	subscriptionID, err := h.upsertSubscription(uid, selection, status, cpSubscriptionID, token, amount, currency, nextPaymentAt, eventType)
	if err != nil {
		return err
	}

	return h.storeEvent(eventType, uid, subscriptionID, transactionID, cpSubscriptionID, accountID, amount, currency, form)
}

type checkoutSelection struct {
	planCode      string
	planName      string
	billingPeriod string
	amount        float64
	currency      string
	memberLimit   int
}

func (h *Handler) checkoutSelection(uid int) (checkoutSelection, error) {
	var result checkoutSelection
	err := h.dbx.QueryRow(`
		SELECT plan_code, plan_name, billing_period, amount, currency, member_limit
		FROM subscriptions WHERE user_id=$1
	`, uid).Scan(&result.planCode, &result.planName, &result.billingPeriod, &result.amount, &result.currency, &result.memberLimit)
	if errors.Is(err, sql.ErrNoRows) {
		plan, _ := v2billing.PlanByCode(v2billing.PlanFounder)
		return checkoutSelection{
			planCode: plan.Code, planName: plan.Name, billingPeriod: v2billing.PeriodMonthly,
			amount: plan.MonthlyAmount, currency: plan.Currency, memberLimit: plan.MemberLimit,
		}, nil
	}
	return result, err
}

func (h *Handler) upsertSubscription(uid int, selection checkoutSelection, status string, cpSubscriptionID string, token string, amount float64, currency string, nextPaymentAt *time.Time, eventType string) (int, error) {
	if amount == 0 {
		amount = selection.amount
	}
	if currency == "" {
		currency = selection.currency
	}

	now := time.Now().UTC()
	var currentPeriodStart *time.Time
	var currentPeriodEnd *time.Time
	var trialStartedAt *time.Time
	var trialEndsAt *time.Time
	var graceUntil *time.Time
	var cancelledAt *time.Time
	var lastPaymentAt *time.Time
	var lastFailedAt *time.Time

	switch status {
	case statusTrial:
		currentPeriodStart = &now
		currentPeriodEnd = nextPaymentAt
		trialStartedAt = &now
		trialEndsAt = nextPaymentAt
		lastPaymentAt = &now
	case statusActive:
		currentPeriodStart = &now
		months := 1
		if selection.billingPeriod == v2billing.PeriodQuarterly {
			months = 3
		} else if selection.billingPeriod == v2billing.PeriodAnnual {
			months = 12
		}
		end := now.AddDate(0, months, 0)
		if nextPaymentAt != nil {
			end = *nextPaymentAt
		}
		currentPeriodEnd = &end
		lastPaymentAt = &now
	case statusPastDue:
		grace := now.AddDate(0, 0, 14)
		graceUntil = &grace
		lastFailedAt = &now
	case statusCanceled:
		cancelledAt = &now
	}

	var id int
	err := h.dbx.QueryRow(`
		INSERT INTO subscriptions (
			user_id, workspace_id, cloudpayments_subscription_id, cloudpayments_token, status, plan_name,
			plan_code, billing_period, amount, currency, member_limit, trial_started_at, trial_ends_at, current_period_start,
			current_period_end, next_payment_at, grace_until, cancelled_at, last_payment_at,
			last_failed_at, failed_attempts, payment_method, payment_provider
		)
		VALUES (
			$1,
			(SELECT id FROM workspaces WHERE owner_user_id=$1 AND status='active' ORDER BY created_at ASC LIMIT 1),
			nullif($2,''), nullif($3,''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
			CASE WHEN $4 = 'past_due' THEN 1 ELSE 0 END,
			'card', 'cloudpayments'
		)
		ON CONFLICT (user_id) DO UPDATE SET
			workspace_id=COALESCE(subscriptions.workspace_id, EXCLUDED.workspace_id),
			cloudpayments_subscription_id=COALESCE(nullif(EXCLUDED.cloudpayments_subscription_id,''), subscriptions.cloudpayments_subscription_id),
			cloudpayments_token=COALESCE(nullif(EXCLUDED.cloudpayments_token,''), subscriptions.cloudpayments_token),
			status=EXCLUDED.status,
			plan_name=EXCLUDED.plan_name,
			plan_code=EXCLUDED.plan_code,
			billing_period=EXCLUDED.billing_period,
			amount=EXCLUDED.amount,
			currency=EXCLUDED.currency,
			member_limit=EXCLUDED.member_limit,
			trial_started_at=COALESCE(EXCLUDED.trial_started_at, subscriptions.trial_started_at),
			trial_ends_at=COALESCE(EXCLUDED.trial_ends_at, subscriptions.trial_ends_at),
			current_period_start=COALESCE(EXCLUDED.current_period_start, subscriptions.current_period_start),
			current_period_end=COALESCE(EXCLUDED.current_period_end, subscriptions.current_period_end),
			next_payment_at=COALESCE(EXCLUDED.next_payment_at, subscriptions.next_payment_at),
			grace_until=CASE WHEN EXCLUDED.status = 'past_due' THEN EXCLUDED.grace_until ELSE NULL END,
			cancelled_at=COALESCE(EXCLUDED.cancelled_at, subscriptions.cancelled_at),
			last_payment_at=COALESCE(EXCLUDED.last_payment_at, subscriptions.last_payment_at),
			last_failed_at=COALESCE(EXCLUDED.last_failed_at, subscriptions.last_failed_at),
			failed_attempts=CASE
				WHEN EXCLUDED.status = 'past_due' THEN subscriptions.failed_attempts + 1
				WHEN EXCLUDED.status IN ('active', 'trial_active') THEN 0
				ELSE subscriptions.failed_attempts
			END,
			payment_method='card',
			payment_provider='cloudpayments',
			updated_at=NOW()
		RETURNING id
	`, uid, cpSubscriptionID, token, status, selection.planName, selection.planCode, selection.billingPeriod,
		amount, currency, selection.memberLimit, trialStartedAt, trialEndsAt, currentPeriodStart, currentPeriodEnd,
		nextPaymentAt, graceUntil, cancelledAt, lastPaymentAt, lastFailedAt).Scan(&id)
	if err != nil {
		return 0, err
	}

	if eventType == "pay" || eventType == "recurrent" {
		_, _ = h.dbx.Exec(`
			UPDATE subscriptions
			SET grace_until=NULL, failed_attempts=0, updated_at=NOW()
			WHERE id=$1 AND status IN ($2, $3)
		`, id, statusActive, statusTrial)
	}

	return id, nil
}

func (h *Handler) statusForUser(uid int) (map[string]any, error) {
	var row struct {
		Status           string
		PlanName         string
		Amount           float64
		Currency         string
		TrialEndsAt      sql.NullTime
		CurrentPeriodEnd sql.NullTime
		NextPaymentAt    sql.NullTime
		GraceUntil       sql.NullTime
		CancelledAt      sql.NullTime
		FailedAttempts   int
	}

	err := h.dbx.QueryRow(`
		SELECT status, plan_name, amount, currency, trial_ends_at, current_period_end,
			next_payment_at, grace_until, cancelled_at, failed_attempts
		FROM subscriptions
		WHERE user_id=$1
	`, uid).Scan(
		&row.Status,
		&row.PlanName,
		&row.Amount,
		&row.Currency,
		&row.TrialEndsAt,
		&row.CurrentPeriodEnd,
		&row.NextPaymentAt,
		&row.GraceUntil,
		&row.CancelledAt,
		&row.FailedAttempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{
			"status":       statusInactive,
			"plan":         h.cp.PlanName(),
			"price":        formatPrice(h.cp.Amount(), h.cp.Currency()),
			"amount":       h.cp.Amount(),
			"currency":     h.cp.Currency(),
			"trial_days":   h.cp.TrialDays(),
			"access":       false,
			"days_left":    0,
			"next_payment": nil,
		}, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	status := row.Status
	if status == statusPastDue && row.GraceUntil.Valid && now.After(row.GraceUntil.Time) {
		status = statusExpired
		_, _ = h.dbx.Exec(`UPDATE subscriptions SET status=$1, updated_at=NOW() WHERE user_id=$2`, statusExpired, uid)
	}

	endsAt := firstValidTime(row.TrialEndsAt, row.CurrentPeriodEnd)
	access := status == statusTrial || status == statusActive ||
		(status == statusCanceled && endsAt != nil && now.Before(*endsAt)) ||
		(status == statusPastDue && row.GraceUntil.Valid && now.Before(row.GraceUntil.Time))

	daysLeft := 0
	if endsAt != nil && endsAt.After(now) {
		daysLeft = int(endsAt.Sub(now).Hours()/24) + 1
	}

	return map[string]any{
		"status":            status,
		"plan":              row.PlanName,
		"price":             formatPrice(row.Amount, row.Currency),
		"amount":            row.Amount,
		"currency":          row.Currency,
		"trial_days":        h.cp.TrialDays(),
		"days_left":         daysLeft,
		"next_payment_date": nullableTime(row.NextPaymentAt),
		"period_end_date":   nullableTime(row.CurrentPeriodEnd),
		"grace_until":       nullableTime(row.GraceUntil),
		"cancelled_at":      nullableTime(row.CancelledAt),
		"failed_attempts":   row.FailedAttempts,
		"access":            access,
	}, nil
}

func (h *Handler) storeEvent(eventType string, uid int, subscriptionID int, transactionID string, cpSubscriptionID string, accountID string, amount float64, currency string, form url.Values) error {
	payload := make(map[string]any)
	for key, values := range form {
		if !safePaymentEventField(key) {
			continue
		}
		if len(values) == 1 {
			payload[key] = values[0]
		} else {
			payload[key] = values
		}
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = h.dbx.Exec(`
		INSERT INTO payment_events (
			event_type, user_id, subscription_id, cloudpayments_transaction_id,
			cloudpayments_subscription_id, account_id, amount, currency, payload
		)
		VALUES ($1,$2,NULLIF($3,0),nullif($4,''),nullif($5,''),nullif($6,''),$7,nullif($8,''),$9)
	`, eventType, uid, subscriptionID, transactionID, cpSubscriptionID, accountID, amount, currency, string(rawPayload))

	return err
}

func cloudPaymentsOrderID(form url.Values) (int64, bool) {
	raw := strings.TrimSpace(formValue(form, "InvoiceId"))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value > 0
}

func safePaymentEventField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "status", "statuscode", "reason", "reasoncode", "testmode", "datetime", "operationtype", "paymentamount":
		return true
	default:
		return false
	}
}

func accountIDForUser(uid int) string {
	return "reup_user_" + strconv.Itoa(uid)
}

func userIDFromAccountID(accountID string) (int, bool) {
	value := strings.TrimPrefix(accountID, "reup_user_")
	if value == accountID {
		return 0, false
	}

	uid, err := strconv.Atoi(value)
	if err != nil || uid <= 0 {
		return 0, false
	}

	return uid, true
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func writeWebhookCode(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"code": code})
}

func readRawBody(r *http.Request) []byte {
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	return data
}

func formValue(form url.Values, key string) string {
	for formKey, values := range form {
		if strings.EqualFold(formKey, key) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}

	return ""
}

func parseAmount(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
	return parsed
}

func parseCloudPaymentsTime(value string) *time.Time {
	if value == "" {
		return nil
	}

	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	if parsed, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		utc := parsed.UTC()
		return &utc
	}

	return nil
}

func nullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}

	return value.Time.UTC().Format(time.RFC3339)
}

func firstValidTime(values ...sql.NullTime) *time.Time {
	for _, value := range values {
		if value.Valid {
			utc := value.Time.UTC()
			return &utc
		}
	}

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func formatPrice(amount float64, currency string) string {
	return strconv.FormatFloat(amount, 'f', 0, 64) + " ₽ / месяц"
}
