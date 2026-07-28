package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"
)

var ErrQuotaExceeded = errors.New("ai_weekly_limit_reached")
var ErrPaymentRequired = errors.New("payment_required")

const maxChatReservationTokens = 75_000

type QuotaSummary struct {
	UsedPercent           int       `json:"used_percent"`
	RemainingPercent      int       `json:"remaining_percent"`
	WeeklyTokenLimit      int       `json:"weekly_token_limit"`
	WeeklyTokensUsed      int       `json:"weekly_tokens_used"`
	PurchasedTokenBalance int       `json:"purchased_token_balance"`
	RemainingTokens       int       `json:"remaining_tokens"`
	State                 string    `json:"state"`
	WindowStartedAt       time.Time `json:"window_started_at"`
	ResetsAt              time.Time `json:"resets_at"`
	Timezone              string    `json:"timezone"`
	ExtraCapacityActive   bool      `json:"extra_capacity_active"`
	AIAvailable           bool      `json:"ai_available"`
	WarningThreshold      int       `json:"warning_threshold"`

	baseLimit        int
	baseUsed         int
	purchasedBalance int
}

type Reservation struct {
	ID string
}

type Service struct {
	dbx     *sql.DB
	enforce bool
}

func NewService(dbx *sql.DB, enforcement ...bool) *Service {
	result := &Service{dbx: dbx}
	if len(enforcement) > 0 {
		result.enforce = enforcement[0]
	}
	return result
}

func (s *Service) Summary(ctx context.Context, workspaceID int) (QuotaSummary, error) {
	if workspaceID <= 0 {
		return QuotaSummary{State: "available", AIAvailable: true, Timezone: "Europe/Moscow"}, nil
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return QuotaSummary{}, err
	}
	defer tx.Rollback()
	state, err := s.ensureQuota(ctx, tx, workspaceID, time.Now().UTC())
	if err != nil {
		return QuotaSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return QuotaSummary{}, err
	}
	return state.summary(), nil
}

func (s *Service) Reserve(ctx context.Context, workspaceID, userID int, module string) (Reservation, error) {
	if workspaceID <= 0 {
		return Reservation{}, nil
	}
	if s.enforce {
		allowed, err := s.hasAIEntitlement(ctx, workspaceID, time.Now().UTC())
		if err != nil {
			return Reservation{}, err
		}
		if !allowed {
			return Reservation{}, ErrPaymentRequired
		}
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Reservation{}, errors.New("ai_quota_transaction_unavailable")
	}
	defer tx.Rollback()
	state, err := s.ensureQuota(ctx, tx, workspaceID, time.Now().UTC())
	if err != nil {
		return Reservation{}, errors.New("ai_quota_state_unavailable")
	}

	reservation := reserveQuotaTokens(&state, maxChatReservationTokens)
	if reservation.total <= 0 {
		if err := s.syncWarning(ctx, tx, workspaceID, state, true); err != nil {
			return Reservation{}, err
		}
		if err := tx.Commit(); err != nil {
			return Reservation{}, err
		}
		return Reservation{}, ErrQuotaExceeded
	}
	source := "base"
	if reservation.base == 0 {
		source = "purchased"
	}

	reservationID := randomID()
	reservationMetadata, err := json.Marshal(map[string]int{
		"reserved_base":      reservation.base,
		"reserved_purchased": reservation.purchased,
	})
	if err != nil {
		return Reservation{}, errors.New("ai_quota_reservation_metadata_failed")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_ai_quotas
		SET base_used=$2, purchased_balance=$3, warning_level=$4, updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, state.baseUsed, state.purchasedBalance, state.warningLevel()); err != nil {
		return Reservation{}, errors.New("ai_quota_reservation_update_failed")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_ai_quota_events (
			workspace_id, user_id, reservation_key, event_type, source, amount, status,
			ai_module, metadata_json
		) VALUES (
			$1, NULLIF($2, 0), $3, 'ai_call', $4, $5, 'reserved', $6,
			$7::jsonb
		)
	`, workspaceID, userID, reservationID, source, reservation.total, module, reservationMetadata); err != nil {
		return Reservation{}, errors.New("ai_quota_reservation_event_failed")
	}
	if err := s.syncWarning(ctx, tx, workspaceID, state, false); err != nil {
		log.Printf("[WARN] ai quota warning sync failed workspace_id=%d: %v", workspaceID, err)
	}
	if err := tx.Commit(); err != nil {
		return Reservation{}, errors.New("ai_quota_reservation_commit_failed")
	}
	return Reservation{ID: reservationID}, nil
}

func (s *Service) hasAIEntitlement(ctx context.Context, workspaceID int, now time.Time) (bool, error) {
	var status string
	var periodEnd, graceUntil sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		SELECT subscription.status, subscription.current_period_end, subscription.grace_until
		FROM subscriptions subscription
		WHERE subscription.workspace_id=$1
			OR (
				subscription.workspace_id IS NULL
				AND subscription.user_id=(SELECT owner_user_id FROM workspaces WHERE id=$1)
			)
		ORDER BY CASE WHEN subscription.workspace_id=$1 THEN 0 ELSE 1 END,
			subscription.updated_at DESC
		LIMIT 1
	`, workspaceID).Scan(&status, &periodEnd, &graceUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch status {
	case "active", "trial_active":
		return true, nil
	case "cancelled":
		return periodEnd.Valid && now.Before(periodEnd.Time), nil
	case "past_due":
		return graceUntil.Valid && now.Before(graceUntil.Time), nil
	default:
		return false, nil
	}
}

func (s *Service) Settle(ctx context.Context, reservationID string, success bool, tokenUsage int) error {
	if reservationID == "" {
		return nil
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var workspaceID, reservedTokens, reservedBase, reservedPurchased int
	var source, status string
	var reservedAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT workspace_id, source, status, created_at, amount,
			COALESCE(
				NULLIF(metadata_json->>'reserved_base', '')::INTEGER,
				CASE WHEN source='base' THEN amount ELSE 0 END
			),
			COALESCE(
				NULLIF(metadata_json->>'reserved_purchased', '')::INTEGER,
				CASE WHEN source='purchased' THEN amount ELSE 0 END
			)
		FROM workspace_ai_quota_events
		WHERE reservation_key=$1
		FOR UPDATE
	`, reservationID).Scan(
		&workspaceID,
		&source,
		&status,
		&reservedAt,
		&reservedTokens,
		&reservedBase,
		&reservedPurchased,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "reserved" {
		return tx.Commit()
	}
	if success {
		state, stateErr := s.ensureQuota(ctx, tx, workspaceID, time.Now().UTC())
		if stateErr != nil {
			return stateErr
		}
		actualTokens := max(0, tokenUsage)
		charge := tokenCharge{actual: actualTokens}
		if !reservedAt.Before(state.windowStartedAt) {
			charge = settleReservedTokens(&state, reservedBase, reservedPurchased, actualTokens)
			if _, updateErr := tx.ExecContext(ctx, `
				UPDATE workspace_ai_quotas
				SET base_used=$2, purchased_balance=$3, warning_level=$4, updated_at=NOW()
				WHERE workspace_id=$1
			`, workspaceID, state.baseUsed, state.purchasedBalance, state.warningLevel()); updateErr != nil {
				return updateErr
			}
			if warningErr := s.syncWarning(ctx, tx, workspaceID, state, !state.available()); warningErr != nil {
				return warningErr
			}
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE workspace_ai_quota_events
			SET status='consumed', amount=$2, settled_at=NOW(),
				metadata_json=jsonb_build_object(
					'actual_tokens', $3,
					'base_tokens', $4,
					'purchased_tokens', $5,
					'unbilled_tokens', GREATEST(0, $3 - $2),
					'reserved_tokens', $6,
					'reserved_base', $7,
					'reserved_purchased', $8
				)
			WHERE reservation_key=$1
		`, reservationID, charge.charged, actualTokens, charge.base, charge.purchased,
			reservedTokens, reservedBase, reservedPurchased)
		return commitOrRollback(tx, err)
	}

	state, err := s.ensureQuota(ctx, tx, workspaceID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !reservedAt.Before(state.windowStartedAt) {
		refundReservedTokens(&state, reservedBase, reservedPurchased)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_ai_quotas
		SET base_used=$2, purchased_balance=$3, warning_level=$4, updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, state.baseUsed, state.purchasedBalance, state.warningLevel()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_ai_quota_events SET status='refunded', settled_at=NOW()
		WHERE reservation_key=$1
	`, reservationID); err != nil {
		return err
	}
	if err := s.syncWarning(ctx, tx, workspaceID, state, false); err != nil {
		return err
	}
	return tx.Commit()
}

type quotaState struct {
	planCode         string
	timezone         string
	windowStartedAt  time.Time
	windowEndsAt     time.Time
	baseLimit        int
	baseUsed         int
	purchasedBalance int
}

func (s *Service) ensureQuota(ctx context.Context, tx *sql.Tx, workspaceID int, now time.Time) (quotaState, error) {
	var planCode, timezone string
	var anchor time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(NULLIF(subscription.plan_code, ''), 'founder'),
			COALESCE(NULLIF(workspace.timezone, ''), 'Europe/Moscow'),
			COALESCE(subscription.quota_anchor_at, subscription.current_period_start, workspace.created_at)
		FROM workspaces workspace
		LEFT JOIN LATERAL (
			SELECT plan_code, quota_anchor_at, current_period_start
			FROM subscriptions
			WHERE workspace_id=workspace.id OR (workspace_id IS NULL AND user_id=workspace.owner_user_id)
			ORDER BY CASE WHEN workspace_id=workspace.id THEN 0 ELSE 1 END, updated_at DESC
			LIMIT 1
		) subscription ON TRUE
		WHERE workspace.id=$1
	`, workspaceID).Scan(&planCode, &timezone, &anchor)
	if err != nil {
		return quotaState{}, err
	}
	plan, err := PlanByCode(planCode)
	if err != nil {
		plan, _ = PlanByCode(PlanFounder)
	}
	windowStart, windowEnd := quotaWindow(anchor.UTC(), now.UTC())

	state := quotaState{
		planCode: plan.Code, timezone: timezone, windowStartedAt: windowStart,
		windowEndsAt: windowEnd, baseLimit: plan.WeeklyTokenLimit,
	}
	var storedPlan string
	var storedStart, storedEnd time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT plan_code, window_started_at, window_ends_at, base_limit, base_used, purchased_balance
		FROM workspace_ai_quotas
		WHERE workspace_id=$1
		FOR UPDATE
	`, workspaceID).Scan(
		&storedPlan, &storedStart, &storedEnd, &state.baseLimit, &state.baseUsed, &state.purchasedBalance,
	)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quotas (
				workspace_id, plan_code, window_started_at, window_ends_at,
				base_limit, base_used, purchased_balance, warning_level
			) VALUES ($1,$2,$3,$4,$5,0,0,0)
		`, workspaceID, plan.Code, windowStart, windowEnd, plan.WeeklyTokenLimit)
		state.baseLimit = plan.WeeklyTokenLimit
		return state, err
	}
	if err != nil {
		return quotaState{}, err
	}

	windowChanged := !storedStart.Equal(windowStart) || !storedEnd.Equal(windowEnd)
	storedBaseLimit := state.baseLimit
	planChanged := storedPlan != plan.Code || storedBaseLimit != plan.WeeklyTokenLimit
	state.planCode = plan.Code
	state.windowStartedAt = windowStart
	state.windowEndsAt = windowEnd
	state.baseLimit = plan.WeeklyTokenLimit
	legacyUnits := storedPlan == plan.Code && isLegacyMessageLimit(storedPlan, storedBaseLimit)
	if legacyUnits {
		state.purchasedBalance = scaleQuotaUnits(state.purchasedBalance, storedBaseLimit, plan.WeeklyTokenLimit)
	}
	if windowChanged {
		state.baseUsed = 0
	} else if legacyUnits {
		state.baseUsed = scaleQuotaUnits(state.baseUsed, storedBaseLimit, plan.WeeklyTokenLimit)
	}
	if windowChanged || planChanged {
		_, err = tx.ExecContext(ctx, `
			UPDATE workspace_ai_quotas
			SET plan_code=$2, window_started_at=$3, window_ends_at=$4,
				base_limit=$5, base_used=$6, purchased_balance=$7,
				warning_level=0, updated_at=NOW()
			WHERE workspace_id=$1
		`, workspaceID, plan.Code, windowStart, windowEnd, plan.WeeklyTokenLimit, state.baseUsed, state.purchasedBalance)
		if err != nil {
			return quotaState{}, err
		}
		if windowChanged {
			_, _ = tx.ExecContext(ctx, `
				INSERT INTO workspace_ai_quota_events (
					workspace_id, reservation_key, event_type, source, amount, status, ai_module
				) VALUES ($1, $2, 'weekly_reset', 'base', $3, 'consumed', 'billing')
			`, workspaceID, "weekly-"+randomID(), plan.WeeklyTokenLimit)
			_, _ = tx.ExecContext(ctx, `
				UPDATE v2_system_warnings
				SET status='resolved', resolved_at=NOW(), updated_at=NOW()
				WHERE workspace_id=$1 AND warning_key='ai_quota_usage' AND status='active'
			`, workspaceID)
		}
	}
	return state, nil
}

func (s *Service) syncWarning(ctx context.Context, tx *sql.Tx, workspaceID int, state quotaState, exhausted bool) error {
	level := state.warningLevel()
	if level < 70 {
		_, err := tx.ExecContext(ctx, `
			UPDATE v2_system_warnings
			SET status='resolved', resolved_at=NOW(), updated_at=NOW()
			WHERE workspace_id=$1 AND warning_key='ai_quota_usage' AND status='active'
		`, workspaceID)
		return err
	}
	severity := "info"
	title := "Использовано 70% недельного AI-лимита"
	message := "Лимит обновится автоматически в начале следующего недельного периода."
	if level >= 90 {
		severity = "warning"
		title = "Использовано 90% недельного AI-лимита"
		message = "Чтобы AI продолжил работу без паузы, можно перейти на следующий тариф или купить сброс лимита."
	}
	if level >= 100 {
		title = "Недельный AI-лимит использован"
		if state.purchasedBalance > 0 && !exhausted {
			message = "Основной лимит закончился. Сейчас используется ранее купленный дополнительный запас."
		} else {
			severity = "critical"
			message = "AI временно недоступен. Остальные функции продолжают работать; можно купить сброс лимита или дождаться обновления."
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO v2_system_warnings (
			workspace_id, warning_key, severity, title, message, details_json, status
		) VALUES ($1, 'ai_quota_usage', $2, $3, $4, jsonb_build_object('used_percent', $5), 'active')
		ON CONFLICT (workspace_id, warning_key) WHERE status='active'
		DO UPDATE SET severity=EXCLUDED.severity, title=EXCLUDED.title,
			message=EXCLUDED.message, details_json=EXCLUDED.details_json, updated_at=NOW()
	`, workspaceID, severity, title, message, level)
	return err
}

func (state quotaState) warningLevel() int {
	if state.baseLimit <= 0 {
		return 100
	}
	percent := int(math.Floor(float64(state.baseUsed) * 100 / float64(state.baseLimit)))
	switch {
	case percent >= 100:
		return 100
	case percent >= 90:
		return 90
	case percent >= 70:
		return 70
	default:
		return 0
	}
}

func (state quotaState) available() bool {
	return state.baseUsed < state.baseLimit || state.purchasedBalance > 0
}

func (state quotaState) summary() QuotaSummary {
	used := 0
	if state.baseLimit > 0 {
		used = int(math.Round(float64(state.baseUsed) * 100 / float64(state.baseLimit)))
	}
	if used > 100 {
		used = 100
	}
	available := state.available()
	status := "available"
	if !available {
		status = "exhausted"
	} else if used >= 90 {
		status = "low"
	}
	return QuotaSummary{
		UsedPercent: used, RemainingPercent: max(0, 100-used), State: status,
		WeeklyTokenLimit: state.baseLimit, WeeklyTokensUsed: min(state.baseUsed, state.baseLimit),
		PurchasedTokenBalance: state.purchasedBalance,
		RemainingTokens:       max(0, state.baseLimit-state.baseUsed) + state.purchasedBalance,
		WindowStartedAt:       state.windowStartedAt, ResetsAt: state.windowEndsAt,
		Timezone: state.timezone, ExtraCapacityActive: state.purchasedBalance > 0,
		AIAvailable: available, WarningThreshold: state.warningLevel(),
		baseLimit: state.baseLimit, baseUsed: state.baseUsed, purchasedBalance: state.purchasedBalance,
	}
}

type tokenCharge struct {
	actual    int
	charged   int
	base      int
	purchased int
}

type tokenReservation struct {
	total     int
	base      int
	purchased int
}

func reserveQuotaTokens(state *quotaState, requested int) tokenReservation {
	baseAvailable := max(0, state.baseLimit-state.baseUsed)
	totalAvailable := baseAvailable + max(0, state.purchasedBalance)
	reserved := tokenReservation{total: min(max(0, requested), totalAvailable)}
	reserved.base = min(reserved.total, baseAvailable)
	reserved.purchased = reserved.total - reserved.base
	state.baseUsed += reserved.base
	state.purchasedBalance -= reserved.purchased
	return reserved
}

func settleReservedTokens(state *quotaState, reservedBase, reservedPurchased, actualTokens int) tokenCharge {
	result := tokenCharge{actual: max(0, actualTokens)}
	refundReservedTokens(state, reservedBase, reservedPurchased)
	remaining := result.actual

	baseTokens := min(remaining, max(0, state.baseLimit-state.baseUsed))
	state.baseUsed += baseTokens
	result.base += baseTokens
	result.charged += baseTokens
	remaining -= baseTokens

	purchasedTokens := min(remaining, state.purchasedBalance)
	state.purchasedBalance -= purchasedTokens
	result.purchased += purchasedTokens
	result.charged += purchasedTokens
	return result
}

func refundReservedTokens(state *quotaState, reservedBase, reservedPurchased int) {
	state.baseUsed = max(0, state.baseUsed-max(0, reservedBase))
	state.purchasedBalance += max(0, reservedPurchased)
}

func isLegacyMessageLimit(planCode string, limit int) bool {
	legacy := map[string]int{
		PlanFounder: 150,
		PlanTeam:    400,
		PlanCompany: 1200,
	}
	return legacy[planCode] == limit
}

func scaleQuotaUnits(value, oldLimit, newLimit int) int {
	if value <= 0 || oldLimit <= 0 || newLimit <= 0 {
		return 0
	}
	return int(math.Round(float64(value) * float64(newLimit) / float64(oldLimit)))
}

func quotaWindow(anchor, now time.Time) (time.Time, time.Time) {
	if anchor.IsZero() || anchor.After(now) {
		anchor = now
	}
	const week = 7 * 24 * time.Hour
	elapsed := now.Sub(anchor)
	periods := elapsed / week
	start := anchor.Add(periods * week)
	return start, start.Add(week)
}

func commitOrRollback(tx *sql.Tx, err error) error {
	if err != nil {
		return err
	}
	return tx.Commit()
}

func randomID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
