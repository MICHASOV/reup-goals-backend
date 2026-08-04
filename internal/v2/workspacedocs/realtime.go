package workspacedocs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type documentStreamKey struct {
	workspaceID int
	documentID  int64
}

type documentHub struct {
	mu          sync.RWMutex
	subscribers map[documentStreamKey]map[chan Document]struct{}
}

func newDocumentHub() *documentHub {
	return &documentHub{subscribers: make(map[documentStreamKey]map[chan Document]struct{})}
}

func (h *documentHub) subscribe(key documentStreamKey) (<-chan Document, func()) {
	updates := make(chan Document, 4)
	h.mu.Lock()
	if h.subscribers[key] == nil {
		h.subscribers[key] = make(map[chan Document]struct{})
	}
	h.subscribers[key][updates] = struct{}{}
	h.mu.Unlock()

	return updates, func() {
		h.mu.Lock()
		delete(h.subscribers[key], updates)
		if len(h.subscribers[key]) == 0 {
			delete(h.subscribers, key)
		}
		h.mu.Unlock()
		close(updates)
	}
}

func (h *documentHub) publish(key documentStreamKey, document Document) {
	h.mu.RLock()
	for subscriber := range h.subscribers[key] {
		select {
		case subscriber <- document:
		default:
		}
	}
	h.mu.RUnlock()
}

func (h *Handler) streamDocument(w http.ResponseWriter, r *http.Request, workspaceID int, documentID int64) {
	if r.Method != http.MethodGet {
		apiWriteMethodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming_unsupported", http.StatusInternalServerError)
		return
	}
	updates, unsubscribe := h.hub.subscribe(documentStreamKey{workspaceID: workspaceID, documentID: documentID})
	defer unsubscribe()

	document, err := h.store.Get(r.Context(), workspaceID, documentID)
	if err != nil {
		writeDocumentError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeDocumentEvent(w, document)
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case document, open := <-updates:
			if !open {
				return
			}
			writeDocumentEvent(w, document)
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeDocumentEvent(w http.ResponseWriter, document Document) {
	payload, err := json.Marshal(document)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: document\ndata: %s\n\n", payload)
}

func apiWriteMethodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMethodNotAllowed)
	_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
}
