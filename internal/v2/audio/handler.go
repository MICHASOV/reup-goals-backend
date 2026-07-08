package audio

import (
	"net/http"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/api"
)

const maxAudioUploadBytes = 26 << 20

type Handler struct {
	ai *ai.OpenAIClient
}

func NewHandler(aiClient *ai.OpenAIClient) *Handler {
	return &Handler{ai: aiClient}
}

func (h *Handler) Transcriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUploadBytes)
	if err := r.ParseMultipartForm(maxAudioUploadBytes); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_audio_upload")
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "audio_file_required")
		return
	}
	defer file.Close()

	language := strings.TrimSpace(r.FormValue("language"))
	if language == "" {
		language = "ru"
	}

	text, err := h.ai.TranscribeAudio(r.Context(), header.Filename, language, file)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "audio_transcription_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]string{"text": text})
}
