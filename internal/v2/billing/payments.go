package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

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
		if billingPeriod == PeriodAnnual {
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
				base_limit, base_used, purchased_balance, warning_level
			) VALUES ($1,$2,$3,$4,$5,0,$5,0)
			ON CONFLICT (workspace_id) DO UPDATE SET
				purchased_balance=workspace_ai_quotas.purchased_balance + EXCLUDED.purchased_balance,
				updated_at=NOW()
		`, workspaceID, plan.Code, now, now.Add(7*24*time.Hour), plan.WeeklyTokenLimit); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_ai_quota_events (
				workspace_id, reservation_key, event_type, source, amount, status, ai_module
			) VALUES ($1,$2,'purchased_reset','purchased',$3,'consumed','billing')
		`, workspaceID, "purchase-"+randomID(), plan.WeeklyTokenLimit); err != nil {
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
