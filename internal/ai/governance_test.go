package ai

import (
	"errors"
	"testing"
)

func TestCallRejectedErrorPreservesCause(t *testing.T) {
	err := RejectCall(ErrRateLimitExceeded)
	if !IsCallRejected(err) {
		t.Fatal("expected the error to be marked as rejected before provider execution")
	}
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatal("expected the original rejection code to remain discoverable")
	}
	if got := RejectCall(err); got != err {
		t.Fatal("wrapping an existing rejection must be idempotent")
	}
}
