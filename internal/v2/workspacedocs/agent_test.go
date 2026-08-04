package workspacedocs

import "testing"

func TestDecodeAgentDocumentPayload(t *testing.T) {
	created, err := decodeAgentDocumentPayload("propose_document", map[string]any{
		"title": "  Регламент продаж  ", "content": "  # Регламент\n\nПорядок работы.  ",
		"linked_direction_ids": []any{4.0, 7.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Регламент продаж" || created.Content == "" || len(created.DirectionIDs) != 2 {
		t.Fatalf("unexpected create payload: %#v", created)
	}

	updated, err := decodeAgentDocumentPayload("update_document", map[string]any{
		"document_id": 12.0, "base_version": 3.0, "title": "Регламент", "content": "Новая версия",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DocumentID != 12 || updated.BaseVersion != 3 {
		t.Fatalf("unexpected update payload: %#v", updated)
	}
}

func TestDecodeAgentDocumentPayloadRejectsUnsafeUpdate(t *testing.T) {
	if _, err := decodeAgentDocumentPayload("update_document", map[string]any{
		"document_id": 12, "title": "Регламент", "content": "Новая версия",
	}); err == nil {
		t.Fatal("update without the read base version must be rejected")
	}
}
