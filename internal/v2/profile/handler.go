package profile

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/subscriptions"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	store        *Store
	dbx          *sql.DB
	cfg          *config.Config
	emailService *auth.EmailService
	payments     *subscriptions.CloudPaymentsClient
}

func NewHandler(dbx *sql.DB, cfg *config.Config, emailService *auth.EmailService, payments *subscriptions.CloudPaymentsClient) *Handler {
	return &Handler{
		store: NewStore(dbx), dbx: dbx, cfg: cfg, emailService: emailService, payments: payments,
	}
}

func (h *Handler) InvitationPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	result, err := h.store.PreviewInvitation(r.Context(), r.URL.Query().Get("token"))
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusNotFound, "invitation_invalid_or_expired")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "invitation_preview_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/profile"), "/")
	segments := splitPath(path)
	if len(segments) == 0 {
		h.overview(w, r, userID)
		return
	}
	if len(segments) == 2 && segments[0] == "invitations" && segments[1] == "accept" {
		h.acceptInvitation(w, r, userID)
		return
	}
	if len(segments) == 2 && segments[0] == "workspace" && segments[1] == "setup" {
		h.workspaceSetup(w, r, userID)
		return
	}

	overview, err := h.loadOverview(r, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "profile_load_failed")
		return
	}

	switch segments[0] {
	case "account":
		h.account(w, r, userID)
	case "password":
		h.password(w, r, userID)
	case "workspace":
		h.workspace(w, r, userID, overview)
	case "members":
		h.members(w, r, userID, overview, segments)
	case "invitations":
		h.invitations(w, r, userID, overview)
	case "settings":
		h.settings(w, r, userID)
	case "billing":
		h.billing(w, r, userID, overview, segments[1:])
	case "about":
		h.about(w, r)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) workspaceSetup(w http.ResponseWriter, r *http.Request, userID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var body struct {
		Name        string `json:"name"`
		CompanyRole string `json:"company_role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.CompanyRole = strings.TrimSpace(body.CompanyRole)
	if len([]rune(body.Name)) < 2 || len([]rune(body.Name)) > 120 || len([]rune(body.CompanyRole)) > 120 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_workspace_setup")
		return
	}
	workspace, membership, err := workspaces.NewStore(h.dbx).Setup(r.Context(), userID, body.Name)
	if errors.Is(err, workspaces.ErrWorkspaceSetupRequired) {
		api.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_setup_failed")
		return
	}
	if _, err := h.dbx.ExecContext(r.Context(), `
		UPDATE users SET company_role=$1 WHERE id=$2
	`, body.CompanyRole, userID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "profile_update_failed")
		return
	}
	displayName := workspace.Name
	if workspace.DisplayName != nil && strings.TrimSpace(*workspace.DisplayName) != "" {
		displayName = strings.TrimSpace(*workspace.DisplayName)
	}
	api.WriteJSON(w, http.StatusCreated, map[string]any{
		"workspace": WorkspaceSummary{
			ID: workspace.ID, Name: workspace.Name, DisplayName: displayName,
			Status: workspace.Status, MemberCount: 1,
		},
		"membership": MembershipSummary{Role: membership.Role, Status: membership.Status},
	})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request, userID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	result, err := h.loadOverview(r, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "profile_load_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request, userID int) {
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var body struct {
		Name        string `json:"name"`
		AvatarURL   string `json:"avatar_url"`
		CompanyRole string `json:"company_role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.AvatarURL = strings.TrimSpace(body.AvatarURL)
	body.CompanyRole = strings.TrimSpace(body.CompanyRole)
	if len([]rune(body.Name)) < 2 || len([]rune(body.Name)) > 120 ||
		len([]rune(body.CompanyRole)) > 120 || len(body.AvatarURL) > 2048 ||
		!validOptionalHTTPURL(body.AvatarURL) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_profile")
		return
	}
	result, err := h.store.UpdateAccount(r.Context(), userID, body.Name, body.AvatarURL, body.CompanyRole)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "profile_update_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) password(w http.ResponseWriter, r *http.Request, userID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := auth.ChangePassword(r.Context(), h.dbx, userID, body.CurrentPassword, body.NewPassword)
	switch {
	case errors.Is(err, auth.ErrCurrentPasswordInvalid):
		api.WriteError(w, http.StatusUnprocessableEntity, "current_password_invalid")
	case err != nil && strings.Contains(err.Error(), "weak_password"):
		api.WriteError(w, http.StatusUnprocessableEntity, "weak_password")
	case err != nil:
		api.WriteError(w, http.StatusInternalServerError, "password_update_failed")
	default:
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "reauthentication_required": true})
	}
}

func (h *Handler) workspace(w http.ResponseWriter, r *http.Request, userID int, overview Overview) {
	if !overview.Capabilities.ManageWorkspace {
		api.WriteError(w, http.StatusForbidden, "owner_required")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if len([]rune(body.Name)) < 2 || len([]rune(body.Name)) > 120 {
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_workspace_name")
			return
		}
		result, err := h.store.UpdateWorkspace(r.Context(), overview.Workspace.ID, body.Name)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "workspace_update_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, result)
	case http.MethodDelete:
		var body struct {
			Confirmation string `json:"confirmation"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Confirmation) != overview.Workspace.DisplayName {
			api.WriteError(w, http.StatusUnprocessableEntity, "workspace_confirmation_mismatch")
			return
		}
		if err := h.store.DeleteWorkspace(r.Context(), overview.Workspace.ID, userID); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "workspace_delete_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request, userID int, overview Overview, segments []string) {
	if len(segments) == 1 {
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		items, err := h.store.Members(r.Context(), overview.Workspace.ID, userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "members_load_failed")
			return
		}
		if !overview.Capabilities.ManageMembers {
			visible := items[:0]
			for _, item := range items {
				if item.Kind == "membership" {
					item.CanBeRemoved = false
					visible = append(visible, item)
				}
			}
			items = visible
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"members": items})
		return
	}
	if !overview.Capabilities.ManageMembers {
		api.WriteError(w, http.StatusForbidden, "member_management_required")
		return
	}
	if len(segments) != 3 {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := strconv.ParseInt(segments[2], 10, 64)
	if err != nil || id <= 0 {
		api.WriteError(w, http.StatusBadRequest, "invalid_member_id")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := h.store.RemoveMember(r.Context(), overview.Workspace.ID, userID, segments[1], id); errors.Is(err, sql.ErrNoRows) {
			api.WriteError(w, http.StatusNotFound, "member_not_found")
			return
		} else if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "member_remove_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPatch:
		if segments[1] != "membership" {
			api.WriteError(w, http.StatusUnprocessableEntity, "member_role_not_editable")
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		item, err := h.store.UpdateMemberRole(
			r.Context(), overview.Workspace.ID, userID, id, strings.ToLower(strings.TrimSpace(body.Role)),
		)
		if errors.Is(err, sql.ErrNoRows) {
			api.WriteError(w, http.StatusNotFound, "member_not_found")
			return
		}
		if errors.Is(err, ErrInvalidMemberRole) {
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "member_role_update_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"member": item})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) invitations(w http.ResponseWriter, r *http.Request, userID int, overview Overview) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !overview.Capabilities.ManageMembers {
		api.WriteError(w, http.StatusForbidden, "member_management_required")
		return
	}
	var body struct {
		Email         string `json:"email"`
		Role          string `json:"role"`
		DepartmentIDs []int  `json:"department_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	email, ok := normalizeEmail(body.Email)
	if !ok || strings.EqualFold(email, overview.Account.Email) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_invitation_email")
		return
	}
	body.Role = strings.ToLower(strings.TrimSpace(body.Role))
	if body.Role == "" {
		body.Role = roleMember
	}
	if body.Role != roleMember && body.Role != roleAdmin {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_invitation_role")
		return
	}
	item, token, err := h.store.Invite(
		r.Context(),
		overview.Workspace.ID,
		userID,
		email,
		body.Role,
		body.DepartmentIDs,
		overview.Subscription.MemberLimit,
	)
	if err != nil {
		var cooldownError *InvitationResendTooSoonError
		if errors.As(err, &cooldownError) {
			retryAfterSeconds := cooldownError.RetryAfterSeconds()
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
			api.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":               cooldownError.Error(),
				"retry_after_seconds": retryAfterSeconds,
			})
			return
		}
		if errors.Is(err, ErrMemberLimitReached) {
			api.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrAlreadyMember) {
			api.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidMemberRole) {
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidDepartments) {
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "invitation_create_failed")
		return
	}
	emailDelivered := true
	if token != "" {
		inviteURL := strings.TrimRight(h.cfg.FrontendBaseURL, "/") + "/invite?token=" +
			url.QueryEscape(token)
		roleLabel := "участника"
		if body.Role == roleAdmin {
			roleLabel = "администратора"
		}
		bodyHTML := fmt.Sprintf(
			"<p>Вас пригласили в Workspace <strong>%s</strong> в REUP.goals в роли %s.</p><p><a href=\"%s\">Принять приглашение</a></p><p>Ссылка действует 7 дней.</p>",
			html.EscapeString(overview.Workspace.DisplayName), roleLabel, html.EscapeString(inviteURL),
		)
		if h.emailService == nil || h.emailService.SendServiceEmail(email, "Приглашение в REUP.goals", bodyHTML) != nil {
			emailDelivered = false
		}
	}
	api.WriteJSON(w, http.StatusCreated, map[string]any{
		"member":              item,
		"email_delivered":     emailDelivered,
		"retry_after_seconds": int(invitationResendCooldown / time.Second),
	})
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request, userID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if len(body.Token) != 64 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_invitation")
		return
	}
	if err := h.store.AcceptInvitation(r.Context(), userID, body.Token); errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invitation_invalid_or_expired")
		return
	} else if errors.Is(err, ErrMemberLimitReached) {
		api.WriteError(w, http.StatusConflict, err.Error())
		return
	} else if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "invitation_accept_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request, userID int) {
	switch r.Method {
	case http.MethodGet:
		result, err := h.store.Settings(r.Context(), userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "settings_load_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, result)
	case http.MethodPatch:
		var body Settings
		if !decodeJSON(w, r, &body) {
			return
		}
		if !validSettings(body) {
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_settings")
			return
		}
		result, err := h.store.UpdateSettings(r.Context(), userID, body)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "settings_update_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, result)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) billing(w http.ResponseWriter, r *http.Request, userID int, overview Overview, segments []string) {
	if !overview.Capabilities.ManageSubscription {
		api.WriteError(w, http.StatusForbidden, "owner_required")
		return
	}
	if len(segments) == 0 {
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		api.WriteJSON(w, http.StatusOK, overview.Subscription)
		return
	}
	switch segments[0] {
	case "checkout":
		h.checkout(w, r, overview)
	case "organization":
		h.billingOrganization(w, r, userID, overview.Workspace.ID)
	case "invoices":
		h.invoices(w, r, userID, overview, segments[1:])
	case "documents":
		h.documents(w, r, overview.Workspace.ID, segments[1:])
	case "payments":
		h.paymentsHistory(w, r, overview.Workspace.ID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) checkout(w http.ResponseWriter, r *http.Request, overview Overview) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.cfg.TopPaymentsCheckoutURL != "" {
		checkoutURL, err := url.Parse(h.cfg.TopPaymentsCheckoutURL)
		if err != nil || checkoutURL.Scheme != "https" {
			api.WriteError(w, http.StatusServiceUnavailable, "checkout_not_configured")
			return
		}
		query := checkoutURL.Query()
		query.Set("workspace_id", strconv.Itoa(overview.Workspace.ID))
		query.Set("amount", strconv.FormatFloat(overview.Subscription.Amount, 'f', 2, 64))
		query.Set("currency", overview.Subscription.Currency)
		query.Set("success_url", h.cfg.FrontendBaseURL+"/account?section=subscription&payment=success")
		query.Set("cancel_url", h.cfg.FrontendBaseURL+"/account?section=subscription&payment=cancelled")
		checkoutURL.RawQuery = query.Encode()
		api.WriteJSON(w, http.StatusOK, map[string]any{
			"provider": "toppayments", "mode": "redirect", "checkout_url": checkoutURL.String(),
		})
		return
	}
	if h.payments == nil || h.payments.PublicID() == "" {
		api.WriteError(w, http.StatusServiceUnavailable, "checkout_not_configured")
		return
	}
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, h.payments.TrialDays())
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"provider": "cloudpayments",
		"mode":     "widget",
		"config": map[string]any{
			"public_id": h.payments.PublicID(), "description": h.payments.PlanName(),
			"first_payment_amount": h.payments.FirstPaymentAmount(), "amount": h.payments.Amount(),
			"currency": h.payments.Currency(), "account_id": "reup_user_" + strconv.Itoa(overview.Account.ID),
			"email": overview.Account.Email, "trial_days": h.payments.TrialDays(),
			"start_date": startDate.Format(time.RFC3339),
		},
	})
}

func (h *Handler) billingOrganization(w http.ResponseWriter, r *http.Request, userID, workspaceID int) {
	switch r.Method {
	case http.MethodGet:
		result, err := h.store.BillingOrganization(r.Context(), workspaceID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "billing_organization_load_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"organization": result})
	case http.MethodPatch:
		var body BillingOrganization
		if !decodeJSON(w, r, &body) {
			return
		}
		body = normalizeOrganization(body)
		if !validOrganization(body) {
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_billing_organization")
			return
		}
		result, err := h.store.SaveBillingOrganization(r.Context(), workspaceID, userID, body)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "billing_organization_update_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, result)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) invoices(w http.ResponseWriter, r *http.Request, userID int, overview Overview, segments []string) {
	if len(segments) == 0 {
		switch r.Method {
		case http.MethodGet:
			items, err := h.store.Invoices(r.Context(), overview.Workspace.ID)
			if err != nil {
				api.WriteError(w, http.StatusInternalServerError, "invoices_load_failed")
				return
			}
			api.WriteJSON(w, http.StatusOK, map[string]any{"invoices": items})
		case http.MethodPost:
			amount := overview.Subscription.Amount
			currency := overview.Subscription.Currency
			if amount <= 0 && h.payments != nil {
				amount = h.payments.Amount()
				currency = h.payments.Currency()
			}
			if amount <= 0 {
				api.WriteError(w, http.StatusServiceUnavailable, "billing_plan_not_configured")
				return
			}
			item, err := h.store.CreateInvoice(r.Context(), overview.Workspace.ID, userID, amount, currency)
			if err != nil {
				if strings.Contains(err.Error(), "billing_organization_required") {
					api.WriteError(w, http.StatusUnprocessableEntity, "billing_organization_required")
					return
				}
				api.WriteError(w, http.StatusInternalServerError, "invoice_create_failed")
				return
			}
			api.WriteJSON(w, http.StatusCreated, item)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}
	if len(segments) != 2 || segments[1] != "email" || r.Method != http.MethodPost {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	invoiceID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil || invoiceID <= 0 {
		api.WriteError(w, http.StatusBadRequest, "invalid_invoice_id")
		return
	}
	items, err := h.store.Invoices(r.Context(), overview.Workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "invoice_load_failed")
		return
	}
	var invoice *Invoice
	for index := range items {
		if items[index].ID == invoiceID {
			invoice = &items[index]
			break
		}
	}
	if invoice == nil {
		api.WriteError(w, http.StatusNotFound, "invoice_not_found")
		return
	}
	if invoice.DocumentID == nil {
		api.WriteError(w, http.StatusInternalServerError, "invoice_document_not_found")
		return
	}
	fileName, _, content, err := h.store.DocumentContent(r.Context(), overview.Workspace.ID, *invoice.DocumentID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "invoice_document_load_failed")
		return
	}
	bodyHTML := fmt.Sprintf(
		"<p>Счёт <strong>%s</strong> на сумму %.2f %s сформирован в REUP.goals.</p><p>PDF-файл приложен к письму и остаётся доступен в разделе «Подписка и биллинг».</p>",
		html.EscapeString(invoice.Number), invoice.Amount, html.EscapeString(invoice.Currency),
	)
	if h.emailService == nil || h.emailService.SendServiceEmailAttachment(
		invoice.RecipientEmail, "Счёт "+invoice.Number+" от REUP.goals", bodyHTML, fileName, content,
	) != nil {
		api.WriteError(w, http.StatusBadGateway, "invoice_email_failed")
		return
	}
	updated, err := h.store.MarkInvoiceEmailed(r.Context(), overview.Workspace.ID, invoiceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "invoice_update_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) documents(w http.ResponseWriter, r *http.Request, workspaceID int, segments []string) {
	if len(segments) == 0 {
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		items, err := h.store.Documents(r.Context(), workspaceID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "billing_documents_load_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"documents": items})
		return
	}
	if len(segments) != 2 || segments[1] != "download" || r.Method != http.MethodGet {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	documentID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil || documentID <= 0 {
		api.WriteError(w, http.StatusBadRequest, "invalid_document_id")
		return
	}
	fileName, mimeType, content, err := h.store.DocumentContent(r.Context(), workspaceID, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusNotFound, "document_not_found")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "document_download_failed")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", strings.ReplaceAll(fileName, "\"", "")))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *Handler) paymentsHistory(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	items, err := h.store.Payments(r.Context(), workspaceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "payments_load_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"payments": items})
}

func (h *Handler) about(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	supportURL := "mailto:" + h.cfg.SupportEmail
	api.WriteJSON(w, http.StatusOK, About{
		Version: h.cfg.AppVersion, ChangelogURL: h.cfg.ChangelogURL,
		DocumentationURL: h.cfg.DocumentationURL, SupportEmail: h.cfg.SupportEmail,
		BugReportURL: supportURL + "?subject=" + url.QueryEscape("Ошибка в REUP.goals"),
		IdeaURL:      supportURL + "?subject=" + url.QueryEscape("Идея для REUP.goals"),
	})
}

func (h *Handler) checkoutAvailable() bool {
	return h.cfg.TopPaymentsCheckoutURL != "" || (h.payments != nil && h.payments.PublicID() != "")
}

func (h *Handler) loadOverview(r *http.Request, userID int) (Overview, error) {
	result, err := h.store.Overview(r.Context(), userID, h.checkoutAvailable())
	if err != nil {
		return Overview{}, err
	}
	if result.Subscription.Amount <= 0 && h.payments != nil {
		result.Subscription.Amount = h.payments.Amount()
		result.Subscription.Currency = h.payments.Currency()
		result.Subscription.Plan = h.payments.PlanName()
	}
	return result, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func splitPath(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, strings.TrimSpace(part))
		}
	}
	return result
}

func normalizeEmail(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(parsed.Address, value) || len(value) > 320 {
		return "", false
	}
	return value, true
}

func validOptionalHTTPURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func validSettings(value Settings) bool {
	return oneOf(value.InterfaceLanguage, "ru", "en") &&
		oneOf(value.Theme, "dark", "system") &&
		oneOf(value.DateFormat, "DD.MM.YYYY", "YYYY-MM-DD", "MM/DD/YYYY") &&
		oneOf(value.AILanguage, "ru", "en")
}

func normalizeOrganization(value BillingOrganization) BillingOrganization {
	value.FullName = strings.TrimSpace(value.FullName)
	value.INN = digitsOnly(value.INN)
	value.KPP = digitsOnly(value.KPP)
	value.RegistrationNumber = digitsOnly(value.RegistrationNumber)
	value.LegalAddress = strings.TrimSpace(value.LegalAddress)
	value.AccountingEmail = strings.ToLower(strings.TrimSpace(value.AccountingEmail))
	value.ContactPerson = strings.TrimSpace(value.ContactPerson)
	return value
}

func validOrganization(value BillingOrganization) bool {
	_, emailOK := normalizeEmail(value.AccountingEmail)
	return len([]rune(value.FullName)) >= 3 && len([]rune(value.FullName)) <= 300 &&
		(len(value.INN) == 10 || len(value.INN) == 12) &&
		(value.KPP == "" || len(value.KPP) == 9) &&
		(len(value.RegistrationNumber) == 13 || len(value.RegistrationNumber) == 15) &&
		len([]rune(value.LegalAddress)) >= 8 && len([]rune(value.LegalAddress)) <= 500 &&
		emailOK && len([]rune(value.ContactPerson)) >= 2 && len([]rune(value.ContactPerson)) <= 160
}

func digitsOnly(value string) string {
	var result strings.Builder
	for _, character := range strings.TrimSpace(value) {
		if character >= '0' && character <= '9' {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
