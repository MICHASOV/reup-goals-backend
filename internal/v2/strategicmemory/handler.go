package strategicmemory

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

const maxStrategicFileUploadBytes = 80 << 20

type Handler struct {
	store      *Store
	service    *Service
	workspaces *workspaces.Store
}

func NewHandler(dbx *sql.DB, aiClient *ai.OpenAIClient, compactThreshold int) *Handler {
	store := NewStore(dbx)
	return &Handler{
		store:      store,
		service:    NewService(store, aiClient, compactThreshold),
		workspaces: workspaces.NewStore(dbx),
	}
}

func (h *Handler) StrategicDirector(w http.ResponseWriter, r *http.Request) {
	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case r.URL.Path == "/api/v2/strategic-director/messages":
		h.messages(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-director/state":
		h.state(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-director/files":
		h.files(w, r, workspace.ID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) StrategicMemory(w http.ResponseWriter, r *http.Request) {
	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case r.URL.Path == "/api/v2/strategic-memory/snapshot":
		h.state(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-memory/claims":
		h.claims(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-memory/agenda":
		h.agenda(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-memory/documents":
		h.documents(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-memory/quality-audit":
		h.qualityAudit(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/strategic-memory/reset":
		h.reset(w, r, workspace.ID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	response, err := h.service.HandleMessage(r.Context(), workspaceID, userID, req.Message)
	if err != nil {
		if strings.Contains(err.Error(), "message_too_short") {
			api.WriteError(w, http.StatusBadRequest, "message_too_short")
			return
		}
		if strings.Contains(err.Error(), "message_too_long") {
			api.WriteError(w, http.StatusBadRequest, "message_too_long")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_message_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) files(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxStrategicFileUploadBytes)
	if err := r.ParseMultipartForm(maxStrategicFileUploadBytes); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_file_upload")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "file_required")
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	response, err := h.service.UploadFile(r.Context(), workspaceID, userID, header.Filename, contentType, header.Size, file)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "strategic_file_upload_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) state(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	state, err := h.service.State(r.Context(), workspaceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_state_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, state)
}

func (h *Handler) claims(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	claims, err := h.store.ListClaims(r.Context(), workspaceID, 500)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_claims_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "claims": claims})
}

func (h *Handler) agenda(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	agenda, err := h.store.ListAgenda(r.Context(), workspaceID, 200)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_agenda_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "agenda": agenda})
}

func (h *Handler) documents(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	documents, err := h.store.ListDocuments(r.Context(), workspaceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_documents_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "documents": documents})
}

func (h *Handler) qualityAudit(w http.ResponseWriter, r *http.Request, workspaceID int) {
	switch r.Method {
	case http.MethodGet:
		report, err := h.store.LatestQualityReport(r.Context(), workspaceID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategic_quality_audit_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "quality_report": report})
	case http.MethodPost:
		var req struct {
			ChangedDocumentTypes []string `json:"changed_document_types"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		report, err := h.service.RunQualityAudit(r.Context(), workspaceID, req.ChangedDocumentTypes, "manual")
		if err != nil {
			if strings.Contains(err.Error(), "quality_audit_no_documents") {
				api.WriteError(w, http.StatusBadRequest, "quality_audit_no_documents")
				return
			}
			api.WriteError(w, http.StatusInternalServerError, "strategic_quality_audit_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "quality_report": report})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if err := h.service.Reset(r.Context(), workspaceID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategic_memory_reset_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace_id": workspaceID})
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, false
	}

	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), uid)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return workspaces.Workspace{}, false
	}
	return workspace, true
}
