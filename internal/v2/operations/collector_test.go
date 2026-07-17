package operations

import "testing"

func TestNormalizedPath(t *testing.T) {
	got := normalizedPath("/api/v2/tasks/42/status")
	if got != "/api/v2/tasks/:id/status" {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestEventName(t *testing.T) {
	got := eventName("POST", "/api/v2/strategy/17/activate")
	if got != "post.api.v2.strategy.:id.activate" {
		t.Fatalf("unexpected event name: %s", got)
	}
}
