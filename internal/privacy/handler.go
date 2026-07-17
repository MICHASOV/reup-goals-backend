package privacy

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/legal"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/operations"
)

const subjectRequestSLA = 10 * 24 * time.Hour

type Handler struct {
	dbx *sql.DB
}

type acceptanceView struct {
	DocumentType string     `json:"document_type"`
	Version      string     `json:"version"`
	Accepted     bool       `json:"accepted"`
	RecordedAt   time.Time  `json:"recorded_at"`
	WithdrawnAt  *time.Time `json:"withdrawn_at,omitempty"`
}

type requestView struct {
	ID                int64      `json:"id"`
	Type              string     `json:"type"`
	Status            string     `json:"status"`
	Details           string     `json:"details"`
	ResolutionSummary string     `json:"resolution_summary,omitempty"`
	ReceivedAt        time.Time  `json:"received_at"`
	DueAt             time.Time  `json:"due_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

func NewHandler(dbx *sql.DB) *Handler {
	return &Handler{dbx: dbx}
}

func (h *Handler) Documents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"documents": legal.CurrentDocuments(),
		"version":   legal.CurrentDocumentVersion,
	})
}

func (h *Handler) Acceptances(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listAcceptances(w, r, userID)
	case http.MethodPost:
		h.updateMarketingAcceptance(w, r, userID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) Requests(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listRequests(w, r, userID)
	case http.MethodPost:
		h.createRequest(w, r, userID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) listAcceptances(w http.ResponseWriter, r *http.Request, userID int) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT DISTINCT ON (document_type)
			document_type, document_version, accepted, recorded_at, withdrawn_at
		FROM legal_acceptances
		WHERE user_id=$1
		ORDER BY document_type, recorded_at DESC, id DESC
	`, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_acceptances_failed")
		return
	}
	defer rows.Close()
	items := make([]acceptanceView, 0)
	for rows.Next() {
		var item acceptanceView
		if err := rows.Scan(&item.DocumentType, &item.Version, &item.Accepted, &item.RecordedAt, &item.WithdrawnAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "privacy_acceptances_failed")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_acceptances_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"acceptances": items})
}

func (h *Handler) updateMarketingAcceptance(w http.ResponseWriter, r *http.Request, userID int) {
	var body struct {
		Accepted bool `json:"accepted"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var subjectKey string
	if err := h.dbx.QueryRowContext(r.Context(), `SELECT privacy_subject_id FROM users WHERE id=$1`, userID).Scan(&subjectKey); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_subject_failed")
		return
	}
	_, err := h.dbx.ExecContext(r.Context(), `
		INSERT INTO legal_acceptances (
			user_id, subject_key, document_type, document_version, accepted,
			legal_basis, source, request_id, withdrawn_at
		)
		VALUES ($1, $2, $3, $4, $5, 'consent', 'account_settings', $6,
			CASE WHEN $5=FALSE THEN NOW() ELSE NULL END)
	`, userID, subjectKey, legal.DocumentMarketing, legal.CurrentDocumentVersion,
		body.Accepted, operations.RequestID(r.Context()))
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_acceptance_update_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "accepted": body.Accepted})
}

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request, userID int) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT id, request_type, status, details, resolution_summary,
			received_at, due_at, completed_at
		FROM privacy_requests
		WHERE user_id=$1
		ORDER BY received_at DESC, id DESC
		LIMIT 100
	`, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_requests_failed")
		return
	}
	defer rows.Close()
	items := make([]requestView, 0)
	for rows.Next() {
		var item requestView
		if err := rows.Scan(&item.ID, &item.Type, &item.Status, &item.Details,
			&item.ResolutionSummary, &item.ReceivedAt, &item.DueAt, &item.CompletedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "privacy_requests_failed")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_requests_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"requests": items})
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request, userID int) {
	var body struct {
		Type    string `json:"type"`
		Details string `json:"details"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	body.Type = strings.ToLower(strings.TrimSpace(body.Type))
	body.Details = strings.TrimSpace(body.Details)
	if !validRequestType(body.Type) || len(body.Details) > 4000 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_privacy_request")
		return
	}
	var subjectKey string
	var workspaceID sql.NullInt64
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT users.privacy_subject_id, membership.workspace_id
		FROM users
		LEFT JOIN LATERAL (
			SELECT workspace_id FROM workspace_memberships
			WHERE user_id=users.id AND status='active'
			ORDER BY is_default DESC, created_at
			LIMIT 1
		) membership ON TRUE
		WHERE users.id=$1
	`, userID).Scan(&subjectKey, &workspaceID)
	if err != nil || subjectKey == "" {
		api.WriteError(w, http.StatusInternalServerError, "privacy_subject_failed")
		return
	}
	dueAt := time.Now().UTC().Add(subjectRequestSLA)
	var item requestView
	err = h.dbx.QueryRowContext(r.Context(), `
		INSERT INTO privacy_requests (
			user_id, workspace_id, subject_key, request_type, details, request_id, due_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, request_type, status, details, resolution_summary,
			received_at, due_at, completed_at
	`, userID, workspaceID, subjectKey, body.Type, body.Details,
		operations.RequestID(r.Context()), dueAt).Scan(
		&item.ID, &item.Type, &item.Status, &item.Details, &item.ResolutionSummary,
		&item.ReceivedAt, &item.DueAt, &item.CompletedAt,
	)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "privacy_request_create_failed")
		return
	}
	api.WriteJSON(w, http.StatusCreated, item)
}

func validRequestType(value string) bool {
	switch value {
	case "access", "export", "rectification", "restriction", "objection", "erasure":
		return true
	default:
		return false
	}
}
