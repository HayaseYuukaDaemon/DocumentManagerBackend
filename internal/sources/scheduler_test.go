package sources

import (
	"context"
	"testing"
	"time"
)

type testRateLimitError struct {
	retryAfter time.Duration
}

func (e *testRateLimitError) Error() string {
	return ErrRateLimited.Error()
}

func (e *testRateLimitError) Unwrap() error {
	return ErrRateLimited
}

func (e *testRateLimitError) RetryAfterDuration() time.Duration {
	return e.retryAfter
}

func TestConcurrencySchedulerAppliesRateLimitOncePerGeneration(t *testing.T) {
	scheduler := NewConcurrencyScheduler(4, 8)
	scheduler.initialBackoff = 10 * time.Millisecond
	scheduler.maxBackoff = 20 * time.Millisecond

	permits := make([]schedulerPermit, 4)
	for index := range permits {
		permit, err := scheduler.acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire permit %d: %v", index, err)
		}
		permits[index] = permit
	}

	rateLimitErr := &testRateLimitError{retryAfter: 15 * time.Millisecond}
	for _, permit := range permits {
		scheduler.finish(permit, rateLimitErr)
	}

	if scheduler.generation != 1 {
		t.Fatalf("generation = %d, want 1", scheduler.generation)
	}
	if scheduler.limit != 2 {
		t.Fatalf("limit = %d, want 2", scheduler.limit)
	}
	if scheduler.backoff != 15*time.Millisecond {
		t.Fatalf("backoff = %s, want 15ms", scheduler.backoff)
	}
}
