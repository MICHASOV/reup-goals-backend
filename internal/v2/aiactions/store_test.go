package aiactions

import (
	"context"
	"testing"
)

func TestRegisterRejectsInvalidScopeBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	_, err := store.Register(context.Background(), 0, "", "", 0, 0, nil, nil)
	if err == nil || err.Error() != "invalid_ai_action_scope" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTruncatePreservesUnicodeBoundaries(t *testing.T) {
	if got := truncate("Привет", 4); got != "Прив" {
		t.Fatalf("unexpected truncated value %q", got)
	}
	if got := truncate("ok", 4); got != "ok" {
		t.Fatalf("short value changed to %q", got)
	}
}
