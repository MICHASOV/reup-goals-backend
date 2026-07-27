package billing

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type AdminHandler struct {
	service *Service
	key     string
}

func NewAdminHandler(service *Service, key string) *AdminHandler {
	return &AdminHandler{service: service, key: strings.TrimSpace(key)}
}

func (h *AdminHandler) ConfirmInvoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.key == "" {
		writeError(w, http.StatusServiceUnavailable, "manual_billing_confirmation_not_configured")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Billing-Admin-Key"))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.key)) != 1 {
		writeError(w, http.StatusForbidden, "billing_admin_required")
		return
	}
	var body struct {
		InvoiceID   int64  `json:"invoice_id"`
		ConfirmedBy string `json:"confirmed_by"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.InvoiceID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.service.ConfirmInvoicePayment(r.Context(), body.InvoiceID, body.ConfirmedBy); err != nil {
		if errors.Is(err, ErrPlanNotFound) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "manual_payment_confirmation_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
