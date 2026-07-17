package jobs

import (
	"testing"
	"time"
)

func TestRetryDelayUsesCappedExponentialBackoff(t *testing.T) {
	if got := retryDelay(1); got != 10*time.Second {
		t.Fatalf("unexpected first retry delay: %s", got)
	}
	if got := retryDelay(20); got != 5*time.Minute {
		t.Fatalf("unexpected capped delay: %s", got)
	}
}
