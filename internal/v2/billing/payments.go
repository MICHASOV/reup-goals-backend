package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type CloudPaymentConfirmation struct {
	SubscriptionIDToCancel string
}

func (s *Service) ValidateCloudPaymentOrder(ctx context.Context, orderID int64, userID int, amount float64, currency string) (bool, error) {
	var createdBy int
	var status, expectedCurrency string
	var expectedAmount float64
	err := s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(billing_order.created_by, workspace.owner_user_id),
			billing_order.status, billing_order.amount, billing_order.currency
		FROM workspace_billing_orders billing_order
		JOIN workspaces workspace ON workspace.id=billing_order.workspace_id
		WHERE billing_order.id=$1 AND billing_order.provider='cloudpayments'
	`, orderID).Scan(&createdBy, &status, &expectedAmount, &expectedCurrency)
	if err != nil {
		return false, err
	}
	return createdBy == userID && status == "waiting" &&
		strings.EqualFold(expectedCurrency, currency) && math.Abs(expectedAmount-amount) <= 0.009, nil
}

func (s *Service) ConfirmCloudPaymentOrder(ctx context.Context, orderID int64, userID int, transactionID, cloudSubscriptionID, token string, amount float64, currency string) (CloudPaymentConfirmation, error) {
	if orderID <= 0 || userID <= 0 || strings.TrimSpace(transactionID) == "" {
		return CloudPaymentConfirmation{}, errors.New("invalid_cloudpayments_order")
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return CloudPaymentConfirmation{}, err
	}
	defer tx.Rollback()

	var workspaceID, ownerUserID, createdBy, quantity int
	var kind, planCode, period, status, expectedCurrency, replacedSubscriptionID, replacementStatus, storedSubscriptionID string
	var expectedAmount float64
	err = tx.QueryRowContext(ctx, `
		SELECT billing_order.workspace_id, workspace.owner_user_id,
			COALESCE(billing_order.created_by, workspace.owner_user_id), billing_order.quantity,
			billing_order.order_kind, billing_order.plan_code, billing_order.billing_period,
			billing_order.status, billing_order.amount, billing_order.currency,
			COALESCE(billing_order.metadata_json->>'replace_cloudpayments_subscription_id',''),
			COALESCE(billing_order.metadata_json->>'replacement_status',''),
			COALESCE(billing_order.metadata_json->>'cloudpayments_subscription_id','')
		FROM workspace_billing_orders billing_order
		JOIN workspaces workspace ON workspace.id=billing_order.workspace_id
		WHERE billing_order.id=$1 AND billing_order.provider='cloudpayments'
		FOR UPDATE OF billing_order
	`, orderID).Scan(
		&workspaceID, &ownerUserID, &createdBy, &quantity, &kind, &planCode, &period,
		&status, &expectedAmount, &expectedCurrency, &replacedSubscriptionID, &replacementStatus,
		&storedSubscriptionID,
	)
	if err != nil {
		return CloudPaymentConfirmation{}, err
	}
	if createdBy != userID || !strings.EqualFold(expectedCurrency, currency) || math.Abs(expectedAmount-amount) > 0.009 {
		return CloudPaymentConfirmation{}, errors.New("cloudpayments_order_mismatch")
	}
	if status == "paid" {
		if err := tx.Commit(); err != nil {
			return CloudPaymentConfirmation{}, err
		}
		return replacementConfirmation(replacedSubscriptionID, storedSubscriptionID, replacementStatus), nil
	}
	if status != "waiting" {
		return CloudPaymentConfirmation{}, errors.New("cloudpayments_order_not_payable")
	}
	plan, err := PlanByCode(planCode)
	if err != nil {
		return CloudPaymentConfirmation{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_billing_orders
		SET status='paid', external_id=$2, paid_at=$3,
			metadata_json=metadata_json || jsonb_build_object('cloudpayments_subscription_id',$4,'cloudpayments_token',$5),
			updated_at=NOW()
		WHERE id=$1
	`, orderID, transactionID, now, cloudSubscriptionID, token); err != nil {
		return CloudPaymentConfirmation{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_billing_payments (
			workspace_id, provider, external_id, method, amount, currency, status, paid_at
		) VALUES ($1,'cloudpayments',$2,'card',$3,$4,'paid',$5)
		ON CONFLICT (provider, external_id) WHERE external_id <> '' DO NOTHING
	`, workspaceID, transactionID, amount, expectedCurrency, now); err != nil {
		return CloudPaymentConfirmation{}, err
	}

	switch kind {
	case OrderQuotaReset:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quotas (
				workspace_id, plan_code, window_started_at, window_ends_at,
				base_limit, base_used, purchased_balance, purchased_reset_balance, warning_level
			) VALUES ($1,$2,$3,$4,$5,0,0,$6,0)
			ON CONFLICT (workspace_id) DO UPDATE SET
				purchased_reset_balance=workspace_ai_quotas.purchased_reset_balance + EXCLUDED.purchased_reset_balance,
				updated_at=NOW()
		`, workspaceID, plan.Code, now, now.AddDate(0, 0, 7), plan.WeeklyTokenLimit, quantity); err != nil {
			return CloudPaymentConfirmation{}, err
		}
	case OrderSubscription:
		months := billingPeriodMonths(period)
		var currentStatus, currentPlan, currentPeriod string
		var currentEnd sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT status, plan_code, billing_period, current_period_end
			FROM subscriptions
			WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
			ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC LIMIT 1
			FOR UPDATE
		`, workspaceID, ownerUserID).Scan(&currentStatus, &currentPlan, &currentPeriod, &currentEnd)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CloudPaymentConfirmation{}, err
		}
		activeUntil := currentEnd.Valid && currentEnd.Time.After(now) && (currentStatus == "active" || currentStatus == "trial_active" || currentStatus == "cancelled")
		if activeUntil {
			pendingEnd := currentEnd.Time.AddDate(0, months, 0)
			_, err = tx.ExecContext(ctx, `
				UPDATE subscriptions SET pending_plan_code=$2, pending_plan_name=$3,
					pending_billing_period=$4, pending_amount=$5, pending_member_limit=$6,
					pending_period_start=$7, pending_period_end=$8,
					cloudpayments_subscription_id=COALESCE(NULLIF($9,''),cloudpayments_subscription_id),
					cloudpayments_token=COALESCE(NULLIF($10,''),cloudpayments_token), updated_at=NOW()
				WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$11)
			`, workspaceID, plan.Code, plan.Name, period, amount, plan.MemberLimit,
				currentEnd.Time, pendingEnd, cloudSubscriptionID, token, ownerUserID)
		} else {
			start := now
			if activeUntil {
				start = currentEnd.Time
			}
			end := start.AddDate(0, months, 0)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO subscriptions (
					user_id, workspace_id, status, plan_name, plan_code, billing_period,
					amount, currency, member_limit, current_period_start, current_period_end,
					next_payment_at, last_payment_at, quota_anchor_at, payment_method, payment_provider,
					cloudpayments_subscription_id, cloudpayments_token
				) VALUES ($1,$2,'active',$3,$4,$5,$6,$7,$8,$9,$10,$10,$11,$9,'card','cloudpayments',NULLIF($12,''),NULLIF($13,''))
				ON CONFLICT (user_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,
					status='active', plan_name=EXCLUDED.plan_name, plan_code=EXCLUDED.plan_code,
					billing_period=EXCLUDED.billing_period, amount=EXCLUDED.amount, currency=EXCLUDED.currency,
					member_limit=EXCLUDED.member_limit, current_period_start=EXCLUDED.current_period_start,
					current_period_end=EXCLUDED.current_period_end, next_payment_at=EXCLUDED.next_payment_at,
					last_payment_at=EXCLUDED.last_payment_at, quota_anchor_at=EXCLUDED.quota_anchor_at,
					payment_method='card', payment_provider='cloudpayments',
					cloudpayments_subscription_id=COALESCE(EXCLUDED.cloudpayments_subscription_id,subscriptions.cloudpayments_subscription_id),
					cloudpayments_token=COALESCE(EXCLUDED.cloudpayments_token,subscriptions.cloudpayments_token), updated_at=NOW()
			`, ownerUserID, workspaceID, plan.Name, plan.Code, period, amount, expectedCurrency,
				plan.MemberLimit, start, end, now, cloudSubscriptionID, token)
		}
		if err != nil {
			return CloudPaymentConfirmation{}, err
		}
	default:
		return CloudPaymentConfirmation{}, fmt.Errorf("unsupported billing order kind %q", kind)
	}
	if err := tx.Commit(); err != nil {
		return CloudPaymentConfirmation{}, err
	}
	return replacementConfirmation(replacedSubscriptionID, cloudSubscriptionID, replacementStatus), nil
}

func replacementConfirmation(previousID, currentID, status string) CloudPaymentConfirmation {
	if status == "pending" && previousID != "" && currentID != "" && previousID != currentID {
		return CloudPaymentConfirmation{SubscriptionIDToCancel: previousID}
	}
	return CloudPaymentConfirmation{}
}

func (s *Service) MarkCloudPaymentSubscriptionReplaced(ctx context.Context, orderID int64, subscriptionID string) error {
	if orderID <= 0 || strings.TrimSpace(subscriptionID) == "" {
		return errors.New("invalid_cloudpayments_subscription_replacement")
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE workspace_billing_orders
		SET metadata_json=metadata_json || jsonb_build_object(
			'replacement_status','cancelled','replaced_cloudpayments_subscription_id',$2
		), updated_at=NOW()
		WHERE id=$1
			AND metadata_json->>'replace_cloudpayments_subscription_id'=$2
	`, orderID, subscriptionID)
	return err
}

func billingPeriodMonths(period string) int {
	switch period {
	case PeriodQuarterly:
		return 3
	case PeriodAnnual:
		return 12
	default:
		return 1
	}
}

func (s *Service) ApplyPendingSubscription(ctx context.Context, workspaceID int) error {
	if workspaceID <= 0 {
		return nil
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE subscriptions SET
			status='active', plan_code=pending_plan_code, plan_name=pending_plan_name,
			billing_period=pending_billing_period, amount=pending_amount,
			member_limit=pending_member_limit, current_period_start=pending_period_start,
			current_period_end=pending_period_end, next_payment_at=pending_period_end,
			quota_anchor_at=pending_period_start, pending_plan_code=NULL,
			pending_plan_name='', pending_billing_period='', pending_amount=NULL,
			pending_member_limit=NULL, pending_period_start=NULL, pending_period_end=NULL,
			updated_at=NOW()
		WHERE (workspace_id=$1 OR (
			workspace_id IS NULL AND user_id=(SELECT owner_user_id FROM workspaces WHERE id=$1)
		)) AND pending_plan_code IS NOT NULL AND pending_period_start <= NOW()
	`, workspaceID)
	return err
}

func (s *Service) ActivatePurchasedReset(ctx context.Context, workspaceID, userID int) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := s.ensureQuota(ctx, tx, workspaceID, time.Now().UTC())
	if err != nil {
		return err
	}
	if state.purchasedResets <= 0 {
		return errors.New("no_purchased_resets")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_ai_quotas
		SET purchased_reset_balance=purchased_reset_balance-1,
			purchased_balance=purchased_balance+$2, updated_at=NOW()
		WHERE workspace_id=$1 AND purchased_reset_balance > 0
	`, workspaceID, state.baseLimit); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_ai_quota_events (
			workspace_id, user_id, reservation_key, event_type, source, amount, status, ai_module
		) VALUES ($1,$2,$3,'activated_reset','purchased',$4,'consumed','billing')
	`, workspaceID, userID, "reset-"+randomID(), state.baseLimit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ConfirmInvoicePayment(ctx context.Context, invoiceID int64, confirmedBy string) error {
	if invoiceID <= 0 {
		return errors.New("invalid_invoice_id")
	}
	confirmedBy = strings.TrimSpace(confirmedBy)
	if confirmedBy == "" {
		confirmedBy = "manual"
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var workspaceID, ownerUserID int
	var orderID sql.NullInt64
	var orderKind, planCode, billingPeriod, status, currency string
	var amount float64
	err = tx.QueryRowContext(ctx, `
		SELECT invoice.workspace_id, workspace.owner_user_id, invoice.order_id,
			invoice.order_kind, invoice.plan_code, invoice.billing_period,
			invoice.status, invoice.amount, invoice.currency
		FROM workspace_billing_invoices invoice
		JOIN workspaces workspace ON workspace.id=invoice.workspace_id
		WHERE invoice.id=$1
		FOR UPDATE OF invoice
	`, invoiceID).Scan(
		&workspaceID, &ownerUserID, &orderID, &orderKind, &planCode,
		&billingPeriod, &status, &amount, &currency,
	)
	if err != nil {
		return err
	}
	if status == "paid" {
		return tx.Commit()
	}
	if status != "waiting" {
		return errors.New("invoice_not_payable")
	}
	plan, err := PlanByCode(planCode)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_billing_invoices
		SET status='paid', paid_at=$2, confirmed_by=$3, updated_at=NOW()
		WHERE id=$1
	`, invoiceID, now, confirmedBy); err != nil {
		return err
	}
	if orderID.Valid {
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspace_billing_orders
			SET status='paid', paid_at=$2, updated_at=NOW()
			WHERE id=$1
		`, orderID.Int64, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_billing_payments (
			workspace_id, invoice_id, provider, external_id, method,
			amount, currency, status, paid_at
		) VALUES ($1,$2,'manual',$3,'invoice',$4,$5,'paid',$6)
	`, workspaceID, invoiceID, confirmedBy, amount, currency, now); err != nil {
		return err
	}

	switch orderKind {
	case OrderSubscription:
		periodEnd := now.AddDate(0, 1, 0)
		if billingPeriod == PeriodQuarterly {
			periodEnd = now.AddDate(0, 3, 0)
		} else if billingPeriod == PeriodAnnual {
			periodEnd = now.AddDate(1, 0, 0)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE subscriptions SET
				workspace_id=$1, status='active', plan_name=$2, plan_code=$3,
				billing_period=$4, amount=$5, currency=$6, current_period_start=$7,
				current_period_end=$8, next_payment_at=$8, grace_until=NULL,
				cancelled_at=NULL, last_payment_at=$7, failed_attempts=0,
				member_limit=$9, quota_anchor_at=$7, payment_method='invoice',
				payment_provider='manual', updated_at=NOW()
				WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$10)
		`, workspaceID, plan.Name, plan.Code, billingPeriod, amount, currency, now,
			periodEnd, plan.MemberLimit, ownerUserID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO subscriptions (
					user_id, workspace_id, status, plan_name, plan_code, billing_period,
					amount, currency, current_period_start, current_period_end,
					next_payment_at, last_payment_at, member_limit, quota_anchor_at,
					payment_method, payment_provider
				) VALUES (
					$1,$2,'active',$3,$4,$5,$6,$7,$8,$9,$9,$8,$10,$8,'invoice','manual'
				)
			`, ownerUserID, workspaceID, plan.Name, plan.Code, billingPeriod,
				amount, currency, now, periodEnd, plan.MemberLimit); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quotas (
				workspace_id, plan_code, window_started_at, window_ends_at,
				base_limit, base_used, purchased_balance, warning_level
			) VALUES ($1,$2,$3,$4,$5,0,0,0)
			ON CONFLICT (workspace_id) DO UPDATE SET
				plan_code=EXCLUDED.plan_code,
				window_started_at=EXCLUDED.window_started_at,
				window_ends_at=EXCLUDED.window_ends_at,
				base_limit=EXCLUDED.base_limit,
				base_used=0,
				warning_level=0,
				updated_at=NOW()
		`, workspaceID, plan.Code, now, now.Add(7*24*time.Hour), plan.WeeklyTokenLimit); err != nil {
			return err
		}
	case OrderQuotaReset:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quotas (
				workspace_id, plan_code, window_started_at, window_ends_at,
				base_limit, base_used, purchased_balance, purchased_reset_balance, warning_level
			) VALUES ($1,$2,$3,$4,$5,0,0,1,0)
			ON CONFLICT (workspace_id) DO UPDATE SET
				purchased_reset_balance=workspace_ai_quotas.purchased_reset_balance + 1,
				updated_at=NOW()
		`, workspaceID, plan.Code, now, now.Add(7*24*time.Hour), plan.WeeklyTokenLimit); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quota_events (
				workspace_id, reservation_key, event_type, source, amount, status, ai_module
			) VALUES ($1,$2,'purchased_reset','purchased',1,'consumed','billing')
		`, workspaceID, "purchase-"+randomID()); err != nil {
			return err
		}
	default:
		return errors.New("billing_order_kind_invalid")
	}

	_, _ = tx.ExecContext(ctx, `
		UPDATE v2_system_warnings
		SET status='resolved', resolved_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$1 AND warning_key='ai_quota_usage' AND status='active'
	`, workspaceID)
	return tx.Commit()
}
