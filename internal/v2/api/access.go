package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/billing"
	"reup-goals-backend/internal/v2/workspaces"
)

// RequireProductAccess protects the paid product surface. Authentication,
// onboarding and billing endpoints intentionally stay outside this middleware
// so a new workspace can finish its company-context interview and subscribe.
func RequireProductAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, false, false, next)
}

// RequireOnboardingOrProductAccess keeps only the initial context interview
// available to an unpaid workspace. Once onboarding is complete, these same
// endpoints become part of the paid product surface.
func RequireOnboardingOrProductAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, true, false, next)
}

func RequireAIChatAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, false, true, next)
}

func RequireOnboardingOrAIChatAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, true, true, next)
}

func requireWorkspaceAccess(dbx *sql.DB, allowPendingOnboarding, requireAIChat bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		workspace, _, err := workspaces.NewStore(dbx).GetOrCreateDefault(r.Context(), userID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "workspace_access_failed")
			return
		}
		access, err := WorkspaceSubscriptionAccess(r.Context(), dbx, workspace.ID, workspace.OwnerUserID, time.Now().UTC())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "subscription_lookup_failed")
			return
		}
		if allowPendingOnboarding {
			pending, pendingErr := workspaces.OnboardingPending(r.Context(), dbx, workspace.ID)
			if pendingErr != nil {
				WriteError(w, http.StatusInternalServerError, "onboarding_access_failed")
				return
			}
			if pending {
				next(w, r)
				return
			}
		}
		if !access.Product {
			WriteError(w, http.StatusPaymentRequired, "payment_required")
			return
		}
		if requireAIChat && !access.AIChat {
			WriteError(w, http.StatusForbidden, "ai_chat_not_included")
			return
		}
		next(w, r)
	}
}

type SubscriptionAccess struct {
	Product  bool
	PlanCode string
	AIChat   bool
}

func WorkspaceSubscriptionAccess(ctx context.Context, dbx *sql.DB, workspaceID, ownerUserID int, now time.Time) (SubscriptionAccess, error) {
	var status, planCode string
	var periodEnd, graceUntil sql.NullTime
	err := dbx.QueryRowContext(ctx, `
		SELECT status, plan_code, current_period_end, grace_until
		FROM subscriptions
		WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
		ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	`, workspaceID, ownerUserID).Scan(&status, &planCode, &periodEnd, &graceUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionAccess{}, nil
	}
	if err != nil {
		return SubscriptionAccess{}, err
	}
	product := subscriptionGrantsProductAccess(status, periodEnd, graceUntil, now)
	plan, err := billing.PlanByCode(planCode)
	if err != nil {
		return SubscriptionAccess{}, err
	}
	return SubscriptionAccess{Product: product, PlanCode: plan.Code, AIChat: product && plan.AIChatEnabled}, nil
}

func WorkspaceHasProductAccess(ctx context.Context, dbx *sql.DB, workspaceID, ownerUserID int, now time.Time) (bool, error) {
	access, err := WorkspaceSubscriptionAccess(ctx, dbx, workspaceID, ownerUserID, now)
	return access.Product, err
}

func subscriptionGrantsProductAccess(status string, periodEnd, graceUntil sql.NullTime, now time.Time) bool {
	switch status {
	case "active", "trial_active":
		return true
	case "cancelled":
		return periodEnd.Valid && now.Before(periodEnd.Time)
	case "past_due":
		return graceUntil.Valid && now.Before(graceUntil.Time)
	default:
		return false
	}
}
