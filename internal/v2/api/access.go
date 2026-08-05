package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/workspaces"
)

// RequireProductAccess protects the paid product surface. Authentication,
// onboarding and billing endpoints intentionally stay outside this middleware
// so a new workspace can finish its company-context interview and subscribe.
func RequireProductAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, false, next)
}

// RequireOnboardingOrProductAccess keeps only the initial context interview
// available to an unpaid workspace. Once onboarding is complete, these same
// endpoints become part of the paid product surface.
func RequireOnboardingOrProductAccess(dbx *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceAccess(dbx, true, next)
}

func requireWorkspaceAccess(dbx *sql.DB, allowPendingOnboarding bool, next http.HandlerFunc) http.HandlerFunc {
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
		allowed, err := WorkspaceHasProductAccess(r.Context(), dbx, workspace.ID, workspace.OwnerUserID, time.Now().UTC())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "subscription_lookup_failed")
			return
		}
		if !allowed && allowPendingOnboarding {
			allowed, err = workspaceOnboardingPending(r.Context(), dbx, workspace.ID)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "onboarding_access_failed")
				return
			}
		}
		if !allowed {
			WriteError(w, http.StatusPaymentRequired, "payment_required")
			return
		}
		next(w, r)
	}
}

func workspaceOnboardingPending(ctx context.Context, dbx *sql.DB, workspaceID int) (bool, error) {
	var pending bool
	err := dbx.QueryRowContext(ctx, `
		SELECT
			COALESCE((
				SELECT ready_revision = 0
				FROM workspace_knowledge_pipeline
				WHERE workspace_id=$1
			), TRUE)
			AND NOT EXISTS (
				SELECT 1 FROM strategies WHERE workspace_id=$1 AND status='active'
			)
	`, workspaceID).Scan(&pending)
	return pending, err
}

func WorkspaceHasProductAccess(ctx context.Context, dbx *sql.DB, workspaceID, ownerUserID int, now time.Time) (bool, error) {
	var status string
	var periodEnd, graceUntil sql.NullTime
	err := dbx.QueryRowContext(ctx, `
		SELECT status, current_period_end, grace_until
		FROM subscriptions
		WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
		ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	`, workspaceID, ownerUserID).Scan(&status, &periodEnd, &graceUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return subscriptionGrantsProductAccess(status, periodEnd, graceUntil, now), nil
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
