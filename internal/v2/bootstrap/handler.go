package bootstrap

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	dbx        *sql.DB
	workspaces *workspaces.Store
}

func NewHandler(dbx *sql.DB) *Handler {
	return &Handler{
		dbx:        dbx,
		workspaces: workspaces.NewStore(dbx),
	}
}

type response struct {
	User         userResponse         `json:"user"`
	Workspace    workspaceResponse    `json:"workspace"`
	Membership   membershipResponse   `json:"membership"`
	Subscription subscriptionResponse `json:"subscription"`
	V2           v2Response           `json:"v2"`
}

type userResponse struct {
	ID    int     `json:"id"`
	Email string  `json:"email"`
	Name  *string `json:"name"`
}

type workspaceResponse struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	DisplayName *string `json:"display_name"`
	Status      string  `json:"status"`
}

type membershipResponse struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type subscriptionResponse struct {
	Status       string     `json:"status"`
	Access       bool       `json:"access"`
	AccessReason string     `json:"access_reason"`
	GraceUntil   *time.Time `json:"grace_until"`
}

type v2Response struct {
	Enabled    bool   `json:"enabled"`
	APIVersion string `json:"api_version"`
}

func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	email, err := h.userEmail(uid)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "user_lookup_failed")
		return
	}

	workspace, membership, err := h.workspaces.GetOrCreateDefault(r.Context(), uid)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_bootstrap_failed")
		return
	}

	subscription, err := h.subscription(workspace.ID, workspace.OwnerUserID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "subscription_lookup_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, response{
		User: userResponse{
			ID:    uid,
			Email: email,
			Name:  nil,
		},
		Workspace: workspaceResponse{
			ID:          workspace.ID,
			Name:        workspace.Name,
			DisplayName: workspace.DisplayName,
			Status:      workspace.Status,
		},
		Membership: membershipResponse{
			Role:   membership.Role,
			Status: membership.Status,
		},
		Subscription: subscription,
		V2: v2Response{
			Enabled:    true,
			APIVersion: "v2",
		},
	})
}

func (h *Handler) userEmail(uid int) (string, error) {
	var email string
	err := h.dbx.QueryRow(`SELECT email FROM users WHERE id=$1`, uid).Scan(&email)
	return email, err
}

func (h *Handler) subscription(workspaceID int, ownerUserID int) (subscriptionResponse, error) {
	var row struct {
		Status     string
		GraceUntil sql.NullTime
	}

	err := h.dbx.QueryRow(`
		SELECT status, grace_until
		FROM subscriptions
		WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
		ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	`, workspaceID, ownerUserID).Scan(&row.Status, &row.GraceUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return subscriptionResponse{
			Status:       "active",
			Access:       true,
			AccessReason: "temporary_v2_access",
			GraceUntil:   nil,
		}, nil
	}
	if err != nil {
		return subscriptionResponse{}, err
	}

	status := row.Status
	now := time.Now().UTC()
	access := false
	accessReason := "payment_required"

	switch status {
	case "trial_active":
		access = true
		accessReason = "trial"
	case "active":
		access = true
		accessReason = "active_subscription"
	case "past_due":
		if row.GraceUntil.Valid && now.Before(row.GraceUntil.Time) {
			access = true
			accessReason = "grace_period"
		}
	case "cancelled":
		access = true
		accessReason = "cancelled_period"
	}

	var graceUntil *time.Time
	if row.GraceUntil.Valid {
		graceUntil = &row.GraceUntil.Time
	}

	return subscriptionResponse{
		Status:       status,
		Access:       access,
		AccessReason: accessReason,
		GraceUntil:   graceUntil,
	}, nil
}
