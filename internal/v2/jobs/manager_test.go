package jobs

import (
	"context"
	"strings"
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

func TestInvokeHandlerConvertsPanicToError(t *testing.T) {
	err := invokeHandler(context.Background(), func(context.Context, Job) error {
		panic("boom")
	}, Job{})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error, got %v", err)
	}
}
