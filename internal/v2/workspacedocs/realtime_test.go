package workspacedocs

import (
	"testing"
	"time"
)

func TestDocumentEventIDSuffix(t *testing.T) {
	tests := []struct {
		path string
		id   int64
		ok   bool
	}{
		{path: "/api/v2/workspace-documents/42/events", id: 42, ok: true},
		{path: "/api/v2/workspace-documents/42/events/", id: 42, ok: true},
		{path: "/api/v2/workspace-documents/42", ok: false},
		{path: "/api/v2/workspace-documents/nope/events", ok: false},
		{path: "/api/v2/workspace-documents/0/events", ok: false},
	}
	for _, test := range tests {
		id, ok := documentEventIDSuffix(test.path)
		if id != test.id || ok != test.ok {
			t.Fatalf("documentEventIDSuffix(%q) = (%d, %v), want (%d, %v)", test.path, id, ok, test.id, test.ok)
		}
	}
}

func TestDocumentHubPublishesOnlyToMatchingDocument(t *testing.T) {
	hub := newDocumentHub()
	key := documentStreamKey{workspaceID: 7, documentID: 12}
	updates, unsubscribe := hub.subscribe(key)
	defer unsubscribe()

	hub.publish(documentStreamKey{workspaceID: 7, documentID: 13}, Document{ID: 13})
	hub.publish(key, Document{ID: 12, Version: 3})

	select {
	case update := <-updates:
		if update.ID != 12 || update.Version != 3 {
			t.Fatalf("unexpected document update: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("expected document update")
	}
}
