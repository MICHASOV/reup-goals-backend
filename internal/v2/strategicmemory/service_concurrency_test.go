package strategicmemory

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRunBoundedLoadsRespectsLimit(t *testing.T) {
	const limit = 3
	var mu sync.Mutex
	running := 0
	peak := 0
	loads := make([]func() error, 12)
	for index := range loads {
		loads[index] = func() error {
			mu.Lock()
			running++
			if running > peak {
				peak = running
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			running--
			mu.Unlock()
			return nil
		}
	}

	if err := runBoundedLoads(limit, loads); err != nil {
		t.Fatalf("runBoundedLoads returned error: %v", err)
	}
	if peak > limit {
		t.Fatalf("peak concurrency = %d, want at most %d", peak, limit)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency = %d, expected concurrent loading", peak)
	}
}

func TestRunBoundedLoadsReturnsAnErrorAfterAllLoadsFinish(t *testing.T) {
	wantErr := errors.New("load failed")
	completed := 0
	var mu sync.Mutex
	loads := []func() error{
		func() error { return wantErr },
		func() error {
			mu.Lock()
			completed++
			mu.Unlock()
			return nil
		},
	}

	if err := runBoundedLoads(2, loads); !errors.Is(err, wantErr) {
		t.Fatalf("runBoundedLoads error = %v, want %v", err, wantErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if completed != 1 {
		t.Fatalf("completed loads = %d, want 1", completed)
	}
}
