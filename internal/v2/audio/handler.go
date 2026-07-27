package audio

import (
	"database/sql"
	"net/http"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/security"
	"reup-goals-backend/internal/v2/api"
)

const maxAudioFileBytes = 25 << 20
const maxAudioUploadBytes = maxAudioFileBytes + (1 << 20)

type Handler struct {
	dbx *sql.DB
	ai  *ai.OpenAIClient
}

func NewHandler(dbx *sql.DB, aiClient *ai.OpenAIClient) *Handler {
	return &Handler{dbx: dbx, ai: aiClient}
}

func (h *Handler) Transcriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUploadBytes)
	// #nosec G120 -- MaxBytesReader above enforces a hard request limit.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_audio_upload")
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "audio_file_required")
		return
	}
	defer file.Close()
	if err := security.ValidateAudio(header.Filename, header.Size, maxAudioFileBytes); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	language := strings.TrimSpace(r.FormValue("language"))
	if language == "" {
		language = "ru"
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	workspaceID := h.workspaceID(r, userID)
	aiCtx := ai.WithScenario(r.Context(), workspaceID, userID, "audio_transcription", "v1")
	text, err := h.ai.TranscribeAudio(aiCtx, security.SafeFilename(header.Filename), language, file)
	if err != nil {
		api.WriteAIError(w, err, http.StatusBadGateway, "audio_transcription_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (h *Handler) workspaceID(r *http.Request, userID int) int {
	if h.dbx == nil || userID <= 0 {
		return 0
	}
	var workspaceID int
	_ = h.dbx.QueryRowContext(r.Context(), `
		SELECT workspace_id
		FROM workspace_memberships
		WHERE user_id=$1 AND status='active'
		ORDER BY is_default DESC, created_at
		LIMIT 1
	`, userID).Scan(&workspaceID)
	return workspaceID
}
