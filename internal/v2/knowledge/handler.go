package knowledge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	store      *Store
	intake     *IntakeService
	guidance   *GuidanceService
	workspaces *workspaces.Store
}

func NewHandler(dbx *sql.DB, intakeAIClient *ai.OpenAIClient, guidanceAIClient *ai.OpenAIClient) *Handler {
	store := NewStore(dbx)
	intake := NewIntakeService(store, intakeAIClient)
	return &Handler{
		store:      store,
		intake:     intake,
		guidance:   NewGuidanceService(store, intake, guidanceAIClient),
		workspaces: workspaces.NewStore(dbx),
	}
}

func (h *Handler) Blocks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/knowledge-base/blocks" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	blocks, err := h.store.List(r.Context(), workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_blocks_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspace.ID,
		"blocks":       blocks,
	})
}

func (h *Handler) Block(w http.ResponseWriter, r *http.Request) {
	blockID, ok := blockIDFromPath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBlock(w, r, workspace.ID, blockID)
	case http.MethodPatch:
		h.updateBlock(w, r, workspace.ID, blockID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) WorkspaceKnowledge(w http.ResponseWriter, r *http.Request) {
	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	workspaceID, ok := debugAICallsPath(r.URL.Path)
	if ok {
		if workspaceID != workspace.ID {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		h.debugAICalls(w, r, workspace.ID)
		return
	}

	workspaceID, action, sessionID, ok := guidancePath(r.URL.Path)
	if ok {
		if workspaceID != workspace.ID {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		h.handleGuidance(w, r, workspace.ID, action, sessionID)
		return
	}

	workspaceID, action, sessionID, ok = intakePath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if workspaceID != workspace.ID {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}

	switch action {
	case "preview":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.previewIntake(w, r, workspace.ID)
	case "confirm":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.confirmIntake(w, r, workspace.ID, sessionID)
	case "reject":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.rejectIntake(w, r, workspace.ID, sessionID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) handleGuidance(w http.ResponseWriter, r *http.Request, workspaceID int, action string, sessionID int) {
	switch action {
	case "bootstrap":
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.guidanceBootstrap(w, r, workspaceID)
	case "preview":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.guidancePreview(w, r, workspaceID)
	case "confirm":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.guidanceConfirm(w, r, workspaceID, sessionID)
	case "reject":
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.guidanceReject(w, r, workspaceID, sessionID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) getBlock(w http.ResponseWriter, r *http.Request, workspaceID int, blockID int) {
	block, err := h.store.Get(r.Context(), workspaceID, blockID)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_block_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"block": block})
}

func (h *Handler) updateBlock(w http.ResponseWriter, r *http.Request, workspaceID int, blockID int) {
	var body struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	body.Status = strings.TrimSpace(body.Status)
	if body.Status != "" && !ValidStatus(body.Status) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}

	block, err := h.store.Update(r.Context(), workspaceID, blockID, body.Content, body.Status)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_block_update_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"block": block})
}

func (h *Handler) previewIntake(w http.ResponseWriter, r *http.Request, workspaceID int) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		RawText string `json:"raw_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	rawText := strings.TrimSpace(body.RawText)
	if len([]rune(rawText)) < 20 {
		api.WriteError(w, http.StatusUnprocessableEntity, "raw_text_too_short")
		return
	}
	if len([]rune(rawText)) > 50000 {
		api.WriteError(w, http.StatusRequestEntityTooLarge, "raw_text_too_long")
		return
	}

	preview, err := h.intake.BuildPreview(r.Context(), workspaceID, uid, rawText)
	if err != nil {
		log.Printf("[WARN] knowledge intake preview failed workspace_id=%d user_id=%d: %v", workspaceID, uid, err)
		api.WriteError(w, http.StatusBadGateway, "knowledge_intake_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, preview)
}

func (h *Handler) guidanceBootstrap(w http.ResponseWriter, r *http.Request, workspaceID int) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	response, err := h.guidance.Bootstrap(r.Context(), workspaceID, uid)
	if err != nil {
		log.Printf("[WARN] knowledge guidance bootstrap failed workspace_id=%d user_id=%d: %v", workspaceID, uid, err)
		api.WriteError(w, http.StatusInternalServerError, "knowledge_guidance_bootstrap_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) guidancePreview(w http.ResponseWriter, r *http.Request, workspaceID int) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		QuestionBlockID int    `json:"question_block_id"`
		RawText         string `json:"raw_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	rawText := strings.TrimSpace(body.RawText)
	if len([]rune(rawText)) < 2 {
		api.WriteError(w, http.StatusUnprocessableEntity, "raw_text_too_short")
		return
	}
	if len([]rune(rawText)) > 50000 {
		api.WriteError(w, http.StatusRequestEntityTooLarge, "raw_text_too_long")
		return
	}

	response, err := h.guidance.PreviewAnswer(r.Context(), workspaceID, uid, body.QuestionBlockID, rawText)
	if err != nil {
		if err.Error() == "invalid_question_block" {
			api.WriteError(w, http.StatusConflict, "invalid_question_block")
			return
		}
		log.Printf("[WARN] knowledge guidance preview failed workspace_id=%d user_id=%d: %v", workspaceID, uid, err)
		api.WriteError(w, http.StatusBadGateway, "knowledge_guidance_preview_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) guidanceConfirm(w http.ResponseWriter, r *http.Request, workspaceID int, sessionID int) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		AcceptedPatchIDs    []int `json:"accepted_patch_ids"`
		ConflictResolutions []struct {
			ConflictID     int    `json:"conflict_id"`
			SelectedOption string `json:"selected_option"`
		} `json:"conflict_resolutions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	resolutions := make([]conflictResolution, 0, len(body.ConflictResolutions))
	for _, resolution := range body.ConflictResolutions {
		selected := strings.TrimSpace(resolution.SelectedOption)
		if selected == "option_a" {
			selected = ConflictOptionExisting
		}
		if selected == "option_b" {
			selected = ConflictOptionNew
		}
		resolutions = append(resolutions, conflictResolution{
			ConflictID:     resolution.ConflictID,
			SelectedOption: selected,
		})
	}

	response, err := h.guidance.Confirm(r.Context(), workspaceID, uid, sessionID, body.AcceptedPatchIDs, resolutions)
	if err != nil {
		switch err.Error() {
		case "unresolved_conflicts":
			api.WriteError(w, http.StatusConflict, "unresolved_conflicts")
		case "session_already_confirmed":
			api.WriteError(w, http.StatusConflict, "session_already_confirmed")
		case "invalid_conflict_resolution":
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_conflict_resolution")
		default:
			log.Printf("[WARN] knowledge guidance confirm failed workspace_id=%d user_id=%d session_id=%d: %v", workspaceID, uid, sessionID, err)
			api.WriteError(w, http.StatusInternalServerError, "knowledge_guidance_confirm_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) guidanceReject(w http.ResponseWriter, r *http.Request, workspaceID int, sessionID int) {
	question, err := h.guidance.Reject(r.Context(), workspaceID, sessionID)
	if err != nil {
		if err.Error() == "session_already_confirmed" {
			api.WriteError(w, http.StatusConflict, "session_already_confirmed")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "knowledge_guidance_reject_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"status":                SessionRejected,
		"active_question_block": question,
	})
}

func (h *Handler) debugAICalls(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	limit := 30
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			api.WriteError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = parsed
	}

	items, err := h.store.RecentAICallLogs(r.Context(), workspaceID, limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "ai_call_logs_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspaceID,
		"items":        items,
	})
}

func (h *Handler) confirmIntake(w http.ResponseWriter, r *http.Request, workspaceID int, sessionID int) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		AcceptedPatchIDs    []int `json:"accepted_patch_ids"`
		ConflictResolutions []struct {
			ConflictID     int    `json:"conflict_id"`
			SelectedOption string `json:"selected_option_id"`
		} `json:"conflict_resolutions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	resolutions := make([]conflictResolution, 0, len(body.ConflictResolutions))
	for _, resolution := range body.ConflictResolutions {
		resolutions = append(resolutions, conflictResolution{
			ConflictID:     resolution.ConflictID,
			SelectedOption: strings.TrimSpace(resolution.SelectedOption),
		})
	}

	result, err := h.store.ConfirmIntake(r.Context(), workspaceID, uid, sessionID, body.AcceptedPatchIDs, resolutions)
	if err != nil {
		switch err.Error() {
		case "unresolved_conflicts":
			api.WriteError(w, http.StatusConflict, "unresolved_conflicts")
		case "session_already_confirmed":
			api.WriteError(w, http.StatusConflict, "session_already_confirmed")
		case "invalid_conflict_resolution":
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_conflict_resolution")
		default:
			api.WriteError(w, http.StatusInternalServerError, "knowledge_intake_confirm_failed")
		}
		return
	}

	api.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) rejectIntake(w http.ResponseWriter, r *http.Request, workspaceID int, sessionID int) {
	if err := h.store.RejectIntake(r.Context(), workspaceID, sessionID); err != nil {
		if err.Error() == "session_already_confirmed" {
			api.WriteError(w, http.StatusConflict, "session_already_confirmed")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "knowledge_intake_reject_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"status":     SessionRejected,
	})
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

func blockIDFromPath(path string) (int, bool) {
	const prefix = "/api/v2/knowledge-base/blocks/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}

	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, false
	}

	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, false
	}

	return id, true
}

func intakePath(path string) (workspaceID int, action string, sessionID int, ok bool) {
	const prefix = "/api/v2/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", 0, false
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	if len(parts) == 4 && parts[1] == "knowledge" && parts[2] == "intake" && parts[3] == "preview" {
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return 0, "", 0, false
		}
		return id, "preview", 0, true
	}

	if len(parts) == 5 && parts[1] == "knowledge" && parts[2] == "intake" {
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return 0, "", 0, false
		}
		sid, err := strconv.Atoi(parts[3])
		if err != nil || sid <= 0 {
			return 0, "", 0, false
		}
		if parts[4] != "confirm" && parts[4] != "reject" {
			return 0, "", 0, false
		}
		return id, parts[4], sid, true
	}

	return 0, "", 0, false
}

func guidancePath(path string) (workspaceID int, action string, sessionID int, ok bool) {
	const prefix = "/api/v2/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", 0, false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	if len(parts) == 4 && parts[1] == "knowledge" && parts[2] == "guidance" && parts[3] == "bootstrap" {
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return 0, "", 0, false
		}
		return id, "bootstrap", 0, true
	}
	if len(parts) == 5 && parts[1] == "knowledge" && parts[2] == "guidance" && parts[3] == "answer" && parts[4] == "preview" {
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return 0, "", 0, false
		}
		return id, "preview", 0, true
	}
	if len(parts) == 6 && parts[1] == "knowledge" && parts[2] == "guidance" && parts[3] == "sessions" {
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			return 0, "", 0, false
		}
		sid, err := strconv.Atoi(parts[4])
		if err != nil || sid <= 0 {
			return 0, "", 0, false
		}
		if parts[5] != "confirm" && parts[5] != "reject" {
			return 0, "", 0, false
		}
		return id, parts[5], sid, true
	}
	return 0, "", 0, false
}

func debugAICallsPath(path string) (workspaceID int, ok bool) {
	const prefix = "/api/v2/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	if len(parts) != 4 || parts[1] != "knowledge" || parts[2] != "debug" || parts[3] != "ai-calls" {
		return 0, false
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
